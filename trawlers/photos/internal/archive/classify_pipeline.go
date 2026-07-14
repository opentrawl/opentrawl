package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type classifyWrite struct {
	input               classifyInput
	hasContent          bool
	contentResult       *modelResult
	contentErr          error
	imagePath           string
	pathClass           string
	prepared            preparedCardRequest
	exported            bool
	resolutionErr       error
	resolutionDuration  time.Duration
	exportBytes         int64
	modelAttempts       int
	modelDuration       time.Duration
	writeDuration       time.Duration
	rateLimitEvents     int
	transientErrEvents  int
	outcome             contentOutcome
	generationID        string
	executionID         string
	generationReused    bool
	transmissionStarted bool
}

type contentOutcome string

var extractImageMetadata = photos.ImageMetadataRecord

var errUnknownCurrentStillMIMEType = errors.New("current-still media type is unknown")
var errCardInputNotReady = errors.New("card input not ready")

type currentStillResolver interface {
	Resolve(context.Context, photos.CurrentStillRequest) (photos.CurrentStillResolution, error)
}

var newCurrentStillResolver = func(root string, exporter photos.CurrentStillExporter) (currentStillResolver, error) {
	return photos.NewCurrentStillResolver(root, exporter)
}

const (
	contentOutcomeClassified              contentOutcome = "classified"
	contentOutcomeFailedParse             contentOutcome = "failed_parse"
	contentOutcomeFailedModel             contentOutcome = "failed_model"
	contentOutcomeStoppedUncertain        contentOutcome = "stopped_uncertain"
	contentOutcomeRateLimited             contentOutcome = "rate_limited"
	contentOutcomeCardInputNotReady       contentOutcome = "card_input_not_ready"
	contentOutcomeFailedDownload          contentOutcome = "failed_download"
	contentOutcomeNotInPhotoKit           contentOutcome = "not_in_photokit"
	contentOutcomeNoContentAvailable      contentOutcome = "no_content_available"
	contentOutcomeSkippedUnsupportedMedia contentOutcome = "skipped_unsupported_media"
)

// contentItem is one asset headed for the model, plus everything prepare
// learns about it on the way that commit needs afterwards.
type contentItem struct {
	input     classifyInput
	imagePath string
	pathClass string

	prepared           preparedCardRequest
	generationID       string
	executionID        string
	exported           bool
	resolutionErr      error
	resolutionDuration time.Duration
	exportBytes        int64
}

// classifyContentInputs drives model classification through trawlkit's
// model.Run, which owns the loop guardrails: one send, adaptive concurrency,
// 429 accounting, and the rule-1.15 quota abort.
// photoscrawl keeps what is photoscrawl's: originals export inside prepare,
// card parsing and SQL writes inside commit, and the outcome-to-queue-state
// mapping.
func classifyContentInputs(ctx context.Context, db *store.Store, paths Paths, inputs []classifyInput, classifier modelClassifier, now func() time.Time, result *ClassifyResult, logger classifyLogger) error {
	// Pre-pass: items that never reach the model resolve immediately.
	items := make([]*contentItem, 0, len(inputs))
	for _, input := range inputs {
		if input.MediaType == "image" {
			items = append(items, &contentItem{input: input})
			continue
		}
		write := classifyWrite{input: input, outcome: missingContentOutcome(input)}
		if err := writeClassifyResult(ctx, db, classifier, write, now, result, logger); err != nil {
			return err
		}
	}
	if len(items) == 0 {
		return nil
	}

	prepare := func(ctx context.Context, index int) (model.Call, error) {
		item := items[index]
		startedAt := time.Now()
		prepared, generationID, found, err := restoreRetainedPreparedCardRequestForAsset(ctx, db, item.input.AssetID, classifier.client)
		imagePath := ""
		if err == nil && !found {
			prepared, imagePath, err = prepareClassifyCardRequestFromCache(ctx, paths, item.input, classifier)
		}
		item.resolutionDuration = time.Since(startedAt)
		if err != nil {
			item.resolutionErr = err
			return model.Call{}, err
		}
		item.prepared = prepared
		item.imagePath = imagePath
		item.pathClass = photos.CurrentStillSourceCache
		item.executionID = approvedCardExecutionID(item.input.AssetID, prepared)
		decision := modelGenerationDecision{GenerationID: generationID}
		if found {
			decision, err = prepareModelGeneration(ctx, db, item.input.AssetID, prepared.PromptVersion, prepared.ParserVersion, prepared.Request, now().UTC())
		} else {
			decision, err = prepareModelGenerationForPreparedCard(ctx, db, item.executionID, item.input.AssetID, prepared, now().UTC())
		}
		item.generationID = decision.GenerationID
		if err != nil {
			return model.Call{}, err
		}
		if err := modelGenerationFault(modelGenerationFaultBeforeSend, model.RawResult{}); err != nil {
			return model.Call{}, fmt.Errorf("%w: %v", errModelGenerationStoppedUncertain, err)
		}
		return decision.Call, nil
	}

	commit := func(res model.Result) error {
		item := items[res.Index]
		write := classifyWrite{
			input:              item.input,
			hasContent:         true,
			imagePath:          item.imagePath,
			pathClass:          item.pathClass,
			prepared:           item.prepared,
			exported:           item.exported,
			resolutionErr:      item.resolutionErr,
			resolutionDuration: item.resolutionDuration,
			exportBytes:        item.exportBytes,
			modelAttempts:      res.Attempts,
			modelDuration:      res.Duration,
			rateLimitEvents:    res.RateLimitEvents,
			transientErrEvents: res.TransientEvents,
			generationID:       item.generationID,
			executionID:        item.executionID,
		}
		if errors.Is(res.Err, errModelGenerationUncertain) || errors.Is(res.Err, errModelGenerationStoppedUncertain) {
			write.contentErr = errModelGenerationStoppedUncertain
			write.outcome = contentOutcomeStoppedUncertain
			return writeClassifyResult(ctx, db, classifier, write, now, result, logger)
		}
		if res.Attempts > 0 {
			if err := modelGenerationFault(modelGenerationFaultAfterSend, res.Raw); err != nil {
				write.contentErr = fmt.Errorf("%w: %v", errModelGenerationStoppedUncertain, err)
				write.outcome = contentOutcomeStoppedUncertain
				write.transmissionStarted = res.Raw.TransmissionStarted
				return writeClassifyResult(ctx, db, classifier, write, now, result, logger)
			}
			persistCtx := ctx
			if ctx.Err() != nil {
				persistCtx = context.WithoutCancel(ctx)
			}
			if err := retainModelGenerationResult(persistCtx, db, item.generationID, res.Raw, now().UTC()); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			if err := modelGenerationFault(modelGenerationFaultAfterRetain, res.Raw); err != nil {
				return err
			}
		}
		switch res.Outcome {
		case model.OutcomeOK:
			parsed, err := parseRetainedModelGeneration(ctx, db, item.generationID, item.input.AssetID, classifier, item.prepared, res.Raw, now().UTC())
			if err != nil {
				write.contentErr = err
				write.outcome = modelFailureOutcome(err)
			} else {
				write.contentResult = &parsed
				write.outcome = contentOutcomeClassified
			}
		case model.OutcomeRateLimited:
			// Quota refusal is the provider's state, not the photo's.
			write.contentErr = res.Err
			write.outcome = contentOutcomeRateLimited
		case model.OutcomeFailed:
			write.contentErr = res.Err
			if errors.Is(item.resolutionErr, errCardInputNotReady) {
				write.hasContent = false
				write.outcome = contentOutcomeCardInputNotReady
			} else if item.resolutionErr != nil {
				write.hasContent = false
				write.outcome = downloadFailureOutcome(item.resolutionErr)
			} else {
				write.outcome = modelFailureOutcome(res.Err)
			}
		case model.OutcomeReused:
			write.generationReused = true
			write.outcome = contentOutcomeClassified
		}
		return writeClassifyResult(ctx, db, classifier, write, now, result, logger)
	}

	stats, err := model.Run(ctx, classifier.client, len(items), prepare, commit, runLogger{logger})
	result.RateLimitAborted = stats.Aborted
	result.ModelConcurrencyPeak = stats.ConcurrencyPeak
	result.ModelConcurrencyFinal = stats.ConcurrencyEnd
	return err
}

func currentStillMIMEType(mediaType string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "public.heic":
		return "image/heic", nil
	case "public.heif":
		return "image/heif", nil
	case "public.jpeg", "public.jpg":
		return "image/jpeg", nil
	case "public.png":
		return "image/png", nil
	case "public.tiff":
		return "image/tiff", nil
	default:
		return "", errUnknownCurrentStillMIMEType
	}
}

// runLogger hands trawlkit's model.Run events to the classify log.
type runLogger struct{ logger classifyLogger }

func (l runLogger) Info(event, message string) error {
	l.logger.info(event, message)
	return nil
}

func (l runLogger) Warn(event, message string) error {
	l.logger.warn(event, message)
	return nil
}

func classifyFailureReason(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *model.HTTPError
	if errors.As(err, &httpErr) {
		return fmt.Sprintf("model returned %s", httpErr.Status)
	}
	return truncateReason(err.Error())
}

func writeClassifyResult(ctx context.Context, db *store.Store, classifier modelClassifier, write classifyWrite, now func() time.Time, result *ClassifyResult, logger classifyLogger) error {
	var metadataWritten, contentWritten, placeWritten int
	classifiedAt := now().UTC()
	writeStartedAt := time.Now()
	if write.outcome == contentOutcomeCardInputNotReady {
		write.writeDuration = time.Since(writeStartedAt)
		result.Processed++
		result.addContentOutcome(write.outcome)
		logger.logOutcome(write)
		return nil
	}
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		switch write.outcome {
		case contentOutcomeFailedParse:
			state, reason := contentOutcomeQueueStateReason(write)
			return updateClassificationQueue(ctx, tx, write.input.QueueID, state, reason, classifiedAt)
		case contentOutcomeFailedModel:
			state, reason := contentOutcomeQueueStateReason(write)
			return updateClassificationQueue(ctx, tx, write.input.QueueID, state, reason, classifiedAt)
		case contentOutcomeStoppedUncertain:
			if write.transmissionStarted {
				if err := markModelGenerationTransmissionStarted(ctx, tx, write.generationID); err != nil {
					return err
				}
			}
			_, err := stopModelGenerationUncertain(ctx, tx, write.input.QueueID, write.generationID, classifiedAt)
			return err
		}
		observations := classifyFromMetadata(write.input)
		written, err := writeMetadataClassification(ctx, tx, write.input, observations, classifiedAt, true)
		if err != nil {
			return err
		}
		metadataWritten = written
		switch write.outcome {
		case contentOutcomeClassified:
			if write.generationReused {
				return updateClassificationQueue(ctx, tx, write.input.QueueID, classifyQueueStateContentClassified, "model_observations", classifiedAt)
			}
			if !write.hasContent || write.contentResult == nil {
				return updateClassificationQueue(ctx, tx, write.input.QueueID, "content_failed", "classified outcome missing model result", classifiedAt)
			}
			written, placeWritten, err = writeModelClassification(ctx, tx, write.input, classifier, *write.contentResult, write.prepared, classifiedAt, write.generationID)
			if err != nil {
				return err
			}
			contentWritten = written
			if err := completeModelGeneration(ctx, tx, write.generationID, write.input.AssetID, classifiedAt); err != nil {
				return err
			}
			if err := completePreparedCardRequest(ctx, tx, write.executionID, classifiedAt.Format(time.RFC3339Nano)); err != nil {
				return err
			}
		default:
			state, reason := contentOutcomeQueueStateReason(write)
			return updateClassificationQueue(ctx, tx, write.input.QueueID, state, reason, classifiedAt)
		}
		return nil
	})
	if err != nil {
		return err
	}
	write.writeDuration = time.Since(writeStartedAt)

	result.Processed++
	result.MetadataClassified++
	result.MetadataObservationsWritten += metadataWritten
	result.addContentOutcome(write.outcome)
	if write.outcome == contentOutcomeClassified {
		result.ContentObservationsWritten += contentWritten
		result.PlaceObservationsWritten += placeWritten
	}
	if write.resolutionErr != nil {
		result.OriginalResolutionFailures++
	}
	if write.exported {
		result.PhotoKitExports++
		result.PhotoKitExportBytes += write.exportBytes
	}
	result.OriginalResolutionMillis += write.resolutionDuration.Milliseconds()
	result.ModelCallAttempts += write.modelAttempts
	result.ModelCallMillis += write.modelDuration.Milliseconds()
	result.ModelRateLimitEvents += write.rateLimitEvents
	result.ModelTransientErrorEvents += write.transientErrEvents
	logger.logOutcome(write)
	return nil
}

func prepareClassifyCardRequestFromCache(ctx context.Context, paths Paths, input classifyInput, classifier modelClassifier) (preparedCardRequest, string, error) {
	if input.SourceState != sourceStateCurrent {
		return preparedCardRequest{}, "", errCardInputNotReady
	}
	if input.HasLocation {
		return preparedCardRequest{}, "", errCardInputNotReady
	}
	original, _, _, ok, err := cardInputAuditCheckedOriginal(input, paths.OriginalsCacheDir())
	if err != nil {
		return preparedCardRequest{}, "", err
	}
	if !ok {
		return preparedCardRequest{}, "", errCardInputNotReady
	}
	metadata, ok := imagemetadata.ReadCheckedArtifacts(filepath.Join(paths.CacheDir, "image-metadata"), original.SHA256)
	if !ok {
		return preparedCardRequest{}, "", errCardInputNotReady
	}
	currentRequest, err := input.currentStillRequest()
	if err != nil {
		return preparedCardRequest{}, "", err
	}
	path, current, proofSHA256, ok := photos.ReadCachedCurrentStill(paths.OriginalsCacheDir(), currentRequest.SourceLibraryID, currentRequest.AssetUUID, currentRequest.Freshness)
	if !ok {
		return preparedCardRequest{}, "", errCardInputNotReady
	}
	image, err := os.ReadFile(path)
	if err != nil {
		return preparedCardRequest{}, "", err
	}
	source, artifacts := cardInputAuditFacts(input, original, metadata, current, proofSHA256)
	prepared, err := renderPreparedCardRequest(source, artifacts, nil, image, classifier)
	if err != nil {
		return preparedCardRequest{}, "", err
	}
	if err := ctx.Err(); err != nil {
		return preparedCardRequest{}, "", err
	}
	return prepared, path, nil
}

func missingContentOutcome(input classifyInput) contentOutcome {
	if input.MediaType != "image" {
		return contentOutcomeSkippedUnsupportedMedia
	}
	return contentOutcomeNoContentAvailable
}

func downloadFailureOutcome(err error) contentOutcome {
	if errors.Is(err, photos.ErrPhotoKitAssetNotFound) {
		return contentOutcomeNotInPhotoKit
	}
	return contentOutcomeFailedDownload
}

func modelFailureOutcome(err error) contentOutcome {
	if isModelParseFailure(err) {
		return contentOutcomeFailedParse
	}
	return contentOutcomeFailedModel
}

func isModelParseFailure(err error) bool {
	return errors.Is(err, errModelCardParse) || errors.Is(err, errUnknownCardCandidate)
}

func contentOutcomeQueueStateReason(write classifyWrite) (string, string) {
	switch write.outcome {
	case contentOutcomeFailedParse:
		return classifyQueueStateMetadataClassified, "retained_response_failed_parse: " + classifyFailureReason(write.contentErr)
	case contentOutcomeFailedModel:
		return "content_failed", "failed_model: " + classifyFailureReason(write.contentErr)
	case contentOutcomeStoppedUncertain:
		return "content_failed", "stopped_uncertain: model attempt has no retained result"
	case contentOutcomeRateLimited:
		return "metadata_classified", "requeued: model rate limited (429)"
	case contentOutcomeCardInputNotReady:
		return "", "card input not ready"
	case contentOutcomeFailedDownload:
		return "failed_download", "failed_download: " + classifyFailureReason(write.resolutionErr)
	case contentOutcomeNotInPhotoKit:
		return "content_not_in_photokit", "PhotoKit asset not found"
	case contentOutcomeNoContentAvailable:
		return "content_no_content_available", "no classifiable image content available"
	case contentOutcomeSkippedUnsupportedMedia:
		mediaType := strings.TrimSpace(write.input.MediaType)
		if mediaType == "" {
			mediaType = "unknown"
		}
		return "content_skipped", "skipped_unsupported_media: " + mediaType
	default:
		return "content_failed", "unknown content outcome"
	}
}

func (result *ClassifyResult) addContentOutcome(outcome contentOutcome) {
	switch outcome {
	case contentOutcomeClassified:
		result.ContentClassified++
	case contentOutcomeFailedParse:
		result.ContentFailedParse++
		result.ContentClassificationFailures++
	case contentOutcomeFailedModel:
		result.ContentFailedModel++
		result.ContentClassificationFailures++
	case contentOutcomeStoppedUncertain:
		result.ContentStoppedUncertain++
	case contentOutcomeRateLimited:
		result.RateLimitRequeued++
	case contentOutcomeCardInputNotReady:
		result.CardInputNotReady++
	case contentOutcomeFailedDownload:
		result.ContentFailedDownload++
	case contentOutcomeNotInPhotoKit:
		result.ContentNotInPhotoKit++
	case contentOutcomeNoContentAvailable:
		result.ContentNoContentAvailable++
		result.WaitingForLocalContent++
	case contentOutcomeSkippedUnsupportedMedia:
		result.ContentSkippedUnsupportedMedia++
	default:
		return
	}
	result.ContentOutcomeTotal++
}
