package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func ApprovedCardApprovalDigest(bundle []byte) (string, error) {
	if _, err := decodeApprovedCardBundle(bundle); err != nil {
		return "", err
	}
	bundleDigest := sha256.Sum256(bundle)
	return hex.EncodeToString(bundleDigest[:]), nil
}

type ApprovedCardReview struct {
	PhotoRef         string
	ProviderIdentity string
	Endpoint         string
	Model            string
	CredentialEnv    string
	RequestSHA256    string
	ApprovalSHA256   string
	CallCap          int
	State            string
}

func ReviewApprovedCardBundle(bundleBytes []byte) (ApprovedCardReview, error) {
	var review ApprovedCardReview
	bundle, err := decodeApprovedCardBundle(bundleBytes)
	if err != nil {
		return review, err
	}
	if bundle.GetPurpose() != string(paidCallPurposeCanary) || bundle.GetCallCap() != 1 || len(bundle.GetItems()) != 1 {
		return review, errors.New("approved card request must contain one photo and one model call")
	}
	approval, err := ApprovedCardApprovalDigest(bundleBytes)
	if err != nil {
		return review, err
	}
	item := bundle.GetItems()[0]
	return ApprovedCardReview{
		PhotoRef: AssetRef(item.GetAssetId()), ProviderIdentity: bundle.GetProviderIdentity(),
		Endpoint: item.GetRequestRoute(), Model: item.GetModelId(), CredentialEnv: bundle.GetCredentialEnv(),
		RequestSHA256: item.GetRequestSha256(), ApprovalSHA256: approval,
		CallCap: int(bundle.GetCallCap()), State: "ready",
	}, nil
}

// ValidateApprovedCardSend checks the explicit local approval and every
// immutable cross-link before the caller opens an archive for writing.
func ValidateApprovedCardSend(bundleBytes []byte, approvalSHA256, credentialEnv string) error {
	want, err := ApprovedCardApprovalDigest(bundleBytes)
	if err != nil {
		return err
	}
	if approvalSHA256 != want {
		return errors.New("approved card approval does not match the prepared request")
	}
	bundle, err := decodeApprovedCardBundle(bundleBytes)
	if err != nil {
		return err
	}
	if bundle.GetCredentialEnv() != strings.TrimSpace(credentialEnv) {
		return errors.New("approved card credential reference does not match the prepared request")
	}
	return nil
}

type ApprovedCardSendItem struct {
	AssetID string
	Model   string
	State   string
}

type ApprovedCardSendResult struct {
	Items []ApprovedCardSendItem
}

// CompletedApprovedCardBundle checks the durable execution receipt without
// opening the archive for writing. A completed approval can be reported again
// even when its source facts or configured credential are no longer present.
func CompletedApprovedCardBundle(ctx context.Context, archivePath string, bundleBytes []byte) (ApprovedCardSendResult, bool, error) {
	var result ApprovedCardSendResult
	if _, err := ReviewApprovedCardBundle(bundleBytes); err != nil {
		return result, false, err
	}
	bundle, err := decodeApprovedCardBundle(bundleBytes)
	if err != nil {
		return result, false, err
	}
	db, err := openCardInputAuditArchive(ctx, archivePath)
	if err != nil {
		return result, false, err
	}
	defer func() { _ = db.Close() }()
	for _, raw := range bundle.GetItems() {
		completed, err := approvedCardCompleted(ctx, db, raw.GetExecutionId())
		if err != nil {
			return ApprovedCardSendResult{}, false, err
		}
		if !completed {
			return ApprovedCardSendResult{}, false, nil
		}
		result.Items = append(result.Items, ApprovedCardSendItem{
			AssetID: raw.GetAssetId(), Model: raw.GetModelId(), State: "already_created",
		})
	}
	return result, true, nil
}

// SendApprovedCardBundle accepts one exact local approval of canonical prepared
// bytes. It validates every configured request before it creates the ledger.
func SendApprovedCardBundle(ctx context.Context, db *store.Store, bundleBytes []byte, approvalSHA256, credentialEnv string, now time.Time, transport approvedCardTransport) (ApprovedCardSendResult, error) {
	var result ApprovedCardSendResult
	if db == nil || transport == nil {
		return result, errors.New("approved card archive and transport are required")
	}
	if err := ValidateApprovedCardSend(bundleBytes, approvalSHA256, credentialEnv); err != nil {
		return result, err
	}
	bundle, err := decodeApprovedCardBundle(bundleBytes)
	if err != nil {
		return result, err
	}
	stage := paidCallStage{
		Purpose: paidCallPurpose(bundle.GetPurpose()), ApprovalReceiptSHA256: approvalSHA256,
		ApprovedCallCap: int(bundle.GetCallCap()), CreatedAt: now,
	}
	items := make([]approvedCardItem, 0, len(bundle.GetItems()))
	for _, raw := range bundle.GetItems() {
		prepared, err := restorePreparedCardRequestUnchecked(raw)
		if err != nil {
			return result, err
		}
		if err := transport.ValidateRequest(prepared.Request); err != nil {
			return result, fmt.Errorf("validate approved card request: %w", err)
		}
		stageItem, err := newPaidCallStageItem(raw.GetExecutionId(), int(raw.GetPosition()), prepared)
		if err != nil {
			return result, err
		}
		stage.Items = append(stage.Items, stageItem)
		items = append(items, approvedCardItem{raw: raw, prepared: prepared})
	}
	stage, err = createPaidCallStage(ctx, db, stage)
	if err != nil {
		return result, err
	}
	for index := range items {
		state, err := executeApprovedCard(ctx, db, stage, items[index], now, transport)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, ApprovedCardSendItem{
			AssetID: items[index].raw.GetAssetId(), Model: items[index].prepared.Request.Model(), State: state,
		})
	}
	return result, nil
}

type approvedCardItem struct {
	raw      *cardwire.ApprovedCardItem
	prepared preparedCardRequest
}

func executeApprovedCard(ctx context.Context, db *store.Store, stage paidCallStage, item approvedCardItem, now time.Time, transport approvedCardTransport) (string, error) {
	if completed, err := approvedCardCompleted(ctx, db, item.raw.GetExecutionId()); err != nil {
		return "", err
	} else if completed {
		return "already_created", nil
	}
	decision, err := claimPaidCall(ctx, db, paidCallClaimInput{StageID: stage.ID, ItemID: item.raw.GetExecutionId(), Prepared: item.prepared, ClaimedAt: now})
	if err != nil {
		return "", err
	}
	if decision.Call.Reused {
		return "", errors.New("approved card completed generation has no card execution")
	}
	if decision.Call.Retained != nil {
		if err := writeApprovedCard(ctx, db, item, decision.GenerationID, *decision.Call.Retained, now); err != nil {
			return "", err
		}
		return "created", nil
	}
	if !decision.Send {
		return "", errors.New("approved card claim did not authorise a send")
	}
	if err := transport.ValidateRequest(decision.Call.Request); err != nil {
		return "", fmt.Errorf("validate approved card request before send: %w", err)
	}
	raw, sendErr := transport.Send(ctx, decision.Call.Request)
	if err := retainModelGenerationResult(ctx, db, decision.GenerationID, raw, now); err != nil {
		return "", err
	}
	if sendErr != nil {
		return "", sendErr
	}
	if err := writeApprovedCard(ctx, db, item, decision.GenerationID, raw, now); err != nil {
		return "", err
	}
	return "created", nil
}

func writeApprovedCard(ctx context.Context, db *store.Store, item approvedCardItem, generationID string, raw model.RawResult, now time.Time) error {
	classifier := modelClassifier{modelID: item.prepared.Request.Model(), promptVersion: item.prepared.PromptVersion}
	result, err := parseRetainedModelGeneration(ctx, db, generationID, item.raw.GetAssetId(), classifier, item.prepared, raw, now)
	if err != nil {
		return err
	}
	queueID, err := approvedCardQueueID(ctx, db, item.raw.GetAssetId())
	if err != nil {
		return err
	}
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, _, err := writeModelClassification(ctx, tx, classifyInput{AssetID: item.raw.GetAssetId(), QueueID: queueID}, classifier, result, item.prepared, now, generationID); err != nil {
			return err
		}
		if err := completeModelGeneration(ctx, tx, generationID, item.raw.GetAssetId(), now); err != nil {
			return err
		}
		return completePreparedCardRequest(ctx, tx, item.raw.GetExecutionId(), now.UTC().Format(time.RFC3339Nano))
	})
}

func approvedCardQueueID(ctx context.Context, db *store.Store, assetID string) (string, error) {
	var queueID string
	if err := db.DB().QueryRowContext(ctx, `select id from classification_queue where asset_id = ?`, assetID).Scan(&queueID); err != nil {
		return "", fmt.Errorf("read approved card queue: %w", err)
	}
	return queueID, nil
}

func approvedCardCompleted(ctx context.Context, db *store.Store, executionID string) (bool, error) {
	var found int
	err := db.DB().QueryRowContext(ctx, `select 1 from card_execution where id = ? and completed_at <> ''`, executionID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
