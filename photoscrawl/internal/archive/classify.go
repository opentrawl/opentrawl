package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

)

const (
	metadataClassifierSource        = "archive_metadata"
	metadataClassifierModelID       = "photoscrawl.archive-metadata.v1"
	metadataClassifierInputVersion  = "asset-resource-album.v1"
	metadataClassificationBatchSize = 500
)

type ClassifyOptions struct {
	All         bool
	Limit       int
	Model       string
	ModelURL    string
	ModelKeyEnv string
	LogSink     ClassifyLogSink
	Now         func() time.Time
}

type ClassifyLogSink interface {
	Info(event, message string) error
	Warn(event, message string) error
}

type ClassifyResult struct {
	Database                       string `json:"database"`
	Classifier                     string `json:"classifier"`
	ModelID                        string `json:"model_id"`
	InputVersion                   string `json:"input_version"`
	Limit                          int    `json:"limit"`
	ElapsedMillis                  int64  `json:"elapsed_millis"`
	Processed                      int    `json:"processed"`
	MetadataClassified             int    `json:"metadata_classified"`
	WaitingForLocalContent         int    `json:"waiting_for_local_content"`
	MetadataObservationsWritten    int    `json:"metadata_observations_written"`
	Model                          string `json:"model,omitempty"`
	ModelRunID                     string `json:"model_run_id,omitempty"`
	ContentClassified              int    `json:"content_classified"`
	ContentFailedParse             int    `json:"content_failed_parse"`
	ContentFailedModel             int    `json:"content_failed_model"`
	ContentFailedDownload          int    `json:"content_failed_download"`
	ContentNotInPhotoKit           int    `json:"content_not_in_photokit"`
	ContentNoContentAvailable      int    `json:"content_no_content_available"`
	ContentSkippedUnsupportedMedia int    `json:"content_skipped_unsupported_media"`
	ContentOutcomeTotal            int    `json:"content_outcome_total,omitempty"`
	ContentObservationsWritten     int    `json:"content_observations_written"`
	PlaceCacheHits                 int    `json:"place_cache_hits,omitempty"`
	PlaceBackfillHits              int    `json:"place_backfill_hits,omitempty"`
	PlaceProviderAttempts          int    `json:"place_provider_attempts,omitempty"`
	PlaceProviderFailures          int    `json:"place_provider_failures,omitempty"`
	PlaceObservationsWritten       int    `json:"place_observations_written,omitempty"`
	ContentClassificationFailures  int    `json:"content_classification_failures"`
	OriginalsDownloaded            int    `json:"originals_downloaded"`
	OriginalDownloadFailures       int    `json:"original_download_failures"`
	OriginalDownloadMillis         int64  `json:"original_download_millis"`
	ModelCallAttempts              int    `json:"model_call_attempts"`
	ModelCallMillis                int64  `json:"model_call_millis"`
	ModelConcurrencyPeak           int    `json:"model_concurrency_peak,omitempty"`
	ModelConcurrencyFinal          int    `json:"model_concurrency_final,omitempty"`
	ModelRateLimitEvents           int    `json:"model_rate_limit_events"`
	RateLimitRequeued              int    `json:"rate_limit_requeued,omitempty"`
	RateLimitAborted               bool   `json:"rate_limit_aborted,omitempty"`
	ModelTransientErrorEvents      int    `json:"model_transient_error_events"`
	BytesDownloaded                int64  `json:"bytes_downloaded"`
}

func Classify(ctx context.Context, paths Paths, opts ClassifyOptions) (ClassifyResult, error) {
	startedAt := time.Now()
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	// The --limit contract (crawlkit/flags): --all classifies every queued
	// asset, an explicit --limit is honored exactly (no hidden ceiling), and
	// the visible default of 100 bounds a bare `classify` so a stray run does
	// not spend the whole queue.
	limit := opts.Limit
	if opts.All {
		limit = 0
	} else if limit <= 0 {
		limit = 100
	}

	db, err := openArchive(ctx, paths.Database)
	if err != nil {
		return ClassifyResult{}, err
	}
	defer func() { _ = db.Close() }()

	result := ClassifyResult{
		Database:     paths.Database,
		Classifier:   metadataClassifierSource,
		ModelID:      metadataClassifierModelID,
		InputVersion: metadataClassifierInputVersion,
		Limit:        limit,
	}
	modelID := strings.TrimSpace(opts.Model)
	var classifier *modelClassifier
	if modelID != "" {
		modelClassifier, err := newModelClassifier(modelID, opts.ModelURL, opts.ModelKeyEnv)
		if err != nil {
			return ClassifyResult{}, err
		}
		classifier = &modelClassifier
		result.Classifier = metadataClassifierSource + "+" + modelClassifierSource
		result.ModelID = modelID
		result.Model = modelID
		result.ModelRunID = stableID("model_run", modelClassifierSource, modelID, modelPromptVersion, now().UTC().Format(time.RFC3339Nano))
	}

	logger := classifyLogger{sink: opts.LogSink}
	if err := ensureSearchIndex(ctx, db, logger); err != nil {
		return ClassifyResult{}, err
	}
	var inputs []classifyInput
	loadStartedAt := time.Now()
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		refreshModelID := ""
		if classifier != nil {
			refreshModelID = classifier.modelID
		}
		inputs, err = loadClassifyInputs(ctx, tx, limit, refreshModelID)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ClassifyResult{}, err
	}
	logger.logPhase("inputs_loaded", time.Since(loadStartedAt), logIntField("rows", len(inputs)))
	if classifier != nil {
		placeStartedAt := time.Now()
		selected := len(inputs)
		inputs, err = prepareClassifyPlaces(ctx, db, paths, inputs, now, &result, logger)
		if err != nil {
			return ClassifyResult{}, err
		}
		logger.logPhase("place_phase", time.Since(placeStartedAt),
			logIntField("selected", selected),
			logIntField("ready", len(inputs)),
			logIntField("cache_hits", result.PlaceCacheHits),
			logIntField("provider_attempts", result.PlaceProviderAttempts),
		)
	}
	if classifier == nil {
		for start := 0; start < len(inputs); start += metadataClassificationBatchSize {
			end := start + metadataClassificationBatchSize
			if end > len(inputs) {
				end = len(inputs)
			}
			batch := inputs[start:end]
			err := db.WithTx(ctx, func(tx *sql.Tx) error {
				if err := clearMetadataObservationsForInputs(ctx, tx, batch); err != nil {
					return err
				}
				for _, input := range batch {
					observations := classifyFromMetadata(input)
					written, err := writeMetadataClassification(ctx, tx, input, observations, now().UTC(), false)
					if err != nil {
						return err
					}
					result.Processed++
					result.MetadataClassified++
					result.MetadataObservationsWritten += written
					if !input.hasLocalContent() {
						result.WaitingForLocalContent++
					}
				}
				return nil
			})
			if err != nil {
				return ClassifyResult{}, err
			}
		}
		return finishClassifyResult(startedAt, result), nil
	}
	if err := classifyContentInputs(ctx, db, paths, inputs, *classifier, now, &result, logger); err != nil {
		return ClassifyResult{}, err
	}
	if classifier != nil {
		if result.ContentOutcomeTotal != result.Processed {
			return ClassifyResult{}, fmt.Errorf("content outcome accounting mismatch: processed %d, outcomes %d", result.Processed, result.ContentOutcomeTotal)
		}
		err := db.WithTx(ctx, func(tx *sql.Tx) error {
			return writeModelRun(ctx, tx, result.ModelRunID, *classifier, len(inputs), result, now().UTC())
		})
		if err != nil {
			return ClassifyResult{}, err
		}
	}
	return finishClassifyResult(startedAt, result), nil
}

func finishClassifyResult(startedAt time.Time, result ClassifyResult) ClassifyResult {
	result.ElapsedMillis = time.Since(startedAt).Milliseconds()
	return result
}
