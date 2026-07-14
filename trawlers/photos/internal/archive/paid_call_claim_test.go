package archive

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func TestPaidCallClaimUsesFixedCapMembershipAndStageWriteFirst(t *testing.T) {
	ctx := context.Background()
	db, _ := openModelGenerationTestStore(t)
	stage, requests := paidCallTestStage(t, db, paidCallPurposeScreening, 2, 3)
	stage, err := createPaidCallStage(ctx, db, stage)
	if err != nil {
		t.Fatal(err)
	}

	var steps []paidCallClaimSQLStep
	paidCallClaimSQLHook = func(step paidCallClaimSQLStep) error {
		steps = append(steps, step)
		if len(steps) == 1 && step != paidCallClaimStageWrite {
			return errors.New("paid call claim read before taking the writer lock")
		}
		return nil
	}
	t.Cleanup(func() { paidCallClaimSQLHook = nil })

	position3 := paidCallClaimForItem(stage, stage.Items[2], requests[stage.Items[2].ItemID])
	logPaidCallBoundary(t, "paid_call_outside_cap_input", paidCallClaimBoundary(position3))
	if _, err := claimPaidCall(ctx, db, position3); err == nil {
		t.Fatal("position 3 claimed outside cap 2")
	} else {
		logPaidCallBoundary(t, "paid_call_outside_cap_output", map[string]any{"error": err.Error(), "claim_rows": 0})
	}
	unknown := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
	unknown.ItemID = "item:unknown"
	logPaidCallBoundary(t, "paid_call_unknown_item_input", paidCallClaimBoundary(unknown))
	if _, err := claimPaidCall(ctx, db, unknown); err == nil {
		t.Fatal("unknown item claimed")
	} else {
		logPaidCallBoundary(t, "paid_call_unknown_item_output", map[string]any{"error": err.Error(), "claim_rows": 0})
	}

	for _, item := range stage.Items[:2] {
		input := paidCallClaimForItem(stage, item, requests[item.ItemID])
		logPaidCallBoundary(t, "paid_call_within_cap_input", paidCallClaimBoundary(input))
		decision, err := claimPaidCall(ctx, db, input)
		if err != nil || !decision.Send || decision.Purpose != paidCallPurposeScreening || decision.GenerationID != "" {
			t.Fatalf("authorised screening claim = %#v, %v", decision, err)
		}
		logPaidCallBoundary(t, "paid_call_within_cap_output", map[string]any{"send": decision.Send, "purpose": decision.Purpose})
		input.ClaimedAt = input.ClaimedAt.Add(time.Hour)
		repeated, err := claimPaidCall(ctx, db, input)
		if err != nil || repeated.Send {
			t.Fatalf("repeated screening claim = %#v, %v", repeated, err)
		}
	}

	wantSteps := []paidCallClaimSQLStep{
		paidCallClaimStageWrite, paidCallClaimStageItemRead,
	}
	if len(steps) < len(wantSteps) || !reflect.DeepEqual(steps[:len(wantSteps)], wantSteps) {
		t.Fatalf("first claim SQL steps = %#v", steps)
	}
	var claims, serial int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from paid_call_claim`).Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `select claim_serial from paid_call_stage where id = ?`, stage.ID).Scan(&serial); err != nil {
		t.Fatal(err)
	}
	logPaidCallBoundary(t, "paid_call_cap_output", map[string]any{"claims": claims, "claim_serial": serial, "steps": steps})
	if claims != 2 || serial != 4 {
		t.Fatalf("fixed membership rows: claims=%d serial=%d", claims, serial)
	}
}

func TestPaidCallClaimRejectsEveryTupleMismatchWithoutRows(t *testing.T) {
	ctx := context.Background()
	db, _ := openModelGenerationTestStore(t)
	stage, requests := paidCallTestStage(t, db, paidCallPurposeScreening, 1, 1)
	stage, err := createPaidCallStage(ctx, db, stage)
	if err != nil {
		t.Fatal(err)
	}
	base := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
	changedBody, err := model.RestoreProviderRequest(base.Prepared.Request.Route(), base.Prepared.Request.Model(), []byte(`{"model":"fixture-vision","prompt":"changed"}`))
	if err != nil {
		t.Fatal(err)
	}
	changedModel, err := model.RestoreProviderRequest(base.Prepared.Request.Route(), "fixture-vision-changed", base.Prepared.Request.Body())
	if err != nil {
		t.Fatal(err)
	}
	changedRoute, err := model.RestoreProviderRequest("https://alternate.example.com/api/generate", base.Prepared.Request.Model(), base.Prepared.Request.Body())
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*paidCallClaimInput){
		"asset": func(input *paidCallClaimInput) {
			input.Prepared.Custody = proto.Clone(input.Prepared.Custody).(*cardwire.CardExecutionCustody)
			input.Prepared.Custody.AssetId = "asset:unknown"
		},
		"CardInput":    func(input *paidCallClaimInput) { input.Prepared.Input.ID = "card_input:changed" },
		"full current": func(input *paidCallClaimInput) { input.Prepared.Image.SHA256 = paidCallTestSHA("changed full current") },
		"request":      func(input *paidCallClaimInput) { input.Prepared.Request = changedBody },
		"model":        func(input *paidCallClaimInput) { input.Prepared.Request = changedModel },
		"route":        func(input *paidCallClaimInput) { input.Prepared.Request = changedRoute },
		"prompt":       func(input *paidCallClaimInput) { input.Prepared.PromptVersion = "fixture-prompt-v2" },
		"parser":       func(input *paidCallClaimInput) { input.Prepared.ParserVersion = "fixture-parser-v2" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			input := base
			change(&input)
			logPaidCallBoundary(t, "paid_call_mismatch_input", map[string]any{"case": name, "claim": paidCallClaimBoundary(input)})
			if _, err := claimPaidCall(ctx, db, input); err == nil {
				t.Fatal("mismatched claim was accepted")
			} else {
				logPaidCallBoundary(t, "paid_call_mismatch_output", map[string]any{"case": name, "error": err.Error(), "claim_rows": 0})
			}
			assertPaidCallCounts(t, db, 0, 1, 0)
		})
	}
}

func TestPaidCallScreeningCannotSatisfyStoredCardGeneration(t *testing.T) {
	ctx := context.Background()
	db, _ := openModelGenerationTestStore(t)
	screening, requests := paidCallTestStage(t, db, paidCallPurposeScreening, 1, 1)
	screening, err := createPaidCallStage(ctx, db, screening)
	if err != nil {
		t.Fatal(err)
	}
	item := screening.Items[0]
	request := requests[item.ItemID]
	screeningInput := paidCallClaimForItem(screening, item, request)
	logPaidCallBoundary(t, "paid_call_screening_input", paidCallClaimBoundary(screeningInput))
	screeningDecision, err := claimPaidCall(ctx, db, screeningInput)
	if err != nil || !screeningDecision.Send || screeningDecision.GenerationID != "" {
		t.Fatalf("screening decision = %#v, %v", screeningDecision, err)
	}
	assertPaidCallCounts(t, db, 1, 1, 0)

	canary := paidCallStage{
		Purpose:               paidCallPurposeCanary,
		ApprovalReceiptSHA256: paidCallTestSHA("synthetic canary approval"),
		ApprovedCallCap:       1,
		Items:                 append([]paidCallStageItem(nil), screening.Items...),
		CreatedAt:             screening.CreatedAt.Add(time.Hour),
	}
	canary, err = createPaidCallStage(ctx, db, canary)
	if err != nil {
		t.Fatal(err)
	}
	canaryInput := paidCallClaimForItem(canary, canary.Items[0], request)
	canaryInput.ClaimedAt = screeningInput.ClaimedAt.Add(time.Hour)
	logPaidCallBoundary(t, "paid_call_canary_after_screening_input", paidCallClaimBoundary(canaryInput))
	canaryDecision, err := claimPaidCall(ctx, db, canaryInput)
	if err != nil || !canaryDecision.Send || canaryDecision.GenerationID == "" {
		t.Fatalf("canary decision after screening = %#v, %v", canaryDecision, err)
	}
	assertPaidCallCounts(t, db, 2, 1, 1)
}

func TestPaidCallProhibitedFirstCardCreatesNoClaimOrGeneration(t *testing.T) {
	ctx := context.Background()
	db, assetID := openModelGenerationTestStore(t)
	if _, err := db.DB().ExecContext(ctx, `
update asset
set first_card_blocked_at = '2026-07-11T10:00:00Z',
    first_card_blocked_snapshot_id = 'snapshot:synthetic-missing'
where id = ?
`, assetID); err != nil {
		t.Fatal(err)
	}
	stage, requests := paidCallTestStage(t, db, paidCallPurposeCanary, 1, 1)
	stage, err := createPaidCallStage(ctx, db, stage)
	if err != nil {
		t.Fatal(err)
	}
	input := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
	logPaidCallBoundary(t, "paid_call_prohibited_input", map[string]any{
		"claim": paidCallClaimBoundary(input), "first_card_blocked_at": "2026-07-11T10:00:00Z",
		"first_card_blocked_snapshot_id": "snapshot:synthetic-missing",
	})
	if _, err := claimPaidCall(ctx, db, input); err == nil {
		t.Fatal("prohibited first card was authorised")
	}
	assertPaidCallCounts(t, db, 0, 1, 0)
	logPaidCallBoundary(t, "paid_call_prohibited_output", map[string]int{"claims": 0, "generations": 0, "attempts": 0})
}

func paidCallClaimForItem(stage paidCallStage, item paidCallStageItem, prepared preparedCardRequest) paidCallClaimInput {
	return paidCallClaimInput{
		StageID: stage.ID, ItemID: item.ItemID, Prepared: prepared,
		ClaimedAt: time.Date(2026, 7, 11, 13, 0, 0, 0, time.UTC),
	}
}

func paidCallClaimBoundary(input paidCallClaimInput) map[string]any {
	return map[string]any{
		"stage_id": input.StageID, "item_id": input.ItemID, "asset_id": input.Prepared.Custody.GetAssetId(),
		"card_input_id": input.Prepared.Input.ID, "full_current_sha256": input.Prepared.Image.SHA256,
		"request_route": input.Prepared.Request.Route(), "model_id": input.Prepared.Request.Model(),
		"request_body": string(input.Prepared.Request.Body()), "prompt_version": input.Prepared.PromptVersion,
		"parser_version": input.Prepared.ParserVersion, "claimed_at": input.ClaimedAt.UTC().Format(time.RFC3339Nano),
	}
}

func assertPaidCallCounts(t *testing.T, db *store.Store, claims, generations, attempts int) {
	t.Helper()
	ctx := context.Background()
	for table, want := range map[string]int{
		"paid_call_claim": claims, "model_generation": generations, "model_generation_attempt": attempts,
	} {
		var got int
		if err := db.DB().QueryRowContext(ctx, "select count(*) from "+table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows = %d, want %d", table, got, want)
		}
	}
}
