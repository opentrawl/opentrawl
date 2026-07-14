package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	metadataClassifierSource        = "archive_metadata"
	metadataClassifierModelID       = "photos.archive-metadata.v1"
	metadataClassifierInputVersion  = "asset-resource-album.v1"
	metadataClassificationBatchSize = 500
)

type ClassifyOptions struct {
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
	ContentStoppedUncertain        int    `json:"content_stopped_uncertain"`
	ContentFailedDownload          int    `json:"content_failed_download"`
	ContentNotInPhotoKit           int    `json:"content_not_in_photokit"`
	ContentNoContentAvailable      int    `json:"content_no_content_available"`
	ContentSkippedUnsupportedMedia int    `json:"content_skipped_unsupported_media"`
	CardInputNotReady              int    `json:"card_input_not_ready,omitempty"`
	ContentOutcomeTotal            int    `json:"content_outcome_total,omitempty"`
	ContentObservationsWritten     int    `json:"content_observations_written"`
	PlaceCacheHits                 int    `json:"place_cache_hits,omitempty"`
	PlaceBackfillHits              int    `json:"place_backfill_hits,omitempty"`
	PlaceProviderAttempts          int    `json:"place_provider_attempts,omitempty"`
	PlaceProviderFailures          int    `json:"place_provider_failures,omitempty"`
	PlaceObservationsWritten       int    `json:"place_observations_written,omitempty"`
	ContentClassificationFailures  int    `json:"content_classification_failures"`
	PhotoKitExports                int    `json:"photokit_exports"`
	OriginalResolutionFailures     int    `json:"original_resolution_failures"`
	OriginalResolutionMillis       int64  `json:"original_resolution_millis"`
	ModelCallAttempts              int    `json:"model_call_attempts"`
	ModelCallMillis                int64  `json:"model_call_millis"`
	ModelConcurrencyPeak           int    `json:"model_concurrency_peak,omitempty"`
	ModelConcurrencyFinal          int    `json:"model_concurrency_final,omitempty"`
	ModelRateLimitEvents           int    `json:"model_rate_limit_events"`
	RateLimitRequeued              int    `json:"rate_limit_requeued,omitempty"`
	RateLimitAborted               bool   `json:"rate_limit_aborted,omitempty"`
	ModelTransientErrorEvents      int    `json:"model_transient_error_events"`
	PhotoKitExportBytes            int64  `json:"photokit_export_bytes"`
}

func (result ClassifyResult) String() string {
	lines := []string{
		fmt.Sprintf("Processed: %d", result.Processed),
		fmt.Sprintf("Metadata classified: %d", result.MetadataClassified),
		fmt.Sprintf("Content classified: %d", result.ContentClassified),
	}
	if result.CardInputNotReady > 0 {
		lines = append(lines, fmt.Sprintf("Card input not ready: %d", result.CardInputNotReady))
	}
	if result.ContentClassificationFailures > 0 {
		lines = append(lines, fmt.Sprintf("Content classification failures: %d", result.ContentClassificationFailures))
	}
	return strings.Join(lines, "\n")
}

func Classify(ctx context.Context, paths Paths, opts ClassifyOptions) (ClassifyResult, error) {
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return ClassifyResult{}, err
	}
	defer func() { _ = db.Close() }()
	return ClassifyWithStore(ctx, db, paths, opts)
}

func ClassifyWithStore(ctx context.Context, db *store.Store, paths Paths, opts ClassifyOptions) (ClassifyResult, error) {
	startedAt := time.Now()
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	// The visible default of 100 bounds a bare `classify` so a stray run does
	// not spend the whole queue. An explicit positive limit is honored exactly.
	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}
	if err := prepareStore(ctx, db); err != nil {
		return ClassifyResult{}, err
	}

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
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
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
		if result.Processed == result.CardInputNotReady {
			result.ModelRunID = ""
		} else {
			err := db.WithTx(ctx, func(tx *sql.Tx) error {
				return writeModelRun(ctx, tx, result.ModelRunID, *classifier, len(inputs), result, now().UTC())
			})
			if err != nil {
				return ClassifyResult{}, err
			}
		}
	}
	return finishClassifyResult(startedAt, result), nil
}

func finishClassifyResult(startedAt time.Time, result ClassifyResult) ClassifyResult {
	result.ElapsedMillis = time.Since(startedAt).Milliseconds()
	return result
}
