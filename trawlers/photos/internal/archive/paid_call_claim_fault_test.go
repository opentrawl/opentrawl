package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

func TestPaidCallCommittedDecisionUsesExactFixtureRequestAndRetainsRawResult(t *testing.T) {
	ctx := context.Background()
	var received []byte
	response := []byte(`{"response":"synthetic retained card","done":true}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var err error
		received, err = io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read fixture request: %v", err)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(response)
	}))
	defer server.Close()
	client, err := model.New(model.Config{BaseURL: server.URL, Model: "fixture-vision"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.Render(model.Request{Prompt: "synthetic fixture transport boundary"})
	if err != nil {
		t.Fatal(err)
	}

	db, _ := openModelGenerationTestStore(t)
	stage, requests := paidCallTestStage(t, db, paidCallPurposeCanary, 1, 1)
	prepared := requests[stage.Items[0].ItemID]
	prepared.Request = request
	digest := request.Digest()
	prepared.RequestSHA256 = hex.EncodeToString(digest[:])
	prepared.Custody = proto.Clone(prepared.Custody).(*cardwire.CardExecutionCustody)
	prepared.Custody.RequestSha256 = prepared.RequestSHA256
	prepared.CustodyBytes, err = proto.MarshalOptions{Deterministic: true}.Marshal(prepared.Custody)
	if err != nil {
		t.Fatal(err)
	}
	prepared.CustodySHA256 = digestBytes(prepared.CustodyBytes)
	prepared.CardRequestID = cardRequestID(prepared.Input.ID, prepared.UTI, prepared.MIMEType, prepared.PromptVersion, request)
	stage.Items[0], err = newPaidCallStageItem(stage.Items[0].ItemID, stage.Items[0].Position, prepared)
	if err != nil {
		t.Fatal(err)
	}
	stage, err = createPaidCallStage(ctx, db, stage)
	if err != nil {
		t.Fatal(err)
	}
	input := paidCallClaimForItem(stage, stage.Items[0], prepared)
	logPaidCallBoundary(t, "paid_call_fixture_claim_input", paidCallClaimBoundary(input))
	decision, err := claimPaidCall(ctx, db, input)
	if err != nil || !decision.Send {
		t.Fatalf("fixture decision = %#v, %v", decision, err)
	}
	raw, err := client.Send(ctx, decision.Call.Request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(received, request.Body()) || !bytes.Equal(raw.Response, response) {
		t.Fatalf("fixture boundary mismatch: received=%s response=%s", received, raw.Response)
	}
	if err := retainModelGenerationResult(ctx, db, decision.GenerationID, raw, input.ClaimedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	input.ClaimedAt = input.ClaimedAt.Add(time.Hour)
	restarted, err := claimPaidCall(ctx, db, input)
	if err != nil || restarted.Send || restarted.Call.Retained == nil || !bytes.Equal(restarted.Call.Retained.Response, response) {
		t.Fatalf("fixture restart = %#v, %v", restarted, err)
	}
	logPaidCallBoundary(t, "paid_call_fixture_input", map[string]any{
		"route": request.Route(), "model": request.Model(), "body": string(request.Body()),
	})
	logPaidCallBoundary(t, "paid_call_fixture_output", map[string]any{
		"received_body": string(received), "raw_response": string(raw.Response), "restart_send": restarted.Send,
	})
}

func TestPaidCallClaimSerialisesBothSourceDeletionCommitOrders(t *testing.T) {
	t.Run("deletion commits first", func(t *testing.T) {
		ctx := context.Background()
		db, assetID := openModelGenerationTestStore(t)
		stage, requests := paidCallTestStage(t, db, paidCallPurposeCanary, 1, 1)
		stage, err := createPaidCallStage(ctx, db, stage)
		if err != nil {
			t.Fatal(err)
		}
		input := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
		if _, err := db.DB().ExecContext(ctx, `update asset set source_state = ? where id = ?`, sourceStateDeletedUpstream, assetID); err != nil {
			t.Fatal(err)
		}
		decision, err := claimPaidCall(ctx, db, input)
		if err == nil || decision.Send {
			t.Fatalf("deletion-first decision = %#v, %v", decision, err)
		}
		assertPaidCallCounts(t, db, 0, 1, 0)
		logPaidCallBoundary(t, "paid_call_deletion_first_input", map[string]any{"claim": paidCallClaimBoundary(input), "asset_state": sourceStateDeletedUpstream})
		logPaidCallBoundary(t, "paid_call_deletion_first_output", map[string]any{"send": decision.Send, "error": err.Error(), "claims": 0})
	})

	t.Run("claim takes writer lock first", func(t *testing.T) {
		ctx := context.Background()
		db, assetID := openModelGenerationTestStore(t)
		stage, requests := paidCallTestStage(t, db, paidCallPurposeCanary, 1, 1)
		stage, err := createPaidCallStage(ctx, db, stage)
		if err != nil {
			t.Fatal(err)
		}
		input := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
		second, err := store.Open(ctx, store.Options{Path: db.Path(), Schema: Schema, SchemaVersion: SchemaVersion})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = second.Close() }()

		locked := make(chan struct{})
		release := make(chan struct{})
		paidCallClaimAfterStageWrite = func() error {
			close(locked)
			<-release
			return nil
		}
		t.Cleanup(func() { paidCallClaimAfterStageWrite = nil })
		type claimResult struct {
			decision paidCallDecision
			err      error
		}
		claimDone := make(chan claimResult, 1)
		go func() {
			decision, err := claimPaidCall(ctx, db, input)
			claimDone <- claimResult{decision: decision, err: err}
		}()
		<-locked

		deleteStarted := make(chan struct{})
		deleteDone := make(chan error, 1)
		var claimsVisibleToDeletion int
		go func() {
			deleteDone <- second.WithTx(ctx, func(tx *sql.Tx) error {
				close(deleteStarted)
				if _, err := tx.ExecContext(ctx, `update asset set source_state = ? where id = ?`, sourceStateDeletedUpstream, assetID); err != nil {
					return err
				}
				return tx.QueryRowContext(ctx, `select count(*) from paid_call_claim where stage_id = ?`, stage.ID).Scan(&claimsVisibleToDeletion)
			})
		}()
		<-deleteStarted
		close(release)

		claimed := <-claimDone
		if claimed.err != nil || !claimed.decision.Send {
			t.Fatalf("claim-first decision = %#v, %v", claimed.decision, claimed.err)
		}
		if err := <-deleteDone; err != nil {
			t.Fatal(err)
		}
		if claimsVisibleToDeletion != 1 {
			t.Fatalf("deletion transaction saw %d claims, want committed claim", claimsVisibleToDeletion)
		}
		var sourceState string
		if err := db.DB().QueryRowContext(ctx, `select source_state from asset where id = ?`, assetID).Scan(&sourceState); err != nil {
			t.Fatal(err)
		}
		assertPaidCallCounts(t, db, 1, 1, 1)
		logPaidCallBoundary(t, "paid_call_claim_first_input", map[string]any{"claim": paidCallClaimBoundary(input), "asset_state": sourceStateCurrent})
		logPaidCallBoundary(t, "paid_call_claim_first_output", map[string]any{"send": true, "claims_visible_to_deletion": claimsVisibleToDeletion, "final_asset_state": sourceState})
	})
}

func TestPaidCallConcurrentClaimsProduceOneFreshSend(t *testing.T) {
	ctx := context.Background()
	db, _ := openModelGenerationTestStore(t)
	stage, requests := paidCallTestStage(t, db, paidCallPurposeCanary, 1, 1)
	stage, err := createPaidCallStage(ctx, db, stage)
	if err != nil {
		t.Fatal(err)
	}
	input := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
	logPaidCallBoundary(t, "paid_call_concurrent_input", paidCallClaimBoundary(input))

	type result struct {
		decision paidCallDecision
		err      error
	}
	results := make(chan result, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			ready.Done()
			<-start
			decision, err := claimPaidCall(ctx, db, input)
			results <- result{decision: decision, err: err}
		}()
	}
	ready.Wait()
	close(start)

	sends, uncertain := 0, 0
	for range 2 {
		got := <-results
		if got.decision.Send {
			sends++
		}
		if errors.Is(got.err, errModelGenerationUncertain) {
			uncertain++
		} else if got.err != nil {
			t.Fatal(got.err)
		}
	}
	if sends != 1 || uncertain != 1 {
		t.Fatalf("concurrent paid call decisions: sends=%d uncertain=%d", sends, uncertain)
	}
	assertPaidCallCounts(t, db, 1, 1, 1)
	logPaidCallBoundary(t, "paid_call_concurrent_output", map[string]int{"fresh_sends": sends, "stopped_uncertain": uncertain, "claims": 1})
}

func TestPaidCallRestartNeverCreatesAnAutomaticSecondSend(t *testing.T) {
	tests := []struct {
		name      string
		raw       *model.RawResult
		complete  bool
		wantReuse bool
	}{
		{name: "claim committed before send"},
		{name: "retained response", raw: &model.RawResult{Response: []byte(`{"response":"synthetic card","done":true}`), Status: "200 OK", StatusCode: 200, TransmissionStarted: true}},
		{name: "retained failure", raw: &model.RawResult{Failure: []byte("synthetic retained timeout"), TransmissionStarted: true}},
		{name: "completed generation", raw: &model.RawResult{Response: []byte(`{"response":"synthetic card","done":true}`), Status: "200 OK", StatusCode: 200, TransmissionStarted: true}, complete: true, wantReuse: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db, assetID := openModelGenerationTestStore(t)
			stage, requests := paidCallTestStage(t, db, paidCallPurposeBackfill, 1, 1)
			stage, err := createPaidCallStage(ctx, db, stage)
			if err != nil {
				t.Fatal(err)
			}
			input := paidCallClaimForItem(stage, stage.Items[0], requests[stage.Items[0].ItemID])
			logPaidCallBoundary(t, "paid_call_restart_input", map[string]any{"case": test.name, "claim": paidCallClaimBoundary(input)})
			first, err := claimPaidCall(ctx, db, input)
			if err != nil || !first.Send {
				t.Fatalf("first decision = %#v, %v", first, err)
			}
			if test.raw != nil {
				if err := retainModelGenerationResult(ctx, db, first.GenerationID, *test.raw, input.ClaimedAt.Add(time.Minute)); err != nil {
					t.Fatal(err)
				}
			}
			if test.complete {
				if err := db.WithTx(ctx, func(tx *sql.Tx) error {
					return completeModelGeneration(ctx, tx, first.GenerationID, assetID, input.ClaimedAt.Add(2*time.Minute))
				}); err != nil {
					t.Fatal(err)
				}
			}
			input.ClaimedAt = input.ClaimedAt.Add(time.Hour)
			restarted, restartErr := claimPaidCall(ctx, db, input)
			if restarted.Send {
				t.Fatalf("restart authorised another send: %#v", restarted)
			}
			switch {
			case test.raw == nil:
				if !errors.Is(restartErr, errModelGenerationUncertain) {
					t.Fatalf("uncertain restart error = %v", restartErr)
				}
			case test.wantReuse:
				if restartErr != nil || !restarted.Call.Reused || restarted.Call.Retained != nil {
					t.Fatalf("completed restart = %#v, %v", restarted, restartErr)
				}
			default:
				if restartErr != nil || restarted.Call.Retained == nil {
					t.Fatalf("retained restart = %#v, %v", restarted, restartErr)
				}
			}
			assertPaidCallCounts(t, db, 1, 1, 1)
			logPaidCallBoundary(t, "paid_call_restart_output", map[string]any{"case": test.name, "send": restarted.Send, "reused": restarted.Call.Reused, "retained": restarted.Call.Retained != nil, "claim_count": 1})
		})
	}
}
