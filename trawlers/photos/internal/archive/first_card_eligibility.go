package archive

import (
	"context"
	"database/sql"
	"fmt"
)

type firstCardEligibility string

const (
	firstCardEligible                    firstCardEligibility = "eligible"
	firstCardProhibitedDeletedBeforeCard firstCardEligibility = "prohibited_deleted_before_first_card"
)

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func firstCardEligibilityForAsset(ctx context.Context, db queryRower, assetID string) (firstCardEligibility, error) {
	var eligibility firstCardEligibility
	if err := db.QueryRowContext(ctx, `
select case
  when exists (
    select 1 from model_observation
    where asset_id = asset.id
      and observation_type = ?
  ) then ?
  when first_card_blocked_at is not null
    and first_card_blocked_snapshot_id is not null
  then ?
  else ?
end
from asset
where id = ?
`, modelObservationCardSummary, firstCardEligible, firstCardProhibitedDeletedBeforeCard, firstCardEligible, assetID).Scan(&eligibility); err != nil {
		return "", fmt.Errorf("read first card eligibility: %w", err)
	}
	return eligibility, nil
}

func migrateFirstCardEligibility(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
update asset
set first_card_blocked_at = coalesce(first_card_blocked_at, first_missing_at),
    first_card_blocked_snapshot_id = coalesce(first_card_blocked_snapshot_id, nullif(trim(source_state_snapshot_id), ''))
where source_state = ?
  and first_missing_at is not null
  and trim(source_state_snapshot_id) <> ''
  and (first_card_blocked_at is null or first_card_blocked_snapshot_id is null)
  and not exists (
    select 1 from model_observation
    where asset_id = asset.id
      and observation_type = ?
  )
`, sourceStateDeletedUpstream, modelObservationCardSummary); err != nil {
		return fmt.Errorf("migrate first card eligibility: %w", err)
	}
	return nil
}

func firstCardEligibilityMigrationRequired(ctx context.Context, db *sql.DB) (bool, error) {
	var required bool
	if err := db.QueryRowContext(ctx, `
select exists (
  select 1
  from asset
  where source_state = ?
    and first_missing_at is not null
    and trim(source_state_snapshot_id) <> ''
    and (first_card_blocked_at is null or first_card_blocked_snapshot_id is null)
    and not exists (
      select 1 from model_observation
      where asset_id = asset.id
        and observation_type = ?
    )
)
`, sourceStateDeletedUpstream, modelObservationCardSummary).Scan(&required); err != nil {
		return false, fmt.Errorf("inspect first card eligibility migration: %w", err)
	}
	return required, nil
}
