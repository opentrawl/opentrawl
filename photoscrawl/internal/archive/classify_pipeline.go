package archive

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
)

const (
	modelConcurrencyStart = 2
	modelConcurrencyMax   = 8
)

type modelJob struct {
	input      classifyInput
	imagePath  string
	pathClass  string
	lease      *originalLease
	downloaded originalExportResult
}

type classifyWrite struct {
	input              classifyInput
	hasContent         bool
	contentResult      *modelResult
	contentErr         error
	imagePath          string
	pathClass          string
	lease              *originalLease
	downloaded         bool
	downloadErr        error
	downloadDuration   time.Duration
	downloadBytes      int64
	cacheHighWater     int64
	modelAttempts      int
	modelDuration      time.Duration
	rateLimitEvents    int
	transientErrEvents int
	outcome            contentOutcome
}

type contentOutcome string

const (
	contentOutcomeClassified              contentOutcome = "classified"
	contentOutcomeFailedParse             contentOutcome = "failed_parse"
	contentOutcomeFailedModel             contentOutcome = "failed_model"
	contentOutcomeFailedDownload          contentOutcome = "failed_download"
	contentOutcomeNotInPhotoKit           contentOutcome = "not_in_photokit"
	contentOutcomeNoContentAvailable      contentOutcome = "no_content_available"
	contentOutcomeSkippedUnsupportedMedia contentOutcome = "skipped_unsupported_media"
)

func classifyContentInputs(ctx context.Context, db *store.Store, paths Paths, inputs []classifyInput, classifier modelClassifier, now func() time.Time, result *ClassifyResult) error {
	cache, err := newOriginalsCache(paths.OriginalsCacheDir(), result.ModelRunID)
	if err != nil {
		return err
	}
	defer cache.Close()
	result.CacheMaxBytes = cache.MaxBytes()
	result.ModelConcurrencyStart = modelConcurrencyStart

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	modelJobs := make(chan modelJob, len(inputs))
	downloads := make(chan classifyInput)
	writes := make(chan classifyWrite, len(inputs))
	writerDone := make(chan error, 1)
	go func() {
		writerDone <- writeClassifyResults(ctx, db, classifier, writes, now, result)
	}()

	limiter := newAdaptiveLimiter(modelConcurrencyStart, modelConcurrencyMax)
	var modelWG sync.WaitGroup
	modelsDone := make(chan struct{})
	go func() {
		defer close(modelsDone)
		for job := range modelJobs {
			modelWG.Add(1)
			go func(job modelJob) {
				defer modelWG.Done()
				runModelJob(ctx, limiter, classifier, job, writes)
			}(job)
		}
		modelWG.Wait()
		close(writes)
	}()

	var downloadWG sync.WaitGroup
	for i := 0; i < downloadConcurrency; i++ {
		downloadWG.Add(1)
		go func() {
			defer downloadWG.Done()
			for input := range downloads {
				exported := cache.export(ctx, input)
				if exported.err != nil {
					sendClassifyWrite(ctx, writes, classifyWrite{
						input:            input,
						contentErr:       exported.err,
						downloadErr:      exported.err,
						downloadDuration: exported.duration,
						outcome:          downloadFailureOutcome(exported.err),
					})
					continue
				}
				sendModelJob(ctx, modelJobs, modelJob{
					input:      input,
					imagePath:  exported.path,
					pathClass:  exported.pathClass,
					lease:      exported.lease,
					downloaded: exported,
				})
			}
		}()
	}

	for _, input := range inputs {
		if ctx.Err() != nil {
			break
		}
		if imagePath, ok := input.contentImagePath(); ok {
			sendModelJob(ctx, modelJobs, modelJob{
				input:     input,
				imagePath: imagePath,
				pathClass: input.localPathClass(imagePath),
			})
			continue
		}
		if input.NeedsDownload && input.MediaType == "image" {
			select {
			case downloads <- input:
			case <-ctx.Done():
			}
			continue
		}
		sendClassifyWrite(ctx, writes, classifyWrite{
			input:   input,
			outcome: missingContentOutcome(input),
		})
	}
	close(downloads)
	downloadWG.Wait()
	close(modelJobs)
	<-modelsDone

	if err := <-writerDone; err != nil {
		cancel()
		return err
	}
	result.ModelConcurrencyPeak = limiter.Peak()
	result.ModelConcurrencyFinal = limiter.Current()
	if highWater := cache.HighWater(); highWater > result.CacheHighWaterBytes {
		result.CacheHighWaterBytes = highWater
	}
	return nil
}

func runModelJob(ctx context.Context, limiter *adaptiveLimiter, classifier modelClassifier, job modelJob, writes chan<- classifyWrite) {
	write := classifyWrite{
		input:            job.input,
		hasContent:       true,
		imagePath:        job.imagePath,
		pathClass:        job.pathClass,
		lease:            job.lease,
		downloaded:       job.downloaded.err == nil && job.downloaded.path != "",
		downloadDuration: job.downloaded.duration,
		downloadBytes:    job.downloaded.bytes,
		cacheHighWater:   job.downloaded.cacheHighMark,
	}
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if err := ctx.Err(); err != nil {
			lastErr = err
			break
		}
		limiter.Acquire()
		startedAt := time.Now()
		modelResult, err := classifier.classify(ctx, job.input, job.imagePath)
		limiter.Release()
		write.modelAttempts++
		write.modelDuration += time.Since(startedAt)
		if err == nil {
			limiter.RecordSuccess()
			write.contentResult = &modelResult
			lastErr = nil
			break
		}
		lastErr = err
		retry := retryableModelError(err)
		if retry.rateLimited {
			write.rateLimitEvents++
			limiter.RecordThrottle()
		} else if retry.transient {
			write.transientErrEvents++
			limiter.RecordThrottle()
		}
		if !retry.retry || attempt == 2 {
			break
		}
	}
	if lastErr != nil {
		write.contentErr = lastErr
		write.outcome = modelFailureOutcome(lastErr)
	} else {
		write.outcome = contentOutcomeClassified
	}
	sendClassifyWrite(ctx, writes, write)
}

func writeClassifyResults(ctx context.Context, db *store.Store, classifier modelClassifier, writes <-chan classifyWrite, now func() time.Time, result *ClassifyResult) error {
	for write := range writes {
		err := func() error {
			if write.lease != nil {
				defer write.lease.Close()
			}
			return writeClassifyResult(ctx, db, classifier, write, now, result)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func writeClassifyResult(ctx context.Context, db *store.Store, classifier modelClassifier, write classifyWrite, now func() time.Time, result *ClassifyResult) error {
	var metadataWritten, contentWritten int
	classifiedAt := now().UTC()
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		switch write.outcome {
		case contentOutcomeFailedParse, contentOutcomeFailedModel:
			if err := clearModelObservations(ctx, tx, write.input.AssetID, classifier.modelID); err != nil {
				return err
			}
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
			written, err = writeModelClassification(ctx, tx, write.input, classifier, *write.contentResult, classifiedAt, write.imagePath, write.pathClass)
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

	result.Processed++
	result.MetadataClassified++
	result.MetadataObservationsWritten += metadataWritten
	result.addContentOutcome(write.outcome)
	if write.outcome == contentOutcomeClassified {
		result.ContentObservationsWritten += contentWritten
	}
	if write.downloadErr != nil {
		result.OriginalDownloadFailures++
	}
	if write.downloaded {
		result.OriginalsDownloaded++
		result.BytesDownloaded += write.downloadBytes
	}
	result.OriginalDownloadMillis += write.downloadDuration.Milliseconds()
	result.ModelCallAttempts += write.modelAttempts
	result.ModelCallMillis += write.modelDuration.Milliseconds()
	result.ModelRateLimitEvents += write.rateLimitEvents
	result.ModelTransientErrorEvents += write.transientErrEvents
	if write.cacheHighWater > result.CacheHighWaterBytes {
		result.CacheHighWaterBytes = write.cacheHighWater
	}
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
	if err == nil {
		return false
	}
	reason := err.Error()
	return strings.Contains(reason, "parse model JSON") ||
		strings.Contains(reason, "model did not return a JSON object") ||
		strings.Contains(reason, "model card")
}

func contentOutcomeQueueStateReason(write classifyWrite) (string, string) {
	switch write.outcome {
	case contentOutcomeFailedParse:
		return "content_failed", "failed_parse: " + classifyFailureReason(write.contentErr)
	case contentOutcomeFailedModel:
		return "content_failed", "failed_model: " + classifyFailureReason(write.contentErr)
	case contentOutcomeFailedDownload:
		return "content_failed", "failed_download: " + classifyFailureReason(write.downloadErr)
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

func sendModelJob(ctx context.Context, jobs chan<- modelJob, job modelJob) {
	select {
	case jobs <- job:
	case <-ctx.Done():
		if job.lease != nil {
			job.lease.Close()
		}
	}
}

func sendClassifyWrite(ctx context.Context, writes chan<- classifyWrite, write classifyWrite) {
	select {
	case writes <- write:
	case <-ctx.Done():
		if write.lease != nil {
			write.lease.Close()
		}
	}
}
