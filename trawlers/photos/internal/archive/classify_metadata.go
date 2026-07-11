package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type metadataObservation struct {
	ObservationType string
	Label           string
}

func classifyFromMetadata(input classifyInput) []metadataObservation {
	out := []metadataObservation{}
	add := func(kind, label string) {
		label = strings.TrimSpace(label)
		if label == "" {
			return
		}
		out = append(out, metadataObservation{ObservationType: kind, Label: label})
	}

	add("media_type", input.MediaType)
	if input.Width > 0 && input.Height > 0 {
		switch {
		case input.Width > input.Height:
			add("geometry", "landscape")
		case input.Height > input.Width:
			add("geometry", "portrait")
		default:
			add("geometry", "square")
		}
	}
	if strings.TrimSpace(input.BurstIdentifier) != "" {
		add("capture_mode", "burst_member")
	}
	for _, resource := range input.Resources {
		add("resource_type", resource.ResourceType)
	}

	keywords := input.keywordText()
	if strings.Contains(keywords, "screenshot") || strings.Contains(keywords, "screen shot") {
		add("document_signal", "screenshot_candidate")
	}
	if containsAny(keywords, "receipt", "invoice", "bill", "statement") {
		add("document_signal", "receipt_candidate")
	}
	if containsAny(keywords, "document", "passport", "ticket", "boarding pass", "menu") {
		add("document_signal", "document_candidate")
	}
	return dedupeMetadataObservations(out)
}

func writeMetadataClassification(ctx context.Context, tx *sql.Tx, input classifyInput, observations []metadataObservation, classifiedAt time.Time, clearExisting bool) (int, error) {
	if clearExisting {
		if err := clearMetadataObservations(ctx, tx, input.AssetID); err != nil {
			return 0, err
		}
	}

	written := 0
	for _, observation := range observations {
		observationID := stableID("metadata_observation", input.AssetID, metadataClassifierSource, observation.ObservationType, observation.Label)
		if _, err := tx.ExecContext(ctx, `
insert into metadata_observation(id, asset_id, observation_type, label, source, classifier_id, evidence_id)
values (?, ?, ?, ?, ?, ?, ?)
`, observationID, input.AssetID, observation.ObservationType, observation.Label, metadataClassifierSource, metadataClassifierModelID, ""); err != nil {
			return written, fmt.Errorf("write metadata observation: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values (?, ?, ?, ?)
`, observationID, input.AssetID, observation.Label, observation.Label); err != nil {
			return written, fmt.Errorf("write observation fts: %w", err)
		}
		written++
	}

	state := classifyQueueStateMetadataClassified
	reason := "local_metadata_observations"
	if !input.hasLocalContent() {
		reason = "local_metadata_observations_waiting_for_content"
	}
	eligibility, err := firstCardEligibilityForAsset(ctx, tx, input.AssetID)
	if err != nil {
		return written, err
	}
	if eligibility == firstCardProhibitedDeletedBeforeCard {
		state = classifyQueueStateFirstCardProhibited
		reason = "deleted_before_first_card"
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
	return clearMetadataObservationsForAssets(ctx, tx, []string{assetID})
}

func clearMetadataObservationsForInputs(ctx context.Context, tx *sql.Tx, inputs []classifyInput) error {
	assetIDs := make([]string, 0, len(inputs))
	seen := map[string]bool{}
	for _, input := range inputs {
		assetID := strings.TrimSpace(input.AssetID)
		if assetID == "" {
			return errors.New("asset id is required")
		}
		if seen[assetID] {
			continue
		}
		seen[assetID] = true
		assetIDs = append(assetIDs, assetID)
	}
	return clearMetadataObservationsForAssets(ctx, tx, assetIDs)
}

func clearMetadataObservationsForAssets(ctx context.Context, tx *sql.Tx, assetIDs []string) error {
	if len(assetIDs) == 0 {
		return nil
	}
	for start := 0; start < len(assetIDs); start += metadataClassificationBatchSize {
		end := start + metadataClassificationBatchSize
		if end > len(assetIDs) {
			end = len(assetIDs)
		}
		if err := clearMetadataObservationsForAssetBatch(ctx, tx, assetIDs[start:end]); err != nil {
			return err
		}
	}
	return nil
}

func clearMetadataObservationsForAssetBatch(ctx context.Context, tx *sql.Tx, assetIDs []string) error {
	placeholders := strings.TrimRight(strings.Repeat("?,", len(assetIDs)), ",")
	args := []any{metadataClassifierSource, metadataClassifierModelID}
	for _, assetID := range assetIDs {
		args = append(args, assetID)
	}
	if _, err := tx.ExecContext(ctx, `
delete from observation_fts
where id in (
    select id from metadata_observation
    where source = ? and classifier_id = ? and asset_id in (`+placeholders+`)
  )
`, args...); err != nil {
		return fmt.Errorf("clear metadata observation fts: %w", err)
	}
	args = []any{metadataClassifierSource, metadataClassifierModelID}
	for _, assetID := range assetIDs {
		args = append(args, assetID)
	}
	if _, err := tx.ExecContext(ctx, `
delete from metadata_observation
where source = ? and classifier_id = ? and asset_id in (`+placeholders+`)
`, args...); err != nil {
		return fmt.Errorf("clear metadata observations: %w", err)
	}
	return nil
}

func dedupeMetadataObservations(observations []metadataObservation) []metadataObservation {
	seen := map[string]bool{}
	out := make([]metadataObservation, 0, len(observations))
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
