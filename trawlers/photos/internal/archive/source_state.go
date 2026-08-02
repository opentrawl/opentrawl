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
)

// SnapshotIncompleteError reports a persisted audit snapshot that cannot change source state.
type SnapshotIncompleteError struct {
	State string
}

func (e *SnapshotIncompleteError) Error() string {
	return fmt.Sprintf("Photos snapshot was %s; audit was recorded but source state was not changed", e.State)
}

func markAssetPresent(ctx context.Context, tx *sql.Tx, assetID, snapshotID string) error {
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
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count deleted upstream assets: %w", err)
	}
	return int(count), nil
}
