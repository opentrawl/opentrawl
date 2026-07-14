package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func writeModelClassification(ctx context.Context, tx *sql.Tx, input classifyInput, classifier modelClassifier, result modelResult, prepared preparedCardRequest, classifiedAt time.Time, generationID string) (int, int, error) {
	if strings.TrimSpace(generationID) == "" {
		return 0, 0, errors.New("model generation id is required")
	}
	if err := supersedeModelObservations(ctx, tx, input.AssetID, generationID, classifiedAt); err != nil {
		return 0, 0, err
	}

	placeWritten, err := writeModelPlaceClassificationAt(ctx, tx, input, result.VenuePlausibility, prepared, generationID, classifiedAt)
	if err != nil {
		return 0, placeWritten, err
	}
	written := 0
	cardFTSID := ""
	cardTexts := []string{}
	for _, observation := range result.Observations {
		valueJSON, err := jsonText(observation.Value)
		if err != nil {
			return written, placeWritten, err
		}
		observationID := stableID("model_observation", input.AssetID, modelClassifierSource, classifier.modelID, classifier.promptVersion, generationID, observation.ObservationType, observation.ValueText)
		inserted, err := tx.ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, generation_id, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do nothing
		`, observationID, input.AssetID, observation.ObservationType, observation.ValueText, valueJSON, observation.Confidence, modelClassifierSource, classifier.modelID, classifier.promptVersion, generationID, "")
		if err != nil {
			return written, placeWritten, fmt.Errorf("write model observation: %w", err)
		}
		count, err := inserted.RowsAffected()
		if err != nil {
			return written, placeWritten, fmt.Errorf("read model observation insert count: %w", err)
		}
		if observation.ObservationType == modelObservationCardSummary {
			cardFTSID = observationID
		}
		cardTexts = append(cardTexts, observation.ValueText)
		written += int(count)
	}
	if cardFTSID == "" {
		return written, placeWritten, errors.New("photo card summary observation was not written")
	}
	if _, err := tx.ExecContext(ctx, `delete from observation_fts where id = ?`, cardFTSID); err != nil {
		return written, placeWritten, fmt.Errorf("clear existing model card fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `insert into observation_fts(id, asset_id, title, body) values (?, ?, '', ?)`, cardFTSID, input.AssetID, strings.Join(cardTexts, "\n")); err != nil {
		return written, placeWritten, fmt.Errorf("write model card fts: %w", err)
	}
	if err := updateClassificationQueue(ctx, tx, input.QueueID, classifyQueueStateContentClassified, "model_observations", classifiedAt); err != nil {
		return written, placeWritten, err
	}
	return written, placeWritten, nil
}

func writeModelRun(ctx context.Context, tx *sql.Tx, runID string, classifier modelClassifier, inputCount int, result ClassifyResult, completedAt time.Time) error {
	metadataJSON, err := jsonText(map[string]any{
		"content_classified":                result.ContentClassified,
		"content_failed_parse":              result.ContentFailedParse,
		"content_failed_model":              result.ContentFailedModel,
		"content_stopped_uncertain":         result.ContentStoppedUncertain,
		"content_failed_download":           result.ContentFailedDownload,
		"content_not_in_photokit":           result.ContentNotInPhotoKit,
		"content_no_content_available":      result.ContentNoContentAvailable,
		"content_skipped_unsupported_media": result.ContentSkippedUnsupportedMedia,
		"content_outcome_total":             result.ContentOutcomeTotal,
		"local_only":                        !classifier.remote(),
		"cloud_transmitted":                 classifier.remote(),
	})
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
insert into model_run(id, source, model_id, prompt_version, started_at, completed_at, input_count, metadata_json)
values (?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  completed_at = excluded.completed_at,
  input_count = excluded.input_count,
  metadata_json = excluded.metadata_json
`, runID, modelClassifierSource, classifier.modelID, classifier.promptVersion, completedAt.Format(time.RFC3339Nano), completedAt.Format(time.RFC3339Nano), inputCount, metadataJSON); err != nil {
		return fmt.Errorf("write model run: %w", err)
	}
	return nil
}

func supersedeModelObservations(ctx context.Context, tx *sql.Tx, assetID, generationID string, supersededAt time.Time) error {
	if strings.TrimSpace(assetID) == "" {
		return errors.New("asset id is required")
	}
	timestamp := supersededAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where asset_id = ?
  and id in (
    select id from model_observation
    where asset_id = ? and source in (?, ?) and superseded_at is null
      and coalesce(generation_id, '') <> ?
  )
`, assetID, assetID, modelClassifierSource, "local_multimodal", generationID); err != nil {
		return fmt.Errorf("clear superseded model observation fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
update model_observation
set superseded_at = ?
where asset_id = ? and source in (?, ?) and superseded_at is null
  and coalesce(generation_id, '') <> ?
`, timestamp, assetID, modelClassifierSource, "local_multimodal", generationID); err != nil {
		return fmt.Errorf("supersede model observations: %w", err)
	}
	return nil
}
