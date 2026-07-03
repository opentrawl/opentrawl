package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func writeModelClassification(ctx context.Context, tx *sql.Tx, input classifyInput, classifier modelClassifier, result modelResult, classifiedAt time.Time, imagePath, imagePathClass string) (int, int, error) {
	if err := clearModelObservations(ctx, tx, input.AssetID, classifier.modelID); err != nil {
		return 0, 0, err
	}
	if strings.TrimSpace(imagePathClass) == "" {
		imagePathClass = input.localPathClass(imagePath)
	}
	evidenceID := stableID("evidence", input.AssetID, "content_classification", modelClassifierSource, classifier.modelID, classifier.promptVersion)
	evidenceJSON, err := jsonText(map[string]any{
		"classifier":        modelClassifierSource,
		"model_id":          classifier.modelID,
		"prompt_version":    classifier.promptVersion,
		"image_bytes":       result.ImageBytes,
		"image_sha256":      result.ImageSHA256,
		"image_extension":   strings.ToLower(filepath.Ext(imagePath)),
		"image_path_class":  imagePathClass,
		"classified_at":     classifiedAt.Format(time.RFC3339Nano),
		"raw_response":      result.RawResponse,
		"parsed_response":   result.Payload,
		"local_only":        !classifier.remote(),
		"cloud_transmitted": classifier.remote(),
	})
	if err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx, `
insert into evidence_ref(id, asset_id, evidence_kind, source, pointer, value_json)
values (?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  asset_id = excluded.asset_id,
  evidence_kind = excluded.evidence_kind,
  source = excluded.source,
  pointer = excluded.pointer,
  value_json = excluded.value_json
`, evidenceID, input.AssetID, "content_classification", modelClassifierSource, input.AssetID+"/classification/photo_card", evidenceJSON); err != nil {
		return 0, 0, fmt.Errorf("write model evidence: %w", err)
	}

	placeWritten, err := writePlaceClassification(ctx, tx, input, result.VenuePlausibility, classifiedAt)
	if err != nil {
		return 0, placeWritten, err
	}
	written := 0
	cardFTSID := ""
	for _, observation := range result.Observations {
		valueJSON, err := jsonText(observation.Value)
		if err != nil {
			return written, placeWritten, err
		}
		observationID := stableID("model_observation", input.AssetID, modelClassifierSource, classifier.modelID, classifier.promptVersion, observation.ObservationType, observation.ValueText)
		if _, err := tx.ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, observationID, input.AssetID, observation.ObservationType, observation.ValueText, valueJSON, observation.Confidence, modelClassifierSource, classifier.modelID, classifier.promptVersion, evidenceID); err != nil {
			return written, placeWritten, fmt.Errorf("write model observation: %w", err)
		}
		if observation.ObservationType == modelObservationCardSummary {
			cardFTSID = observationID
		}
		written++
	}
	if cardFTSID == "" {
		return written, placeWritten, errors.New("photo card summary observation was not written")
	}
	termBody := strings.Join(result.SearchTerms, " ")
	if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, cardFTSID, input.AssetID, "", termBody); err != nil {
		return written, placeWritten, fmt.Errorf("write model card fts: %w", err)
	}
	for _, term := range result.SearchTerms {
		termID := stableID("observation_term", input.AssetID, cardFTSID, term)
		if _, err := tx.ExecContext(ctx, `
insert into observation_term(id, asset_id, observation_id, term, term_type, source, model_id)
values (?, ?, ?, ?, ?, ?, ?)
`, termID, input.AssetID, cardFTSID, term, "photo_card", modelClassifierSource, classifier.modelID); err != nil {
			return written, placeWritten, fmt.Errorf("write observation term: %w", err)
		}
	}
	if err := updateClassificationQueue(ctx, tx, input.QueueID, "content_classified", "model_observations", classifiedAt); err != nil {
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
delete from observation_term
where asset_id = ?
  and observation_id in (
    select id from model_observation
    where asset_id = ? and source in (?, ?) and model_id = ?
  )
`, assetID, assetID, modelClassifierSource, "local_multimodal", modelID); err != nil {
		return fmt.Errorf("clear observation terms: %w", err)
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
	if _, err := tx.ExecContext(ctx, `
delete from evidence_ref
where asset_id = ?
  and evidence_kind = 'content_classification'
  and source in (?, ?)
`, assetID, modelClassifierSource, "local_multimodal"); err != nil {
		return fmt.Errorf("clear model evidence: %w", err)
	}
	return nil
}
