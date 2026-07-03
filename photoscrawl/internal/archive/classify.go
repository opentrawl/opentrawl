package archive

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
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
	Now         func() time.Time
}

type ClassifyResult struct {
	Database                      string `json:"database"`
	Classifier                    string `json:"classifier"`
	ModelID                       string `json:"model_id"`
	InputVersion                  string `json:"input_version"`
	Limit                         int    `json:"limit"`
	Processed                     int    `json:"processed"`
	MetadataClassified            int    `json:"metadata_classified"`
	WaitingForLocalContent        int    `json:"waiting_for_local_content"`
	MetadataObservationsWritten   int    `json:"metadata_observations_written"`
	Model                         string `json:"model,omitempty"`
	ModelRunID                    string `json:"model_run_id,omitempty"`
	ContentClassified             int    `json:"content_classified"`
	ContentObservationsWritten    int    `json:"content_observations_written"`
	ContentClassificationFailures int    `json:"content_classification_failures"`
}

func Classify(ctx context.Context, paths Paths, opts ClassifyOptions) (ClassifyResult, error) {
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	limit := opts.Limit
	if opts.All {
		limit = 0
	} else if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return ClassifyResult{}, err
	}
	defer db.Close()

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
		modelClassifier := newModelClassifier(modelID, opts.ModelURL, opts.ModelKeyEnv)
		classifier = &modelClassifier
		result.Classifier = metadataClassifierSource + "+" + modelClassifierSource
		result.ModelID = modelID
		result.Model = modelID
		result.ModelRunID = stableID("model_run", modelClassifierSource, modelID, modelPromptVersion, now().UTC().Format(time.RFC3339Nano))
	}

	var inputs []classifyInput
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inputs, err = loadClassifyInputs(ctx, tx, limit, classifier != nil)
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ClassifyResult{}, err
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
		return result, nil
	}
	for _, input := range inputs {
		var contentResult *modelResult
		var contentErr error
		imagePath, hasImage := input.contentImagePath()
		if classifier != nil && hasImage {
			modelResult, err := classifier.classify(ctx, imagePath)
			if err != nil {
				contentErr = err
			} else {
				contentResult = &modelResult
			}
		}

		err := db.WithTx(ctx, func(tx *sql.Tx) error {
			observations := classifyFromMetadata(input)
			written, err := writeMetadataClassification(ctx, tx, input, observations, now().UTC(), true)
			if err != nil {
				return err
			}
			result.Processed++
			result.MetadataClassified++
			result.MetadataObservationsWritten += written
			if !input.hasLocalContent() {
				result.WaitingForLocalContent++
			}
			if classifier == nil || !hasImage {
				return nil
			}
			if contentErr != nil {
				result.ContentClassificationFailures++
				return updateClassificationQueue(ctx, tx, input.QueueID, "content_failed", truncateReason(contentErr.Error()), now().UTC())
			}
			contentWritten, err := writeModelClassification(ctx, tx, input, *classifier, *contentResult, now().UTC())
			if err != nil {
				return err
			}
			result.ContentClassified++
			result.ContentObservationsWritten += contentWritten
			return nil
		})
		if err != nil {
			return ClassifyResult{}, err
		}
	}
	if classifier != nil {
		err := db.WithTx(ctx, func(tx *sql.Tx) error {
			return writeModelRun(ctx, tx, result.ModelRunID, *classifier, len(inputs), result.ContentClassified, result.ContentClassificationFailures, now().UTC())
		})
		if err != nil {
			return ClassifyResult{}, err
		}
	}
	return result, nil
}
