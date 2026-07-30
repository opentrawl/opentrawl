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
