package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type paidCallPurpose string

const (
	paidCallPurposeScreening paidCallPurpose = "screening"
	paidCallPurposeCanary    paidCallPurpose = "canary"
	paidCallPurposeBackfill  paidCallPurpose = "backfill"
)

type paidCallStageItem struct {
	ItemID   string
	Position int
	Prepared *preparedCardRequest
}

type paidCallStage struct {
	ID                    string
	Purpose               paidCallPurpose
	ApprovalReceiptSHA256 string
	ApprovedCallCap       int
	Items                 []paidCallStageItem
	CreatedAt             time.Time
}

type paidCallStageTuple struct {
	AssetID           string
	CardInputID       string
	CustodySHA256     string
	FullCurrentSHA256 string
	RequestRoute      string
	ModelID           string
	RequestSHA256     string
	PromptVersion     string
	ParserVersion     string
}

func newPaidCallStageItem(itemID string, position int, prepared preparedCardRequest) (paidCallStageItem, error) {
	if err := validatePreparedCardRequest(prepared); err != nil {
		return paidCallStageItem{}, err
	}
	return paidCallStageItem{
		ItemID: itemID, Position: position, Prepared: &prepared,
	}, nil
}

func createPaidCallStage(ctx context.Context, db *store.Store, stage paidCallStage) (paidCallStage, error) {
	if db == nil {
		return paidCallStage{}, errors.New("archive store is not open")
	}
	if err := validatePaidCallStage(stage); err != nil {
		return paidCallStage{}, err
	}
	derivedID := paidCallStageID(stage)
	if stage.ID != "" && stage.ID != derivedID {
		return paidCallStage{}, errors.New("paid call stage id does not match its immutable input")
	}
	stage.ID = derivedID
	stage.CreatedAt = stage.CreatedAt.UTC()

	err := db.WithTx(ctx, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `
insert into paid_call_stage(
  id, purpose, approval_receipt_sha256, approved_call_cap, item_count, claim_serial, created_at
)
values (?, ?, ?, ?, ?, 0, ?)
on conflict(id) do nothing
`, stage.ID, stage.Purpose, stage.ApprovalReceiptSHA256, stage.ApprovedCallCap, len(stage.Items), stage.CreatedAt.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("persist paid call stage: %w", err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read paid call stage insert result: %w", err)
		}
		if inserted == 0 {
			createdAt, err := verifyPaidCallStage(ctx, tx, stage)
			if err != nil {
				return err
			}
			stage.CreatedAt = createdAt
			return nil
		}
		for _, item := range stage.Items {
			tuple := paidCallItemTuple(item)
			if _, err := retainPreparedModelBoundaryTx(ctx, tx, item.ItemID, item.Prepared.Custody.GetAssetId(), *item.Prepared, stage.CreatedAt); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
insert into paid_call_stage_item(
  stage_id, item_id, position, asset_id, card_input_id, custody_sha256, full_current_sha256,
  request_route, model_id, request_sha256, prompt_version, parser_version
)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, stage.ID, item.ItemID, item.Position, tuple.AssetID, tuple.CardInputID, tuple.CustodySHA256, tuple.FullCurrentSHA256,
				tuple.RequestRoute, tuple.ModelID, tuple.RequestSHA256, tuple.PromptVersion, tuple.ParserVersion); err != nil {
				return fmt.Errorf("persist paid call stage item %d: %w", item.Position, err)
			}
		}
		return nil
	})
	if err != nil {
		return paidCallStage{}, err
	}
	stage.Items = append([]paidCallStageItem(nil), stage.Items...)
	return stage, nil
}

func validatePaidCallStage(stage paidCallStage) error {
	switch stage.Purpose {
	case paidCallPurposeScreening, paidCallPurposeCanary, paidCallPurposeBackfill:
	default:
		return fmt.Errorf("paid call purpose %q is not supported", stage.Purpose)
	}
	if err := validateSHA256("approval receipt", stage.ApprovalReceiptSHA256); err != nil {
		return err
	}
	if len(stage.Items) == 0 {
		return errors.New("paid call stage requires at least one item")
	}
	if stage.ApprovedCallCap <= 0 || stage.ApprovedCallCap > len(stage.Items) {
		return errors.New("paid call cap must be positive and no greater than the item count")
	}
	if stage.CreatedAt.IsZero() {
		return errors.New("paid call stage creation time is required")
	}

	itemIDs := make(map[string]struct{}, len(stage.Items))
	tuples := make(map[paidCallStageTuple]struct{}, len(stage.Items))
	for index, item := range stage.Items {
		if item.Prepared == nil {
			return fmt.Errorf("paid call item %d: prepared card request is required", item.Position)
		}
		if err := validatePreparedCardRequest(*item.Prepared); err != nil {
			return fmt.Errorf("paid call item %d: %w", item.Position, err)
		}
		tuple := paidCallItemTuple(item)
		if item.Position != index+1 {
			return errors.New("paid call item positions must be contiguous and start at 1")
		}
		for name, value := range map[string]string{
			"item id": item.ItemID, "asset id": tuple.AssetID, "CardInput id": tuple.CardInputID,
			"request route": tuple.RequestRoute, "model id": tuple.ModelID,
			"prompt version": tuple.PromptVersion, "parser version": tuple.ParserVersion,
		} {
			if err := requireExactPaidCallValue(name, value); err != nil {
				return fmt.Errorf("paid call item %d: %w", item.Position, err)
			}
		}
		if err := validateSHA256("full-current", tuple.FullCurrentSHA256); err != nil {
			return fmt.Errorf("paid call item %d: %w", item.Position, err)
		}
		if err := validateSHA256("custody", tuple.CustodySHA256); err != nil {
			return fmt.Errorf("paid call item %d: %w", item.Position, err)
		}
		if err := validateSHA256("request", tuple.RequestSHA256); err != nil {
			return fmt.Errorf("paid call item %d: %w", item.Position, err)
		}
		if _, exists := itemIDs[item.ItemID]; exists {
			return fmt.Errorf("paid call item id %q is duplicated", item.ItemID)
		}
		itemIDs[item.ItemID] = struct{}{}
		if _, exists := tuples[tuple]; exists {
			return fmt.Errorf("paid call item tuple at position %d is duplicated", item.Position)
		}
		tuples[tuple] = struct{}{}
	}
	return nil
}

func verifyPaidCallStage(ctx context.Context, tx *sql.Tx, want paidCallStage) (time.Time, error) {
	var purpose paidCallPurpose
	var receipt, createdAt string
	var cap, itemCount int
	if err := tx.QueryRowContext(ctx, `
select purpose, approval_receipt_sha256, approved_call_cap, item_count, created_at
from paid_call_stage
where id = ?
`, want.ID).Scan(&purpose, &receipt, &cap, &itemCount, &createdAt); err != nil {
		return time.Time{}, fmt.Errorf("read paid call stage: %w", err)
	}
	if purpose != want.Purpose || receipt != want.ApprovalReceiptSHA256 || cap != want.ApprovedCallCap ||
		itemCount != len(want.Items) {
		return time.Time{}, errors.New("paid call stage identity already exists with different bytes")
	}
	storedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("read paid call stage creation time: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
select item_id, position, asset_id, card_input_id, custody_sha256, full_current_sha256,
       request_route, model_id, request_sha256, prompt_version, parser_version
from paid_call_stage_item
where stage_id = ?
order by position
`, want.ID)
	if err != nil {
		return time.Time{}, fmt.Errorf("read paid call stage items: %w", err)
	}
	defer func() { _ = rows.Close() }()
	got := make([]struct {
		itemID   string
		position int
		tuple    paidCallStageTuple
	}, 0, len(want.Items))
	for rows.Next() {
		var item struct {
			itemID   string
			position int
			tuple    paidCallStageTuple
		}
		if err := rows.Scan(&item.itemID, &item.position, &item.tuple.AssetID, &item.tuple.CardInputID,
			&item.tuple.CustodySHA256, &item.tuple.FullCurrentSHA256, &item.tuple.RequestRoute, &item.tuple.ModelID, &item.tuple.RequestSHA256,
			&item.tuple.PromptVersion, &item.tuple.ParserVersion); err != nil {
			return time.Time{}, fmt.Errorf("scan paid call stage item: %w", err)
		}
		got = append(got, item)
	}
	if err := rows.Err(); err != nil {
		return time.Time{}, fmt.Errorf("read paid call stage items: %w", err)
	}
	if len(got) != len(want.Items) {
		return time.Time{}, errors.New("paid call stage identity already exists with a different item count")
	}
	for index := range got {
		if got[index].tuple != paidCallItemTuple(want.Items[index]) || got[index].itemID != want.Items[index].ItemID || got[index].position != want.Items[index].Position {
			return time.Time{}, fmt.Errorf("paid call stage identity already exists with different item bytes at position %d", index+1)
		}
	}
	return storedCreatedAt, nil
}

func paidCallStageID(stage paidCallStage) string {
	hash := sha256.New()
	parts := []string{string(stage.Purpose), stage.ApprovalReceiptSHA256, strconv.Itoa(stage.ApprovedCallCap), strconv.Itoa(len(stage.Items))}
	for _, item := range stage.Items {
		tuple := paidCallItemTuple(item)
		parts = append(parts, strconv.Itoa(item.Position), item.ItemID, tuple.AssetID, tuple.CardInputID,
			tuple.CustodySHA256, tuple.FullCurrentSHA256, tuple.RequestRoute, tuple.ModelID, tuple.RequestSHA256,
			tuple.PromptVersion, tuple.ParserVersion)
	}
	for _, part := range parts {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(part))
	}
	return "paid_call_stage:" + hex.EncodeToString(hash.Sum(nil))
}

func paidCallItemTuple(item paidCallStageItem) paidCallStageTuple {
	if item.Prepared == nil {
		return paidCallStageTuple{}
	}
	digest := item.Prepared.Request.Digest()
	return paidCallStageTuple{
		AssetID: item.Prepared.Custody.GetAssetId(), CardInputID: item.Prepared.Input.ID,
		CustodySHA256: item.Prepared.CustodySHA256, FullCurrentSHA256: item.Prepared.Image.SHA256,
		RequestRoute: item.Prepared.Request.Route(), ModelID: item.Prepared.Request.Model(),
		RequestSHA256: hex.EncodeToString(digest[:]), PromptVersion: item.Prepared.PromptVersion,
		ParserVersion: item.Prepared.ParserVersion,
	}
}

func requireExactPaidCallValue(name, value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must be non-empty and have no surrounding whitespace", name)
	}
	return nil
}

func validateSHA256(name, value string) error {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size || value != strings.ToLower(value) {
		return fmt.Errorf("%s SHA-256 must be 64 lower-case hexadecimal characters", name)
	}
	return nil
}
