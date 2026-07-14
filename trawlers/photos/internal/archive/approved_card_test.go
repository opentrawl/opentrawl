package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"google.golang.org/protobuf/proto"
)

func TestApprovedCardBundleBindsPreparedBytesAndRetainedSuccessResumes(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	assetID := "asset:approved"
	seedFixtureCardAsset(t, ctx, db, assetID)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor(assetID)
	item, err := prepareCard(preparedCard{
		source: prepared.Source, artifacts: prepared.Artifacts, evidence: prepared.Evidence,
		classify: prepared.Classify, currentStill: prepared.CurrentStill,
		classifier: classifier,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := renderPreparedCardRequest(prepared.Source, prepared.Artifacts, prepared.Evidence, prepared.CurrentStill, classifier)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := restorePreparedCardRequest(item, classifier.client)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]preparedCardRequest{"fresh": fresh, "restored": restored} {
		custodyBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(value.Custody)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(custodyBytes, value.CustodyBytes) || !bytes.Equal(value.CustodyBytes, item.GetCustody()) || value.Custody.GetCardInputSha256() == "" || value.Custody.GetRequestSha256() != value.RequestSHA256 {
			t.Fatalf("%s in-memory custody does not match retained bytes", name)
		}
	}
	var providerEnvelope struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(item.GetRequestBody(), &providerEnvelope); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(providerEnvelope.Prompt, `"provider_index":  0`) && !strings.Contains(providerEnvelope.Prompt, `"provider_index": 0`) {
		t.Fatalf("complete CardInput ProtoJSON omitted provider index 0:\n%s", providerEnvelope.Prompt)
	}
	unsupported := proto.Clone(item).(*cardwire.ApprovedCardItem)
	unsupported.PromptVersion = "photo-card-v3.0"
	if _, err := restorePreparedCardRequest(unsupported, classifier.client); err == nil {
		t.Fatal("unregistered retained prompt version restored")
	}
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bundle)
	transport := &approvedCardFixtureTransport{request: fixtureProviderResponse(t).Response}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	if err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), now, transport); err != nil {
		t.Fatal(err)
	}
	if transport.sends != 1 || !bytes.Equal(transport.body, item.GetRequestBody()) {
		t.Fatalf("sends=%d body=%q", transport.sends, transport.body)
	}
	var cards, complete, claims int
	if err := db.DB().QueryRowContext(ctx, "select count(*) from card_execution where completed_at <> ''").Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, "select count(*) from model_generation_asset where completed_at is not null").Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, "select count(*) from paid_call_claim").Scan(&claims); err != nil {
		t.Fatal(err)
	}
	if cards != 1 || complete != 1 || claims != 1 {
		t.Fatalf("cards=%d complete=%d claims=%d", cards, complete, claims)
	}
	if err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), now.Add(time.Hour), transport); err != nil {
		t.Fatal(err)
	}
	if transport.sends != 1 {
		t.Fatal("completed card sent again")
	}
}

func TestApprovedCardRejectsApprovalMismatchBeforeLedger(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	seedFixtureCardAsset(t, ctx, db, "asset:approved-mismatch")
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor("asset:approved-mismatch")
	item, err := prepareCard(preparedCard{source: prepared.Source, artifacts: prepared.Artifacts, evidence: prepared.Evidence, classify: prepared.Classify, currentStill: prepared.CurrentStill, classifier: classifier}, 1)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	transport := &approvedCardFixtureTransport{}
	if err := SendApprovedCardBundle(ctx, db, bundle, strings.Repeat("0", 64), time.Now(), transport); err == nil {
		t.Fatal("mismatched approval was accepted")
	}
	var stages int
	if err := db.DB().QueryRowContext(ctx, "select count(*) from paid_call_stage").Scan(&stages); err != nil {
		t.Fatal(err)
	}
	if stages != 0 || transport.sends != 0 {
		t.Fatalf("stages=%d sends=%d", stages, transport.sends)
	}
}

func TestApprovedCardRetainsParseFailureWithoutCompleting(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	assetID := "asset:approved-parse-failure"
	seedFixtureCardAsset(t, ctx, db, assetID)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor(assetID)
	item, err := prepareCard(preparedCard{source: prepared.Source, artifacts: prepared.Artifacts, evidence: prepared.Evidence, classify: prepared.Classify, currentStill: prepared.CurrentStill, classifier: classifier}, 1)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bundle)
	transport := &approvedCardFixtureTransport{request: []byte(`{"response":"not a card","done":true}`)}
	if err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), time.Now(), transport); err == nil {
		t.Fatal("parse failure completed an approved card")
	}
	var attempts, complete, cards, parseFailures int
	if err := db.DB().QueryRowContext(ctx, "select count(*) from model_generation_attempt where retained_at is not null").Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, "select count(*) from model_generation_asset where completed_at is not null").Scan(&complete); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, "select count(*) from card_execution where completed_at <> ''").Scan(&cards); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, "select count(*) from model_generation_asset where parse_failure is not null").Scan(&parseFailures); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || complete != 0 || cards != 0 || parseFailures != 1 {
		t.Fatalf("attempts=%d complete=%d cards=%d parse_failures=%d", attempts, complete, cards, parseFailures)
	}
}

func TestApprovedCardRejectsCardInputMutationEvenWithNewOuterDigests(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	assetID := "asset:approved-custody-mismatch"
	seedFixtureCardAsset(t, ctx, db, assetID)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor(assetID)
	item, err := prepareCard(preparedCard{source: prepared.Source, artifacts: prepared.Artifacts, evidence: prepared.Evidence, classify: prepared.Classify, currentStill: prepared.CurrentStill, classifier: classifier}, 1)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeApprovedCardBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	input := new(cardwire.CardInput)
	if err := proto.Unmarshal(decoded.Items[0].CardInput, input); err != nil {
		t.Fatal(err)
	}
	input.Metadata.RecordSha256 = strings.Repeat("a", 64)
	decoded.Items[0].CardInput, err = proto.MarshalOptions{Deterministic: true}.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	inputDigest := sha256.Sum256(decoded.Items[0].CardInput)
	decoded.Items[0].CardInputId = "card_input:" + hex.EncodeToString(inputDigest[:])
	mutated, err := proto.MarshalOptions{Deterministic: true}.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(mutated)
	if err := SendApprovedCardBundle(ctx, db, mutated, hex.EncodeToString(digest[:]), time.Now(), &approvedCardFixtureTransport{}); err == nil {
		t.Fatal("changed CardInput crossed the custody boundary")
	}
	var stages int
	if err := db.DB().QueryRowContext(ctx, "select count(*) from paid_call_stage").Scan(&stages); err != nil {
		t.Fatal(err)
	}
	if stages != 0 {
		t.Fatalf("stages=%d", stages)
	}
}

type approvedCardFixtureTransport struct {
	body    []byte
	request []byte
	sends   int
}

func (t *approvedCardFixtureTransport) ValidateRequest(request model.ProviderRequest) error {
	if request.Route() != "https://models.example.com/api/generate" || request.Model() != "fixture-model" {
		return errors.New("unexpected fixture request")
	}
	return nil
}

func (t *approvedCardFixtureTransport) Send(_ context.Context, request model.ProviderRequest) (model.RawResult, error) {
	t.sends++
	t.body = request.Body()
	return model.RawResult{Response: bytes.Clone(t.request), Status: "200 OK", StatusCode: 200, TransmissionStarted: true}, nil
}
