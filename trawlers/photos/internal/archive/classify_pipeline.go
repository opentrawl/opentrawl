package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type classifyWrite struct {
	input              classifyInput
	hasContent         bool
	contentResult      *modelResult
	contentErr         error
	imagePath          string
	pathClass          string
	exported           bool
	resolutionErr      error
	resolutionDuration time.Duration
	exportBytes        int64
	modelAttempts      int
	modelDuration      time.Duration
	writeDuration      time.Duration
	rateLimitEvents    int
	transientErrEvents int
	outcome            contentOutcome
}

type contentOutcome string

const (
	contentOutcomeClassified              contentOutcome = "classified"
	contentOutcomeFailedParse             contentOutcome = "failed_parse"
	contentOutcomeFailedModel             contentOutcome = "failed_model"
	contentOutcomeRateLimited             contentOutcome = "rate_limited"
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

	meta               imageMeta
	exported           bool
	resolutionErr      error
	resolutionDuration time.Duration
	exportBytes        int64
}

// classifyContentInputs drives model classification through trawlkit's
// model.Run, which owns the loop guardrails: bounded retries, adaptive
// concurrency, 429-requeue-never-fail, and the rule-1.15 quota abort.
// photoscrawl keeps what is photoscrawl's: originals export inside prepare,
// card parsing and SQL writes inside commit, and the outcome-to-queue-state
// mapping.
func classifyContentInputs(ctx context.Context, db *store.Store, paths Paths, inputs []classifyInput, classifier modelClassifier, now func() time.Time, result *ClassifyResult, logger classifyLogger) error {
	resolver, err := photos.NewOriginalResolver(paths.OriginalsCacheDir(), exportOriginalResource)
	if err != nil {
		return err
	}

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

	prepare := func(ctx context.Context, index int) (model.Request, error) {
		item := items[index]
		startedAt := time.Now()
		resolved, err := resolver.Resolve(ctx, item.input.originalRequest())
		item.resolutionDuration = time.Since(startedAt)
		if err != nil {
			item.resolutionErr = err
			return model.Request{}, err
		}
		item.exported = resolved.Exported
		if resolved.Exported {
			item.exportBytes = resolved.Size
		}
		item.imagePath = resolved.Path
		item.pathClass = resolved.Source
		logger.logOriginalResolved(item.input, resolved)
		// The request owns its image bytes after buildRequest returns.
		// Hold the cache lease until then so another process cannot evict it.
		defer func() {
			if resolved.Lease != nil {
				resolved.Lease.Close()
			}
		}()
		request, meta, err := classifier.buildRequest(item.input, item.imagePath)
		if err != nil {
			return model.Request{}, err
		}
		item.meta = meta
		return request, nil
	}

	commit := func(res model.Result) error {
		item := items[res.Index]
		write := classifyWrite{
			input:              item.input,
			hasContent:         true,
			imagePath:          item.imagePath,
			pathClass:          item.pathClass,
			exported:           item.exported,
			resolutionErr:      item.resolutionErr,
			resolutionDuration: item.resolutionDuration,
			exportBytes:        item.exportBytes,
			modelAttempts:      res.Attempts,
			modelDuration:      res.Duration,
			rateLimitEvents:    res.RateLimitEvents,
			transientErrEvents: res.TransientEvents,
		}
		switch res.Outcome {
		case model.OutcomeOK:
			parsed, err := classifier.parseResult(res.Response.Text, item.input, item.meta)
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
			if item.resolutionErr != nil {
				write.hasContent = false
				write.outcome = downloadFailureOutcome(item.resolutionErr)
			} else {
				write.outcome = modelFailureOutcome(res.Err)
			}
		}
		return writeClassifyResult(ctx, db, classifier, write, now, result, logger)
	}

	stats, err := model.Run(ctx, classifier.client, len(items), prepare, commit, runLogger{logger})
	result.RateLimitAborted = stats.Aborted
	result.ModelConcurrencyPeak = stats.ConcurrencyPeak
	result.ModelConcurrencyFinal = stats.ConcurrencyEnd
	return err
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
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		switch write.outcome {
		case contentOutcomeFailedParse, contentOutcomeFailedModel:
			state, reason := contentOutcomeQueueStateReason(write)
			return updateClassificationQueue(ctx, tx, write.input.QueueID, state, reason, classifiedAt)
		}
		observations := classifyFromMetadata(write.input)
		written, err := writeMetadataClassification(ctx, tx, write.input, observations, classifiedAt, true)
		if err != nil {
			return err
		}
		metadataWritten = written
		switch write.outcome {
		case contentOutcomeClassified:
			if !write.hasContent || write.contentResult == nil {
				return updateClassificationQueue(ctx, tx, write.input.QueueID, "content_failed", "classified outcome missing model result", classifiedAt)
			}
			written, placeWritten, err = writeModelClassification(ctx, tx, write.input, classifier, *write.contentResult, classifiedAt, write.imagePath, write.pathClass)
			if err != nil {
				return err
			}
			contentWritten = written
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
	return errors.Is(err, errModelCardParse)
}

func contentOutcomeQueueStateReason(write classifyWrite) (string, string) {
	switch write.outcome {
	case contentOutcomeFailedParse:
		return "content_failed", "failed_parse: " + classifyFailureReason(write.contentErr)
	case contentOutcomeFailedModel:
		return "content_failed", "failed_model: " + classifyFailureReason(write.contentErr)
	case contentOutcomeRateLimited:
		return "metadata_classified", "requeued: model rate limited (429)"
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
	case contentOutcomeRateLimited:
		result.RateLimitRequeued++
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
