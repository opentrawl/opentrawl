package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func TestPaidCallStagePersistsOneImmutableOrderedApproval(t *testing.T) {
	ctx := context.Background()
	db, _ := openModelGenerationTestStore(t)
	stage, _ := paidCallTestStage(t, db, paidCallPurposeScreening, 2, 3)
	client, err := model.New(model.Config{BaseURL: "https://models.example.com/api", Model: "fixture-vision"})
	if err != nil {
		t.Fatal(err)
	}

	created, err := createPaidCallStage(ctx, db, stage)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range created.Items {
		retained, _, found, err := restoreRetainedPreparedCardRequest(ctx, db, item.ItemID, client)
		if err != nil || !found {
			t.Fatalf("restore screened item %q: found=%t err=%v", item.ItemID, found, err)
		}
		if !bytes.Equal(retained.Input.Bytes, item.Prepared.Input.Bytes) || !bytes.Equal(retained.CustodyBytes, item.Prepared.CustodyBytes) || !bytes.Equal(retained.Request.Body(), item.Prepared.Request.Body()) {
			t.Fatalf("screening item %q did not retain its prepared boundary", item.ItemID)
		}
	}
	stored, storedTuples := readPaidCallTestStage(t, db, created.ID)
	logPaidCallBoundary(t, "paid_call_stage_input", stage)
	logPaidCallBoundary(t, "paid_call_stage_output", stored)
	if stored.ID != created.ID || stored.Purpose != stage.Purpose ||
		stored.ApprovalReceiptSHA256 != stage.ApprovalReceiptSHA256 ||
		stored.ApprovedCallCap != stage.ApprovedCallCap || len(stored.Items) != len(stage.Items) {
		t.Fatalf("stored paid call stage = %#v", stored)
	}
	for index := range stage.Items {
		if stored.Items[index].ItemID != stage.Items[index].ItemID || stored.Items[index].Position != stage.Items[index].Position || storedTuples[index] != paidCallItemTuple(stage.Items[index]) {
			t.Fatalf("stored item %d = %#v, want %#v", index+1, stored.Items[index], stage.Items[index])
		}
	}

	reopenInput := created
	reopenInput.CreatedAt = created.CreatedAt.Add(time.Hour)
	reopened, err := createPaidCallStage(ctx, db, reopenInput)
	if err != nil || reopened.ID != created.ID || !reopened.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("idempotent stage reopen = %#v, %v", reopened, err)
	}

	changes := map[string]func(*paidCallStage){
		"purpose": func(value *paidCallStage) { value.Purpose = paidCallPurposeCanary },
		"cap":     func(value *paidCallStage) { value.ApprovedCallCap = 1 },
		"receipt": func(value *paidCallStage) { value.ApprovalReceiptSHA256 = paidCallTestSHA("changed receipt") },
		"tuple":   func(value *paidCallStage) { value.Items[0].Prepared.Input.ID = "card_input:changed" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			changed := created
			changed.Items = append([]paidCallStageItem(nil), created.Items...)
			for index := range changed.Items {
				prepared := *changed.Items[index].Prepared
				changed.Items[index].Prepared = &prepared
			}
			change(&changed)
			logPaidCallBoundary(t, "paid_call_changed_stage_input", map[string]any{"case": name, "stage": changed})
			if _, err := createPaidCallStage(ctx, db, changed); err == nil {
				t.Fatal("changed immutable stage input was accepted")
			} else {
				logPaidCallBoundary(t, "paid_call_changed_stage_output", map[string]any{"case": name, "error": err.Error()})
			}
			var stages, items int
			if err := db.DB().QueryRowContext(ctx, `select count(*) from paid_call_stage`).Scan(&stages); err != nil {
				t.Fatal(err)
			}
			if err := db.DB().QueryRowContext(ctx, `select count(*) from paid_call_stage_item`).Scan(&items); err != nil {
				t.Fatal(err)
			}
			if stages != 1 || items != 3 {
				t.Fatalf("changed stage mutated rows: stages=%d items=%d", stages, items)
			}
		})
	}
}

func TestPaidCallStageRejectsInvalidMembershipBeforeWriting(t *testing.T) {
	tests := map[string]func(*paidCallStage){
		"empty":             func(stage *paidCallStage) { stage.Items = nil },
		"zero cap":          func(stage *paidCallStage) { stage.ApprovedCallCap = 0 },
		"cap above items":   func(stage *paidCallStage) { stage.ApprovedCallCap = 4 },
		"non-contiguous":    func(stage *paidCallStage) { stage.Items[1].Position = 3 },
		"duplicate item id": func(stage *paidCallStage) { stage.Items[1].ItemID = stage.Items[0].ItemID },
		"duplicate tuple": func(stage *paidCallStage) {
			stage.Items[1] = stage.Items[0]
			stage.Items[1].ItemID = "item:duplicate-tuple"
			stage.Items[1].Position = 2
		},
		"invalid request sha": func(stage *paidCallStage) { stage.Items[0].Prepared.RequestSHA256 = "not-a-digest" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			db, _ := openModelGenerationTestStore(t)
			stage, _ := paidCallTestStage(t, db, paidCallPurposeScreening, 2, 3)
			mutate(&stage)
			logPaidCallBoundary(t, "paid_call_invalid_stage_input", map[string]any{"case": name, "stage": stage})
			if _, err := createPaidCallStage(ctx, db, stage); err == nil {
				t.Fatal("invalid stage was accepted")
			} else {
				logPaidCallBoundary(t, "paid_call_invalid_stage_output", map[string]any{"case": name, "error": err.Error()})
			}
			var count int
			if err := db.DB().QueryRowContext(ctx, `select count(*) from paid_call_stage`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("invalid stage wrote %d rows", count)
			}
		})
	}
}

func paidCallTestStage(
	t *testing.T,
	db *store.Store,
	purpose paidCallPurpose,
	cap, itemCount int,
) (paidCallStage, map[string]preparedCardRequest) {
	t.Helper()
	client, err := model.New(model.Config{BaseURL: "https://models.example.com/api", Model: "fixture-vision"})
	if err != nil {
		t.Fatal(err)
	}
	stage := paidCallStage{
		Purpose:               purpose,
		ApprovalReceiptSHA256: paidCallTestSHA("synthetic approval receipt"),
		ApprovedCallCap:       cap,
		CreatedAt:             time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC),
	}
	requests := make(map[string]preparedCardRequest, itemCount)
	for position := 1; position <= itemCount; position++ {
		positionText := strconv.Itoa(position)
		assetID := "asset:synthetic"
		if position > 1 {
			assetID = "asset:paid-call-" + positionText
			insertModelGenerationTestAsset(t, db, assetID, "queue:paid-call-"+positionText, "paid-call-"+positionText)
		}
		request, err := client.Render(model.Request{Prompt: "synthetic paid call item " + positionText})
		if err != nil {
			t.Fatal(err)
		}
		prepared := paidCallTestPrepared(t, assetID, request, positionText)
		itemID := approvedCardExecutionID(assetID, prepared)
		item, err := newPaidCallStageItem(itemID, position, prepared)
		if err != nil {
			t.Fatal(err)
		}
		stage.Items = append(stage.Items, item)
		requests[itemID] = prepared
	}
	return stage, requests
}

func paidCallTestPrepared(t *testing.T, assetID string, request model.ProviderRequest, suffix string) preparedCardRequest {
	t.Helper()
	fullCurrentSHA := paidCallTestSHA("synthetic full current " + suffix)
	inputMessage := &cardwire.CardInput{SchemaVersion: cardinput.SchemaVersion, FullCurrent: &cardwire.FullCurrent{Role: "full_current", MediaType: "public.png", SizeBytes: 1, Sha256: fullCurrentSHA}}
	inputBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(inputMessage)
	if err != nil {
		t.Fatal(err)
	}
	inputID := "card_input:" + digestBytes(inputBytes)
	requestDigest := request.Digest()
	requestSHA := hex.EncodeToString(requestDigest[:])
	custody := &cardwire.CardExecutionCustody{SourceId: "source:synthetic", AssetId: assetID, FullCurrentProofSha256: paidCallTestSHA("proof " + suffix), CardInputSha256: inputID[len("card_input:"):], RequestSha256: requestSHA}
	custodyBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(custody)
	if err != nil {
		t.Fatal(err)
	}
	prepared := preparedCardRequest{
		Input: cardinput.Result{Input: inputMessage, Bytes: inputBytes, ID: inputID}, Custody: custody,
		CustodyBytes: custodyBytes, CustodySHA256: digestBytes(custodyBytes), Image: imageMeta{Bytes: 1, SHA256: fullCurrentSHA},
		UTI: "public.png", MIMEType: "image/png", PromptVersion: modelPromptVersion, ParserVersion: modelParserVersion,
		Request: request, RequestSHA256: requestSHA, CandidateByID: map[string]preparedPlaceCandidate{}, CandidatesInSeq: []preparedPlaceCandidate{},
	}
	prepared.CardRequestID = cardRequestID(prepared.Input.ID, prepared.UTI, prepared.MIMEType, prepared.PromptVersion, request)
	return prepared
}

func readPaidCallTestStage(t *testing.T, db *store.Store, stageID string) (paidCallStage, []paidCallStageTuple) {
	t.Helper()
	ctx := context.Background()
	var stage paidCallStage
	var createdAt string
	if err := db.DB().QueryRowContext(ctx, `
select id, purpose, approval_receipt_sha256, approved_call_cap, created_at
from paid_call_stage where id = ?
`, stageID).Scan(&stage.ID, &stage.Purpose, &stage.ApprovalReceiptSHA256, &stage.ApprovedCallCap, &createdAt); err != nil {
		t.Fatal(err)
	}
	stage.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	rows, err := db.DB().QueryContext(ctx, `
select item_id, position, asset_id, card_input_id, custody_sha256, full_current_sha256,
       request_route, model_id, request_sha256, prompt_version, parser_version
from paid_call_stage_item where stage_id = ? order by position
`, stageID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	tuples := make([]paidCallStageTuple, 0)
	for rows.Next() {
		var item paidCallStageItem
		var tuple paidCallStageTuple
		if err := rows.Scan(&item.ItemID, &item.Position, &tuple.AssetID, &tuple.CardInputID,
			&tuple.CustodySHA256, &tuple.FullCurrentSHA256, &tuple.RequestRoute, &tuple.ModelID, &tuple.RequestSHA256,
			&tuple.PromptVersion, &tuple.ParserVersion); err != nil {
			t.Fatal(err)
		}
		stage.Items = append(stage.Items, item)
		tuples = append(tuples, tuple)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return stage, tuples
}

func paidCallTestSHA(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func logPaidCallBoundary(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW boundary=%s\n%s", name, raw)
}
