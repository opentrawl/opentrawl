package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"google.golang.org/protobuf/proto"
)

func TestApprovedCardBundleBindsPreparedBytesAndRetainedSuccessResumes(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer func() { _ = db.Close() }()
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
		Messages []struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(item.GetRequestBody(), &providerEnvelope); err != nil {
		t.Fatal(err)
	}
	if len(providerEnvelope.Messages) != 1 || len(providerEnvelope.Messages[0].Content) == 0 || !strings.Contains(providerEnvelope.Messages[0].Content[0].Text, `"provider_index":  0`) && !strings.Contains(providerEnvelope.Messages[0].Content[0].Text, `"provider_index": 0`) {
		t.Fatalf("complete CardInput ProtoJSON omitted provider index 0: %#v", providerEnvelope)
	}
	unsupported := proto.Clone(item).(*cardwire.ApprovedCardItem)
	unsupported.PromptVersion = "photo-card-v3.0"
	if _, err := restorePreparedCardRequest(unsupported, classifier.client); err == nil {
		t.Fatal("unregistered retained prompt version restored")
	}
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, "synthetic-models", "SYNTHETIC_MODEL_KEY", []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bundle)
	transport := &approvedCardFixtureTransport{request: fixtureProviderResponse(t).Response}
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	first, err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), "SYNTHETIC_MODEL_KEY", now, transport)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].State != "created" {
		t.Fatalf("first send = %#v", first)
	}
	opened, err := OpenWithStore(ctx, db, AssetRef(assetID))
	if err != nil {
		t.Fatal(err)
	}
	if opened.Ref != AssetRef(assetID) || strings.TrimSpace(opened.Model.Summary) == "" || opened.Model.ModelID != "fixture-model" {
		t.Fatalf("opened created card = %#v", opened)
	}
	if transport.sends != 1 || !bytes.Equal(transport.body, item.GetRequestBody()) {
		t.Fatalf("sends=%d body=%q", transport.sends, transport.body)
	}
	replayed, found, err := CompletedApprovedCardBundle(ctx, db.Path(), bundle)
	if err != nil {
		t.Fatal(err)
	}
	if !found || len(replayed.Items) != 1 || replayed.Items[0].State != "already_created" || transport.sends != 1 {
		t.Fatalf("read-only completed replay = %#v found=%v sends=%d", replayed, found, transport.sends)
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
	second, err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), "SYNTHETIC_MODEL_KEY", now.Add(time.Hour), transport)
	if err != nil {
		t.Fatal(err)
	}
	if transport.sends != 1 || len(second.Items) != 1 || second.Items[0].State != "already_created" {
		t.Fatalf("completed repeat = %#v sends=%d", second, transport.sends)
	}
}

func TestApprovedCardRejectsApprovalMismatchBeforeLedger(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer func() { _ = db.Close() }()
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
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, "synthetic-models", "SYNTHETIC_MODEL_KEY", []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	transport := &approvedCardFixtureTransport{}
	if _, err := SendApprovedCardBundle(ctx, db, bundle, strings.Repeat("0", 64), "SYNTHETIC_MODEL_KEY", time.Now(), transport); err == nil {
		t.Fatal("mismatched approval was accepted")
	}
	digest := sha256.Sum256(bundle)
	if _, err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), "OTHER_SYNTHETIC_MODEL_KEY", time.Now(), transport); err == nil {
		t.Fatal("mismatched credential reference was accepted")
	}
	var stages int
	if err := db.DB().QueryRowContext(ctx, "select count(*) from paid_call_stage").Scan(&stages); err != nil {
		t.Fatal(err)
	}
	if stages != 0 || transport.sends != 0 {
		t.Fatalf("stages=%d sends=%d", stages, transport.sends)
	}
}

func TestApprovedCardReviewRejectsBackfillAndBindsProviderIdentity(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer func() { _ = db.Close() }()
	assetID := "asset:approved-review"
	seedFixtureCardAsset(t, ctx, db, assetID)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor(assetID)
	item, err := prepareCard(preparedCard{
		source: prepared.Source, artifacts: prepared.Artifacts, evidence: prepared.Evidence,
		classify: prepared.Classify, currentStill: prepared.CurrentStill, classifier: classifier,
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, "Synthetic Models", "SYNTHETIC_MODEL_KEY", []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	review, err := ReviewApprovedCardBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if review.ProviderIdentity != "Synthetic Models" || review.Endpoint != "https://models.example.com/v1/chat/completions" || review.CredentialEnv != "SYNTHETIC_MODEL_KEY" {
		t.Fatalf("review = %#v", review)
	}
	decoded, err := decodeApprovedCardBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Purpose = string(paidCallPurposeBackfill)
	backfill, err := proto.MarshalOptions{Deterministic: true}.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReviewApprovedCardBundle(backfill); err == nil {
		t.Fatal("normal review accepted a backfill bundle")
	}
	originalApproval, err := ApprovedCardApprovalDigest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	decoded.Purpose = string(paidCallPurposeCanary)
	decoded.ProviderIdentity = "Other Synthetic Models"
	otherProvider, err := proto.MarshalOptions{Deterministic: true}.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	otherApproval, err := ApprovedCardApprovalDigest(otherProvider)
	if err != nil {
		t.Fatal(err)
	}
	if otherApproval == originalApproval {
		t.Fatal("provider identity did not change the approval")
	}
}

func TestApprovedCardRetainsParseFailureWithoutCompleting(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer func() { _ = db.Close() }()
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
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, "synthetic-models", "SYNTHETIC_MODEL_KEY", []*cardwire.ApprovedCardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bundle)
	transport := &approvedCardFixtureTransport{request: []byte(`{"response":"not a card","done":true}`)}
	if _, err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), "SYNTHETIC_MODEL_KEY", time.Now(), transport); err == nil {
		t.Fatal("parse failure completed an approved card")
	}
	if _, err := SendApprovedCardBundle(ctx, db, bundle, hex.EncodeToString(digest[:]), "SYNTHETIC_MODEL_KEY", time.Now().Add(time.Minute), transport); err == nil {
		t.Fatal("retained parse failure completed on retry")
	}
	if transport.sends != 1 {
		t.Fatalf("retained result sent again: sends=%d", transport.sends)
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
	defer func() { _ = db.Close() }()
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
	bundle, err := marshalApprovedCardBundle(paidCallPurposeCanary, 1, "synthetic-models", "SYNTHETIC_MODEL_KEY", []*cardwire.ApprovedCardItem{item})
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
	if _, err := SendApprovedCardBundle(ctx, db, mutated, hex.EncodeToString(digest[:]), "SYNTHETIC_MODEL_KEY", time.Now(), &approvedCardFixtureTransport{}); err == nil {
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

func TestApprovedCardFreshnessRejectsChangedSourceRouteAndCredentialBeforeLedger(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Synthetic Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "synthetic.jpeg")
	writeSyntheticImage(t, imagePath)
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider: "synthetic", PhotosVersion: "fixture", AuthorizationStatus: "authorized",
		Assets: []photos.Asset{{
			LocalIdentifier: "approved-freshness", MediaType: "image", CreationDate: "2026-07-14T09:00:00Z",
			Width: 2, Height: 2,
			Resources: []photos.Resource{{
				Type: "local_original", UTI: "public.jpeg", OriginalFilename: "synthetic.jpeg",
				LocalPath: imagePath, Availability: "local", AvailableLocally: true,
			}},
		}},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{LibraryPath: libraryPath, Provider: provider, Now: fixedClock("2026-07-14T09:05:00Z")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Classify(ctx, paths, ClassifyOptions{Now: fixedClock("2026-07-14T09:10:00Z")}); err != nil {
		t.Fatal(err)
	}
	prepareCheckedCardInputForModelTest(t, ctx, paths, libraryPath, "approved-freshness")
	assetID := stableID("asset", stableID("source_library", libraryPath), "approved-freshness")
	options := ApprovedCardPrepareOptions{
		ArchivePath: paths.Database, CacheDir: paths.CacheDir, AssetIDs: []string{assetID},
		ProviderIdentity: "Synthetic Models", Model: "synthetic-vision",
		ModelURL: "https://models.example.com/v1", CredentialEnv: "SYNTHETIC_MODEL_KEY",
		Purpose: "canary", CallCap: 1,
	}
	bundle, err := PrepareApprovedCardBundle(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	review, err := ReviewApprovedCardBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if review.PhotoRef != AssetRef(assetID) || review.ProviderIdentity != options.ProviderIdentity || review.Model != options.Model || review.CredentialEnv != options.CredentialEnv || review.CallCap != 1 || review.State != "ready" {
		t.Fatalf("review = %#v", review)
	}
	if err := ValidateApprovedCardBundleFreshness(ctx, bundle, options); err != nil {
		t.Fatal(err)
	}
	movedCurrentStills := paths.OriginalsCacheDir() + "-moved"
	if err := os.Rename(paths.OriginalsCacheDir(), movedCurrentStills); err != nil {
		t.Fatal(err)
	}
	if err := ValidateApprovedCardBundleFreshness(ctx, bundle, options); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("missing current-still error = %v", err)
	}
	if err := os.Rename(movedCurrentStills, paths.OriginalsCacheDir()); err != nil {
		t.Fatal(err)
	}
	changedRoute := options
	changedRoute.ModelURL = "https://other-models.example.com/v1"
	if err := ValidateApprovedCardBundleFreshness(ctx, bundle, changedRoute); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("changed route error = %v", err)
	}
	changedCredential := options
	changedCredential.CredentialEnv = "OTHER_SYNTHETIC_MODEL_KEY"
	if err := ValidateApprovedCardBundleFreshness(ctx, bundle, changedCredential); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("changed credential error = %v", err)
	}
	db := openTestStore(t, ctx, paths)
	if _, err := db.DB().ExecContext(ctx, `update asset set favorite = 1 where id = ?`, assetID); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ValidateApprovedCardBundleFreshness(ctx, bundle, options); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("changed source error = %v", err)
	}
	proof := openTestStore(t, ctx, paths)
	defer func() { _ = proof.Close() }()
	for _, table := range []string{"paid_call_stage", "paid_call_claim", "model_generation_attempt"} {
		var count int
		if err := proof.DB().QueryRowContext(ctx, "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows = %d after freshness failures", table, count)
		}
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("synthetic source changed: %v", err)
	}
}

type approvedCardFixtureTransport struct {
	body    []byte
	request []byte
	sends   int
}

func (t *approvedCardFixtureTransport) ValidateRequest(request model.ProviderRequest) error {
	if request.Route() != "https://models.example.com/v1/chat/completions" || request.Model() != "fixture-model" {
		return errors.New("unexpected fixture request")
	}
	return nil
}

func (t *approvedCardFixtureTransport) Send(_ context.Context, request model.ProviderRequest) (model.RawResult, error) {
	t.sends++
	t.body = request.Body()
	return model.RawResult{Response: bytes.Clone(t.request), Status: "200 OK", StatusCode: 200, TransmissionStarted: true}, nil
}
