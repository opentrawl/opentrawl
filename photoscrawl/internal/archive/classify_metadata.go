package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type visualObservation struct {
	ObservationType string
	Label           string
	Confidence      float64
}

func classifyFromMetadata(input classifyInput) []visualObservation {
	out := []visualObservation{}
	add := func(kind, label string, confidence float64) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		out = append(out, visualObservation{ObservationType: kind, Label: label, Confidence: confidence})
	}

	add("media_type", input.MediaType, 1)
	add("content_availability", pickLabel(input.hasLocalContent(), "local_content_available", "local_content_unavailable"), 1)
	if input.Width > 0 && input.Height > 0 {
		switch {
		case input.Width > input.Height:
			add("geometry", "landscape", 1)
		case input.Height > input.Width:
			add("geometry", "portrait", 1)
		default:
			add("geometry", "square", 1)
		}
	}
	if strings.TrimSpace(input.BurstIdentifier) != "" {
		add("capture_mode", "burst_member", 1)
	}
	for _, resource := range input.Resources {
		add("resource_type", resource.ResourceType, 1)
		add("resource_uti", resource.UTI, 1)
	}

	keywords := input.keywordText()
	if strings.Contains(keywords, "screenshot") || strings.Contains(keywords, "screen shot") {
		add("document_signal", "screenshot_candidate", 0.75)
	}
	if containsAny(keywords, "receipt", "invoice", "bill", "statement") {
		add("document_signal", "receipt_candidate", 0.65)
	}
	if containsAny(keywords, "document", "passport", "ticket", "boarding pass", "menu") {
		add("document_signal", "document_candidate", 0.6)
	}
	return dedupeVisualObservations(out)
}

func writeMetadataClassification(ctx context.Context, tx *sql.Tx, input classifyInput, observations []visualObservation, classifiedAt time.Time) (int, error) {
	if err := clearMetadataObservations(ctx, tx, input.AssetID); err != nil {
		return 0, err
	}
	evidenceID := stableID("evidence", input.AssetID, "classification_input", metadataClassifierSource, metadataClassifierInputVersion)
	evidenceJSON, err := jsonText(map[string]any{
		"classifier":         metadataClassifierSource,
		"model_id":           metadataClassifierModelID,
		"input_version":      metadataClassifierInputVersion,
		"media_type":         input.MediaType,
		"media_subtypes":     input.MediaSubtypes,
		"creation_date":      input.CreationDate,
		"favorite":           input.Favorite,
		"hidden":             input.Hidden,
		"resource_count":     len(input.Resources),
		"album_count":        len(input.Albums),
		"width":              input.Width,
		"height":             input.Height,
		"has_local_content":  input.hasLocalContent(),
		"needs_download":     input.NeedsDownload,
		"classified_at":      classifiedAt.Format(time.RFC3339Nano),
		"metadata_only":      true,
		"content_not_opened": true,
	})
	if err != nil {
		return 0, err
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
`, evidenceID, input.AssetID, "classification_input", metadataClassifierSource, input.AssetID+"/classification/archive_metadata", evidenceJSON); err != nil {
		return 0, fmt.Errorf("write classification evidence: %w", err)
	}

	written := 0
	for _, observation := range observations {
		observationID := stableID("visual_observation", input.AssetID, metadataClassifierSource, observation.ObservationType, observation.Label)
		if _, err := tx.ExecContext(ctx, `
insert into visual_observation(id, asset_id, observation_type, label, confidence, bounding_box_json, source, model_id, evidence_id)
values (?, ?, ?, ?, ?, '{}', ?, ?, ?)
`, observationID, input.AssetID, observation.ObservationType, observation.Label, observation.Confidence, metadataClassifierSource, metadataClassifierModelID, evidenceID); err != nil {
			return written, fmt.Errorf("write visual observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, input.AssetID, observation.Label, strings.Join(nonEmpty(observation.ObservationType, observation.Label, metadataClassifierSource), " ")); err != nil {
			return written, fmt.Errorf("write observation fts: %w", err)
		}
		written++
	}

	state := "metadata_classified"
	reason := "local_metadata_observations"
	if !input.hasLocalContent() {
		reason = "local_metadata_observations_waiting_for_content"
	}
	if _, err := tx.ExecContext(ctx, `
update classification_queue
set state = ?, reason = ?, updated_at = ?
where id = ?
`, state, reason, classifiedAt.Format(time.RFC3339Nano), input.QueueID); err != nil {
		return written, fmt.Errorf("update classification queue: %w", err)
	}
	return written, nil
}

func updateClassificationQueue(ctx context.Context, tx *sql.Tx, queueID, state, reason string, updatedAt time.Time) error {
	if _, err := tx.ExecContext(ctx, `
update classification_queue
set state = ?, reason = ?, updated_at = ?
where id = ?
`, state, reason, updatedAt.Format(time.RFC3339Nano), queueID); err != nil {
		return fmt.Errorf("update classification queue: %w", err)
	}
	return nil
}

func clearMetadataObservations(ctx context.Context, tx *sql.Tx, assetID string) error {
	if strings.TrimSpace(assetID) == "" {
		return errors.New("asset id is required")
	}
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where asset_id = ?
  and id in (
    select id from visual_observation
    where asset_id = ? and source = ? and model_id = ?
  )
`, assetID, assetID, metadataClassifierSource, metadataClassifierModelID); err != nil {
		return fmt.Errorf("clear metadata observation fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
delete from visual_observation
where asset_id = ? and source = ? and model_id = ?
`, assetID, metadataClassifierSource, metadataClassifierModelID); err != nil {
		return fmt.Errorf("clear metadata visual observations: %w", err)
	}
	return nil
}

func dedupeVisualObservations(observations []visualObservation) []visualObservation {
	seen := map[string]bool{}
	out := make([]visualObservation, 0, len(observations))
	for _, observation := range observations {
		key := observation.ObservationType + "\x00" + observation.Label
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, observation)
	}
	return out
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func pickLabel(condition bool, ifTrue, ifFalse string) string {
	if condition {
		return ifTrue
	}
	return ifFalse
}
