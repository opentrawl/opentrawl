package archive

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const (
	sourceStateCurrent         = "current"
	sourceStateDeletedUpstream = "deleted_upstream"
	queueStateSourceDeleted    = "source_deleted"
)

// SnapshotIncompleteError reports a persisted audit snapshot that cannot change source state.
type SnapshotIncompleteError struct {
	State string
}

func (e *SnapshotIncompleteError) Error() string {
	return fmt.Sprintf("Photos snapshot was %s; audit was recorded but source state was not changed", e.State)
}

func markAssetPresent(ctx context.Context, tx *sql.Tx, assetID, snapshotID string, completedAt time.Time) error {
	var previousState string
	if err := tx.QueryRowContext(ctx, `select source_state from asset where id = ?`, assetID).Scan(&previousState); err != nil {
		return fmt.Errorf("read asset source state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
update asset
set source_state = ?,
    first_missing_at = null,
    source_deleted_at = null,
    source_state_snapshot_id = case
      when source_state <> ? or trim(source_state_snapshot_id) = '' then ?
      else source_state_snapshot_id
    end
where id = ?
`, sourceStateCurrent, sourceStateCurrent, snapshotID, assetID); err != nil {
		return fmt.Errorf("mark asset current: %w", err)
	}
	if previousState != sourceStateDeletedUpstream {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
update classification_queue
set state = case
      when exists (
        select 1 from model_observation
        where asset_id = classification_queue.asset_id
          and superseded_at is null
      ) then ?
      else ?
    end,
    reason = case
      when exists (
        select 1 from model_observation
        where asset_id = classification_queue.asset_id
          and superseded_at is null
      ) then 'source_restored: existing card retained'
      else 'source_restored: ready for classification'
    end,
    updated_at = ?
where asset_id = ?
`, classifyQueueStateContentClassified, classifyQueueStatePending, completedAt.Format(time.RFC3339Nano), assetID); err != nil {
		return fmt.Errorf("restore asset classification eligibility: %w", err)
	}
	return nil
}

func markMissingAssetsDeleted(ctx context.Context, tx *sql.Tx, sourceID, snapshotID string, completedAt time.Time) (int, error) {
	missingAt := completedAt.Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
update asset
set source_state = ?,
    first_missing_at = case
      when source_state = ? then first_missing_at
      else ?
    end,
    source_deleted_at = case
      when source_state = ? then source_deleted_at
      else null
    end,
    source_state_snapshot_id = case
      when source_state = ? then source_state_snapshot_id
      else ?
    end
where source_library_id = ?
  and id in (
    select asset_id
    from crawl_seen_asset
    where source_library_id = ? and last_seen_snapshot_id <> ?
  )
`, sourceStateDeletedUpstream, sourceStateDeletedUpstream, missingAt, sourceStateDeletedUpstream, sourceStateDeletedUpstream, snapshotID, sourceID, sourceID, snapshotID)
	if err != nil {
		return 0, fmt.Errorf("mark missing assets deleted upstream: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
update classification_queue
set state = ?, reason = 'source_deleted_upstream', updated_at = ?
where source_library_id = ?
  and asset_id in (
    select asset_id
    from crawl_seen_asset
    where source_library_id = ? and last_seen_snapshot_id <> ?
  )
`, queueStateSourceDeleted, missingAt, sourceID, sourceID, snapshotID); err != nil {
		return 0, fmt.Errorf("remove deleted assets from classification queue: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted upstream assets: %w", err)
	}
	return int(count), nil
}
