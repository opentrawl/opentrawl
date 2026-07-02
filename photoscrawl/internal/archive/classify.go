package archive

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
)

const (
	metadataClassifierSource       = "archive_metadata"
	metadataClassifierModelID      = "photoscrawl.archive-metadata.v1"
	metadataClassifierInputVersion = "asset-resource-album.v1"
)

type ClassifyOptions struct {
	All              bool
	Limit            int
	LocalModel       string
	LocalModelURL    string
	LocalModelKeyEnv string
	Now              func() time.Time
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
	VisualObservationsWritten     int    `json:"visual_observations_written"`
	LocalModel                    string `json:"local_model,omitempty"`
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
	localModel := strings.TrimSpace(opts.LocalModel)
	var classifier *localModelClassifier
	if localModel != "" {
		localClassifier := newLocalModelClassifier(localModel, opts.LocalModelURL, opts.LocalModelKeyEnv)
		classifier = &localClassifier
		result.Classifier = metadataClassifierSource + "+" + localModelClassifierSource
		result.ModelID = localModel
		result.LocalModel = localModel
		result.ModelRunID = stableID("model_run", localModelClassifierSource, localModel, localModelPromptVersion, now().UTC().Format(time.RFC3339Nano))
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
	for _, input := range inputs {
		var contentResult *localModelResult
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
			written, err := writeMetadataClassification(ctx, tx, input, observations, now().UTC())
			if err != nil {
				return err
			}
			result.Processed++
			result.MetadataClassified++
			result.VisualObservationsWritten += written
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
			contentWritten, err := writeLocalModelClassification(ctx, tx, input, *classifier, *contentResult, now().UTC())
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
