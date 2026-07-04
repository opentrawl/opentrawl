package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func writeModelClassification(ctx context.Context, tx *sql.Tx, input classifyInput, classifier modelClassifier, result modelResult, classifiedAt time.Time, _, _ string) (int, int, error) {
	if err := clearModelObservations(ctx, tx, input.AssetID, classifier.modelID); err != nil {
		return 0, 0, err
	}

	placeWritten, err := writePlaceClassification(ctx, tx, input, result.VenuePlausibility)
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
		observationID := stableID("model_observation", input.AssetID, modelClassifierSource, classifier.modelID, classifier.promptVersion, observation.ObservationType, observation.ValueText)
		if _, err := tx.ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, observationID, input.AssetID, observation.ObservationType, observation.ValueText, valueJSON, observation.Confidence, modelClassifierSource, classifier.modelID, classifier.promptVersion, ""); err != nil {
			return written, placeWritten, fmt.Errorf("write model observation: %w", err)
		}
		if observation.ObservationType == modelObservationCardSummary {
			cardFTSID = observationID
		}
		cardTexts = append(cardTexts, observation.ValueText)
		written++
	}
	if cardFTSID == "" {
		return written, placeWritten, errors.New("photo card summary observation was not written")
	}
	// Raw card prose, not a deduped term list: bm25 needs real term
	// frequency to rank an asset that is about grilling above one that
	// mentions a grill once.
	if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, cardFTSID, input.AssetID, "", strings.Join(cardTexts, "\n")); err != nil {
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

func clearModelObservations(ctx context.Context, tx *sql.Tx, assetID, modelID string) error {
	if strings.TrimSpace(assetID) == "" {
		return errors.New("asset id is required")
	}
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where asset_id = ?
  and id in (
    select id from model_observation
    where asset_id = ? and source in (?, ?) and model_id = ?
  )
`, assetID, assetID, modelClassifierSource, "local_multimodal", modelID); err != nil {
		return fmt.Errorf("clear model observation fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
delete from model_observation
where asset_id = ? and source in (?, ?) and model_id = ?
`, assetID, modelClassifierSource, "local_multimodal", modelID); err != nil {
		return fmt.Errorf("clear model observations: %w", err)
	}
	if err := clearPlaceObservations(ctx, tx, assetID); err != nil {
		return err
	}
	return nil
}
