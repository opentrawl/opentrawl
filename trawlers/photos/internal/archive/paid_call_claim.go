package archive

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type paidCallClaimInput struct {
	StageID   string
	ItemID    string
	Prepared  preparedCardRequest
	ClaimedAt time.Time
}

type paidCallDecision struct {
	Purpose      paidCallPurpose
	GenerationID string
	Call         model.Call
	Send         bool
}

type paidCallClaimSQLStep string

const (
	paidCallClaimStageWrite      paidCallClaimSQLStep = "stage_write"
	paidCallClaimStageItemRead   paidCallClaimSQLStep = "stage_item_read"
	paidCallClaimSourceStateRead paidCallClaimSQLStep = "source_state_read"
	paidCallClaimEligibilityRead paidCallClaimSQLStep = "eligibility_read"
)

// paidCallClaimSQLHook and paidCallClaimAfterStageWrite are narrow seams for
// deterministic transaction-order tests. Production leaves both nil.
var (
	paidCallClaimSQLHook         func(paidCallClaimSQLStep) error
	paidCallClaimAfterStageWrite func() error
)

func claimPaidCall(ctx context.Context, db *store.Store, input paidCallClaimInput) (paidCallDecision, error) {
	if db == nil {
		return paidCallDecision{}, errors.New("archive store is not open")
	}
	if err := validatePaidCallClaimInput(input); err != nil {
		return paidCallDecision{}, err
	}

	var decision paidCallDecision
	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		if err := observePaidCallClaimSQL(paidCallClaimStageWrite); err != nil {
			return err
		}
		var cap int
		if err := tx.QueryRowContext(ctx, `
update paid_call_stage
set claim_serial = claim_serial + 1
where id = ?
returning purpose, approved_call_cap
`, input.StageID).Scan(&decision.Purpose, &cap); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("paid call stage does not exist")
			}
			return fmt.Errorf("lock paid call stage: %w", err)
		}
		if paidCallClaimAfterStageWrite != nil {
			if err := paidCallClaimAfterStageWrite(); err != nil {
				return err
			}
		}

		if err := observePaidCallClaimSQL(paidCallClaimStageItemRead); err != nil {
			return err
		}
		item, position, err := readPaidCallStageItem(ctx, tx, input.StageID, input.ItemID)
		if err != nil {
			return err
		}
		if position > cap {
			return errors.New("paid call item is outside the approved cap")
		}
		if err := matchPaidCallClaimInput(item, input); err != nil {
			return err
		}

		if err := observePaidCallClaimSQL(paidCallClaimSourceStateRead); err != nil {
			return err
		}
		var sourceState string
		if err := tx.QueryRowContext(ctx, `select source_state from asset where id = ?`, input.Prepared.Custody.GetAssetId()).Scan(&sourceState); err != nil {
			return fmt.Errorf("read paid call asset source state: %w", err)
		}
		if sourceState != sourceStateCurrent {
			return fmt.Errorf("paid call asset source state is %q, not current", sourceState)
		}

		if err := observePaidCallClaimSQL(paidCallClaimEligibilityRead); err != nil {
			return err
		}
		eligibility, err := firstCardEligibilityForAsset(ctx, tx, input.Prepared.Custody.GetAssetId())
		if err != nil {
			return err
		}
		if eligibility != firstCardEligible {
			return fmt.Errorf("paid call asset is %s", eligibility)
		}

		if decision.Purpose == paidCallPurposeScreening {
			decision.Call.Request = input.Prepared.Request
			fresh, err := insertPaidCallClaim(ctx, tx, input, decision.Purpose, "")
			if err != nil {
				return err
			}
			decision.Send = fresh
			return nil
		}

		generation, err := prepareModelGenerationTx(
			ctx, tx, input.Prepared.Custody.GetAssetId(), input.Prepared.PromptVersion, input.Prepared.ParserVersion, input.Prepared.Request, input.ClaimedAt,
		)
		decision.GenerationID = generation.GenerationID
		decision.Call = generation.Call
		if err != nil {
			return err
		}
		if err := retainPreparedCardRequest(ctx, tx, input.ItemID, input.Prepared.Custody.GetAssetId(), generation.GenerationID, input.Prepared); err != nil {
			return err
		}
		if !generation.Fresh {
			return nil
		}
		fresh, err := insertPaidCallClaim(ctx, tx, input, decision.Purpose, generation.GenerationID)
		if err != nil {
			return err
		}
		if !fresh {
			return errors.New("paid call claim already exists for a newly created generation attempt")
		}
		decision.Send = true
		return nil
	})
	return decision, err
}

func validatePaidCallClaimInput(input paidCallClaimInput) error {
	for name, value := range map[string]string{
		"stage id": input.StageID, "item id": input.ItemID,
	} {
		if err := requireExactPaidCallValue(name, value); err != nil {
			return err
		}
	}
	if input.ClaimedAt.IsZero() {
		return errors.New("paid call claim time is required")
	}
	return validatePreparedCardRequest(input.Prepared)
}

func readPaidCallStageItem(ctx context.Context, tx *sql.Tx, stageID, itemID string) (paidCallStageTuple, int, error) {
	var item paidCallStageTuple
	var storedItemID string
	var position int
	err := tx.QueryRowContext(ctx, `
select item_id, position, asset_id, card_input_id, custody_sha256, full_current_sha256,
       request_route, model_id, request_sha256, prompt_version, parser_version
from paid_call_stage_item
where stage_id = ? and item_id = ?
`, stageID, itemID).Scan(&storedItemID, &position, &item.AssetID, &item.CardInputID,
		&item.CustodySHA256, &item.FullCurrentSHA256, &item.RequestRoute, &item.ModelID, &item.RequestSHA256,
		&item.PromptVersion, &item.ParserVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return paidCallStageTuple{}, 0, errors.New("paid call stage item does not exist")
	}
	if err != nil {
		return paidCallStageTuple{}, 0, fmt.Errorf("read paid call stage item: %w", err)
	}
	return item, position, nil
}

func matchPaidCallClaimInput(item paidCallStageTuple, input paidCallClaimInput) error {
	digest := input.Prepared.Request.Digest()
	requestSHA256 := hex.EncodeToString(digest[:])
	if item.AssetID != input.Prepared.Custody.GetAssetId() || item.CardInputID != input.Prepared.Input.ID ||
		item.CustodySHA256 != input.Prepared.CustodySHA256 ||
		item.FullCurrentSHA256 != input.Prepared.Image.SHA256 || item.RequestRoute != input.Prepared.Request.Route() ||
		item.ModelID != input.Prepared.Request.Model() || item.RequestSHA256 != requestSHA256 ||
		item.PromptVersion != input.Prepared.PromptVersion || item.ParserVersion != input.Prepared.ParserVersion {
		return errors.New("paid call claim does not match the approved stage item")
	}
	return nil
}

func insertPaidCallClaim(
	ctx context.Context,
	tx *sql.Tx,
	input paidCallClaimInput,
	purpose paidCallPurpose,
	generationID string,
) (bool, error) {
	digest := input.Prepared.Request.Digest()
	requestSHA256 := hex.EncodeToString(digest[:])
	var generation any
	if generationID != "" {
		generation = generationID
	}
	result, err := tx.ExecContext(ctx, `
insert into paid_call_claim(
  stage_id, item_id, purpose, asset_id, card_input_id, custody_sha256, full_current_sha256,
  request_sha256, prompt_version, parser_version, generation_id, claimed_at
)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(stage_id, item_id) do nothing

`, input.StageID, input.ItemID, purpose, input.Prepared.Custody.GetAssetId(), input.Prepared.Input.ID, input.Prepared.CustodySHA256,
		input.Prepared.Image.SHA256, requestSHA256, input.Prepared.PromptVersion, input.Prepared.ParserVersion,
		generation, input.ClaimedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, fmt.Errorf("persist paid call claim: %w", err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read paid call claim insert result: %w", err)
	}
	if inserted == 1 {
		return true, nil
	}
	if err := verifyPaidCallClaim(ctx, tx, input, purpose, generationID); err != nil {
		return false, err
	}
	return false, nil
}

func verifyPaidCallClaim(
	ctx context.Context,
	tx *sql.Tx,
	input paidCallClaimInput,
	purpose paidCallPurpose,
	generationID string,
) error {
	var gotPurpose paidCallPurpose
	var assetID, cardInputID, custodySHA256, fullCurrentSHA256, requestSHA256, promptVersion, parserVersion, claimedAt string
	var gotGeneration sql.NullString
	if err := tx.QueryRowContext(ctx, `
select purpose, asset_id, card_input_id, custody_sha256, full_current_sha256, request_sha256,
       prompt_version, parser_version, generation_id, claimed_at
from paid_call_claim
where stage_id = ? and item_id = ?

`, input.StageID, input.ItemID).Scan(&gotPurpose, &assetID, &cardInputID, &custodySHA256, &fullCurrentSHA256,
		&requestSHA256, &promptVersion, &parserVersion, &gotGeneration, &claimedAt); err != nil {
		return fmt.Errorf("read paid call claim: %w", err)
	}
	digest := input.Prepared.Request.Digest()
	wantDigest := hex.EncodeToString(digest[:])
	wantGenerationValid := generationID != ""
	if gotPurpose != purpose || assetID != input.Prepared.Custody.GetAssetId() || cardInputID != input.Prepared.Input.ID ||
		custodySHA256 != input.Prepared.CustodySHA256 ||
		fullCurrentSHA256 != input.Prepared.Image.SHA256 || requestSHA256 != wantDigest ||
		promptVersion != input.Prepared.PromptVersion || parserVersion != input.Prepared.ParserVersion ||
		gotGeneration.Valid != wantGenerationValid || (gotGeneration.Valid && gotGeneration.String != generationID) {
		return errors.New("paid call claim already exists with different bytes")
	}
	if claimedAt == "" {
		return errors.New("paid call claim has no committed claim time")
	}
	return nil
}

func observePaidCallClaimSQL(step paidCallClaimSQLStep) error {
	if paidCallClaimSQLHook == nil {
		return nil
	}
	return paidCallClaimSQLHook(step)
}
