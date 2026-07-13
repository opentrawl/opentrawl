package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
)

func TestFixtureCardUsesCanonicalGenerationParserWriterAndRestart(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	seedFixtureCardAsset(t, ctx, db, "asset:fixture")
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor("asset:fixture")
	fixture := fixtureProviderResponse(t)
	executionID := fixtureExecutionIdentity(t, prepared, classifier)
	fixtureBytes := fixtureWireBytes(t, fixture)
	prepareCalls := 0
	first, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) { prepareCalls++; return prepared, nil }, classifier, fixtureBytes, time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if prepareCalls != 1 || first.Reused {
		t.Fatalf("first execution prepare=%d reused=%v", prepareCalls, first.Reused)
	}
	if first.Summary != "Synthetic harbour at dusk." || first.OCR != "FERRY 12" || len(first.Uncertainties) != 2 || first.VenuePlausibility.Verdict != "plausible" {
		t.Fatalf("canonical card fields = %+v", first)
	}
	var storedInput, storedRequest, storedResponse []byte
	var observations, paidClaims int
	if err := db.DB().QueryRowContext(ctx, `select c.card_input, g.request_body, a.response_body from card_execution c join model_generation g on g.id=c.generation_id join model_generation_attempt a on a.generation_id=g.id where c.asset_id=?`, "asset:fixture").Scan(&storedInput, &storedRequest, &storedResponse); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(storedInput, first.Input.Bytes) || !bytes.Equal(storedRequest, first.Request.Body()) || !bytes.Equal(storedResponse, fixture.Response) || bytes.Equal(storedInput, storedRequest) {
		t.Fatal("raw persisted boundaries differ or request is only CardInput bytes")
	}
	if err := db.DB().QueryRowContext(ctx, `select count(*) from model_observation where generation_id=(select generation_id from card_execution where asset_id=?)`, "asset:fixture").Scan(&observations); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `select count(*) from paid_call_claim`).Scan(&paidClaims); err != nil {
		t.Fatal(err)
	}
	if observations != 5 || paidClaims != 0 {
		t.Fatalf("canonical observations=%d paid claims=%d", observations, paidClaims)
	}
	var changesBefore, changesAfter int
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&changesBefore)
	second, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) {
		t.Fatal("restart rebuilt CardInput")
		return fixtureCardPreparation{}, nil
	}, modelClassifier{}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&changesAfter)
	if !second.Reused || changesAfter != changesBefore || second.ParserVersion != modelParserVersion || second.PromptVersion != modelPromptVersion || second.OCR != first.OCR || len(second.Uncertainties) != 2 || second.VenuePlausibility.Verdict != "plausible" || second.Custody.SourceId != "source:fixture" || second.Custody.AssetId != "asset:fixture" || second.Custody.ImmutableOriginalResourceId != "resource:fixture" || second.Custody.MetadataRecordId != "metadata:fixture" || second.Custody.MetadataProjectionId != "projection:fixture" || second.Custody.FullCurrentProofSha256 != prepared.Artifacts.FullCurrent.ProofSHA256 || len(second.Custody.Evidence) != 1 || second.Custody.Evidence[0].ProviderIdentity != "synthetic-provider" || second.Custody.Evidence[0].Operation != "synthetic-nearby" || second.Custody.Evidence[0].RawResponseSha256 != prepared.Evidence[0].RawResponseSHA256 {
		t.Fatalf("restart result=%+v changes=%d/%d", second, changesBefore, changesAfter)
	}
	changedPrepareCalls := 0
	if _, err := executeFixtureCard(ctx, db, executionID+"-changed", func() (fixtureCardPreparation, error) { changedPrepareCalls++; return prepared, nil }, classifier, fixtureBytes, time.Now()); err == nil || changedPrepareCalls != 1 {
		t.Fatalf("changed identity err=%v prepare=%d", err, changedPrepareCalls)
	}
	var changesAfterMismatch int
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&changesAfterMismatch)
	if changesAfterMismatch != changesAfter {
		t.Fatalf("changed identity wrote %d rows", changesAfterMismatch-changesAfter)
	}
	secondClassifier, err := newModelClassifier("fixture-model-v2", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	secondClassifier.promptVersion = modelPromptVersion + "-fixture-v2"
	secondExecutionID := fixtureExecutionIdentity(t, prepared, secondClassifier)
	if secondExecutionID == executionID {
		t.Fatal("changed request and prompt kept the execution identity")
	}
	third, err := executeFixtureCard(ctx, db, secondExecutionID, func() (fixtureCardPreparation, error) { return prepared, nil }, secondClassifier, fixtureBytes, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if third.Input.ID != first.Input.ID || third.Request.Model() == first.Request.Model() {
		t.Fatalf("second execution=%+v", third)
	}
	firstAgain, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) {
		t.Fatal("first execution reopened through preparation")
		return fixtureCardPreparation{}, nil
	}, modelClassifier{}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	secondAgain, err := executeFixtureCard(ctx, db, secondExecutionID, func() (fixtureCardPreparation, error) {
		t.Fatal("second execution reopened through preparation")
		return fixtureCardPreparation{}, nil
	}, modelClassifier{}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var executionCount int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from card_execution where asset_id=? and card_input_id=?`, "asset:fixture", first.Input.ID).Scan(&executionCount); err != nil {
		t.Fatal(err)
	}
	if !firstAgain.Reused || !secondAgain.Reused || executionCount != 2 || firstAgain.Request.Model() == secondAgain.Request.Model() {
		t.Fatalf("coexisting executions first=%+v second=%+v count=%d", firstAgain, secondAgain, executionCount)
	}
	t.Logf("RAW CardInput protobuf:\n%s", prototext.Format(first.Input.Input))
	t.Logf("RAW rendered provider request:\n%s", first.Request.Body())
	t.Logf("RAW fixture response protobuf:\n%s", prototext.Format(fixture))
	t.Logf("RAW stored card: summary=%q description=%q ocr=%q uncertainties=%q", second.Summary, second.Description, second.OCR, second.Uncertainties)
}

func TestFixtureCardExecutionIdentityCoversInputRequestPromptAndParser(t *testing.T) {
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor("asset:identity")
	input, err := cardinput.Build(prepared.Source, prepared.Artifacts, prepared.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := classifier.buildRequestFromBytes(prepared.Classify, prepared.CurrentStill, prepared.MIMEType, imagemetadata.Projection{Lines: prepared.Source.Metadata.ProjectionLines})
	if err != nil {
		t.Fatal(err)
	}
	providerRequest, err := classifier.client.Render(request)
	if err != nil {
		t.Fatal(err)
	}
	base := fixtureCardExecutionID(prepared.Source.AssetID, input.ID, providerRequest, modelPromptVersion, modelParserVersion)
	changedRequest, err := model.RestoreProviderRequest(providerRequest.Route(), providerRequest.Model(), append(providerRequest.Body(), ' '))
	if err != nil {
		t.Fatal(err)
	}
	identities := []string{
		fixtureCardExecutionID(prepared.Source.AssetID, input.ID+"-changed", providerRequest, modelPromptVersion, modelParserVersion),
		fixtureCardExecutionID(prepared.Source.AssetID, input.ID, changedRequest, modelPromptVersion, modelParserVersion),
		fixtureCardExecutionID(prepared.Source.AssetID, input.ID, providerRequest, modelPromptVersion+"-changed", modelParserVersion),
		fixtureCardExecutionID(prepared.Source.AssetID, input.ID, providerRequest, modelPromptVersion, modelParserVersion+"-changed"),
	}
	for index, identity := range identities {
		if identity == base {
			t.Fatalf("identity mutation %d did not change execution id", index)
		}
	}
}

func TestFixtureCardIncompleteInputWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	seedFixtureCardAsset(t, ctx, db, "asset:bad")
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor("asset:bad")
	executionID := fixtureExecutionIdentity(t, prepared, classifier)
	prepared.Artifacts.Metadata.RecordID = ""
	var before, after int
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&before)
	if _, err := executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) { return prepared, nil }, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), time.Now()); err == nil {
		t.Fatal("incomplete input succeeded")
	}
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&after)
	if before != after {
		t.Fatalf("incomplete input wrote %d rows", after-before)
	}
}

func TestFixtureCardMismatchedPlaceBoundaryWritesNothing(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*fixtureCardPreparation)
		wantErr string
	}{
		{name: "omitted place", mutate: func(prepared *fixtureCardPreparation) { prepared.Classify.Place = nil }, wantErr: "checked place evidence identities differ"},
		{name: "omitted identity", mutate: func(prepared *fixtureCardPreparation) {
			prepared.Classify.Place.EvidenceRawResponseSHA256 = nil
		}, wantErr: "checked place evidence identities differ"},
		{name: "wrong identity", mutate: func(prepared *fixtureCardPreparation) {
			prepared.Classify.Place.EvidenceRawResponseSHA256[0] = strings.Repeat("0", 64)
		}, wantErr: "checked place evidence identities differ"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			db := fixtureCardStore(t, ctx)
			defer db.Close()
			assetID := "asset:place-mismatch:" + test.name
			seedFixtureCardAsset(t, ctx, db, assetID)
			classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
			if err != nil {
				t.Fatal(err)
			}
			prepared := fixtureCardPreparationFor(assetID)
			test.mutate(&prepared)
			executionID := fixtureExecutionIdentity(t, prepared, classifier)
			var before, after int
			_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&before)
			_, err = executeFixtureCard(ctx, db, executionID, func() (fixtureCardPreparation, error) {
				return prepared, nil
			}, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), time.Now())
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("place mismatch error = %v", err)
			}
			_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&after)
			if before != after {
				t.Fatalf("place mismatch wrote %d rows", after-before)
			}
		})
	}
}

func TestFixturePlaceEvidenceIdentityPreservesOrder(t *testing.T) {
	first := strings.Repeat("a", 64)
	second := strings.Repeat("b", 64)
	records := []place.EvidenceRecord{{RawResponseSHA256: first}, {RawResponseSHA256: second}}
	prompt := classifyInput{Place: &classifyPlaceContext{EvidenceRawResponseSHA256: []string{second, first}}}
	if err := validateFixturePlaceEvidenceIdentity(records, prompt); err == nil {
		t.Fatal("reordered checked place evidence identities succeeded")
	}
}

func TestFixtureCardUnsafeEvidenceWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	seedFixtureCardAsset(t, ctx, db, "asset:unsafe")
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor("asset:unsafe")
	accuracy := 4.0
	prepared.Source.Location = &cardinput.LocationFact{Latitude: 52.0, Longitude: 4.0, HorizontalAccuracyMeters: &accuracy}
	prepared.Source.RequiredPlaceOperations = []string{"synthetic-reverse"}
	digest := func(value string) string { sum := sha256.Sum256([]byte(value)); return hex.EncodeToString(sum[:]) }
	prepared.Evidence = []place.EvidenceRecord{{Input: place.Input{AssetID: "asset:unsafe", TakenAt: prepared.Source.CaptureTime, Location: place.Coordinate{Latitude: 52.0, Longitude: 4.0}, AccuracyMeters: 4.0}, ProviderIdentity: "synthetic", Operation: "synthetic-reverse", CoordinateVariant: "source", ParserVersion: "v1", PreAuthRequestSHA256: digest("request"), RawResponseSHA256: digest("response"), HTTPStatus: 200, Address: &place.Address{Locality: "Example"}, CompletionState: "complete", StopReason: "billing_signal"}}
	var before, after int
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&before)
	if _, err := executeFixtureCard(ctx, db, "card_execution:unsafe", func() (fixtureCardPreparation, error) { return prepared, nil }, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), time.Now()); err == nil {
		t.Fatal("unsafe evidence succeeded")
	}
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&after)
	if before != after {
		t.Fatalf("unsafe evidence wrote %d rows", after-before)
	}
}

func TestFixtureCardMismatchedSourceWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := fixtureCardStore(t, ctx)
	defer db.Close()
	seedFixtureCardAsset(t, ctx, db, "asset:source-mismatch")
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	prepared := fixtureCardPreparationFor("asset:source-mismatch")
	prepared.Source.SourceID = "source:other"
	var before, after int
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&before)
	if _, err := executeFixtureCard(ctx, db, "card_execution:source-mismatch", func() (fixtureCardPreparation, error) { return prepared, nil }, classifier, fixtureWireBytes(t, fixtureProviderResponse(t)), time.Now()); err == nil {
		t.Fatal("mismatched source succeeded")
	}
	_ = db.DB().QueryRowContext(ctx, `select total_changes()`).Scan(&after)
	if before != after {
		t.Fatalf("mismatched source wrote %d rows", after-before)
	}
}

func fixtureCardStore(t *testing.T, ctx context.Context) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: filepath.Join(t.TempDir(), "photos.db"), Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `insert into source_library(id,library_path,snapshot_path,snapshot_created_at,photos_version,metadata_json) values('source:fixture','fixture','fixture','2026-07-13T00:00:00Z','fixture','{}')`); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedFixtureCardAsset(t *testing.T, ctx context.Context, db *store.Store, assetID string) {
	t.Helper()
	_, err := db.DB().ExecContext(ctx, `insert into asset(id,local_identifier,media_type,media_subtypes,creation_date,modification_date,added_date,timezone_name,width,height,duration_seconds,favorite,hidden,burst_identifier,represents_burst,camera_make,camera_model,lens_model,source_library_id,metadata_json) values(?,?, 'image','', '2026-07-13T09:00:00Z','','','UTC',2,2,0,0,0,'',0,'Example','Camera','Lens','source:fixture','{}'); insert into classification_queue(id,asset_id,source_library_id,state,reason,needs_download,updated_at) values(?,?, 'source:fixture','pending','fixture',0,'2026-07-13T09:00:00Z')`, assetID, assetID, "queue:"+assetID, assetID)
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureCardPreparationFor(assetID string) fixtureCardPreparation {
	current := []byte("synthetic-current-still")
	digest := func(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
	accuracy := 4.0
	original := cardinput.ImmutableOriginalFact{ResourceType: "photo", UTI: "public.jpeg", Filename: "synthetic.jpg", Availability: "local", SizeBytes: 3, SHA256: digest([]byte("original"))}
	metadata := cardinput.MetadataFact{RecordSHA256: digest([]byte("metadata")), ProjectionSHA256: digest([]byte("projection")), ProjectionLines: []string{"Camera: Example Camera"}}
	full := cardinput.FullCurrentFact{Role: "full_current", MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, SizeBytes: int64(len(current)), SHA256: digest(current)}
	source := cardinput.SourceFacts{AssetID: assetID, SourceID: "source:fixture", CaptureTime: "2026-07-13T09:00:00Z", MediaType: "image", PixelWidth: 2, PixelHeight: 2, ImmutableOriginal: original, Metadata: metadata, FullCurrent: full, Location: &cardinput.LocationFact{Latitude: 52.0, Longitude: 4.0, HorizontalAccuracyMeters: &accuracy}, RequiredPlaceOperations: []string{"synthetic-nearby"}}
	artifacts := cardinput.CheckedArtifacts{ImmutableOriginal: cardinput.CheckedImmutableOriginal{Fact: original, ResourceID: "resource:fixture"}, Metadata: cardinput.CheckedMetadata{Fact: metadata, RecordID: "metadata:fixture", ProjectionID: "projection:fixture"}, FullCurrent: cardinput.CheckedFullCurrent{Fact: full, ProofSHA256: digest([]byte("proof"))}}
	evidence := []place.EvidenceRecord{{Input: place.Input{AssetID: assetID, TakenAt: source.CaptureTime, Location: place.Coordinate{Latitude: 52.0, Longitude: 4.0}, AccuracyMeters: 4.0}, ProviderIdentity: "synthetic-provider", Operation: "synthetic-nearby", CoordinateVariant: "source", ParserVersion: "v1", PreAuthRequestSHA256: digest([]byte("place-request")), RawResponseSHA256: digest([]byte("place-response")), HTTPStatus: 200, Candidates: []place.EvidenceCandidate{{ProviderIndex: 0, ProviderID: "terminal-1", Name: "Example Ferry Terminal", Categories: []string{"transport"}, DistanceM: 12, Source: "synthetic-provider"}}, CompletionState: "complete"}}
	providerCandidate := place.POICandidate{Name: "Example Ferry Terminal", Category: "transport", DistanceM: 12, Tier: place.TierVenueCandidate, Source: "synthetic-provider"}
	classify := classifyInput{QueueID: "queue:" + assetID, AssetID: assetID, SourceLibraryID: "source:fixture", MediaType: "image", CreationDate: source.CaptureTime, Width: 2, Height: 2, CameraMake: "Example", CameraModel: "Camera", LensModel: "Lens", HasLocation: true, Latitude: 52.0, Longitude: 4.0, AccuracyMeters: 4.0, Place: &classifyPlaceContext{Result: place.Result{Input: place.Input{AssetID: assetID, TakenAt: source.CaptureTime, Location: place.Coordinate{Latitude: 52.0, Longitude: 4.0}, AccuracyMeters: 4.0}, Provider: "synthetic-provider", POIStatus: place.POIStatusFound, POITotal: 1, POICandidates: []place.POICandidate{providerCandidate}}, CacheStatus: "fixture", EvidenceRawResponseSHA256: []string{evidence[0].RawResponseSHA256}}}
	return fixtureCardPreparation{Source: source, Artifacts: artifacts, Evidence: evidence, Classify: classify, CurrentStill: current, MIMEType: "image/jpeg"}
}

func fixtureProviderResponse(t *testing.T) *cardwire.FixtureResponse {
	t.Helper()
	prose := "Summary\nSynthetic harbour at dusk.\nDescription\nA synthetic ferry crosses a calm harbour under an orange sky.\nVenue plausibility\ncandidate_id: venue_candidate_1\nverdict: plausible\nreason: The terminal is near the synthetic coordinate.\nOCR\nFERRY 12\nUncertainty\n- The distant shoreline is indistinct.\n- The ferry name is not readable."
	body, err := json.Marshal(map[string]any{"response": prose, "done": true})
	if err != nil {
		t.Fatal(err)
	}
	return &cardwire.FixtureResponse{Response: body, Status: "200 OK", StatusCode: 200, ProviderRequestId: "fixture-request", TransmissionStarted: true}
}

func fixtureExecutionIdentity(t *testing.T, prepared fixtureCardPreparation, classifier modelClassifier) string {
	t.Helper()
	input, err := cardinput.Build(prepared.Source, prepared.Artifacts, prepared.Evidence)
	if err != nil {
		t.Fatal(err)
	}
	request, _, err := classifier.buildRequestFromBytes(prepared.Classify, prepared.CurrentStill, prepared.MIMEType, imagemetadata.Projection{Lines: prepared.Source.Metadata.ProjectionLines})
	if err != nil {
		t.Fatal(err)
	}
	providerRequest, err := classifier.client.Render(request)
	if err != nil {
		t.Fatal(err)
	}
	return fixtureCardExecutionID(prepared.Source.AssetID, input.ID, providerRequest, classifier.promptVersion, modelParserVersion)
}

func fixtureWireBytes(t *testing.T, fixture *cardwire.FixtureResponse) []byte {
	t.Helper()
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
