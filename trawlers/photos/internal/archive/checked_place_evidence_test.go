package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
)

func TestLocatedCheckedEvidenceBuildsOneCardInputAcrossPreparations(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer func() { _ = db.Close() }()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:located", "current", "image", `{"present":true}`)
	if _, err := db.ExecContext(ctx, `alter table location_observation add column id integer`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into location_observation(id,asset_id,latitude,longitude,horizontal_accuracy) values(1,'asset:located',52.36,4.89,8)`); err != nil {
		t.Fatal(err)
	}
	cacheDir, input, operations := seedLocatedCheckedArtifacts(t, ctx, db)
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}

	audit, err := inspectCardInput(ctx, db, true, CardInputAuditInspectOptions{
		CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"},
		CacheDir:                       cacheDir, PlaceEvidenceOperations: operations,
	}, classifier, input.AssetID)
	if err != nil || audit.StopReason != "" {
		t.Fatalf("audit=%#v error=%v", audit, err)
	}
	auditBytes, err := base64.StdEncoding.DecodeString(audit.CardInputWire)
	if err != nil {
		t.Fatal(err)
	}

	classified, _, err := prepareClassifyCardRequestFromCache(ctx, Paths{CacheDir: cacheDir}, input, classifier, operations)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := prepareApprovedCardFromArchive(ctx, db, ApprovedCardPrepareOptions{
		CacheDir: cacheDir, SourceLibraryID: "source:synthetic", PlaceEvidenceOperations: operations,
	}, classifier, input.AssetID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(auditBytes) != string(classified.Input.Bytes) || string(auditBytes) != string(approved.CardInput) {
		t.Fatalf("CardInput differs: audit=%x classify=%x approved=%x", auditBytes, classified.Input.Bytes, approved.CardInput)
	}
	restarted, _, err := prepareClassifyCardRequestFromCache(ctx, Paths{CacheDir: cacheDir}, input, classifier, operations)
	if err != nil || string(restarted.Input.Bytes) != string(classified.Input.Bytes) {
		t.Fatalf("restart CardInput=%x error=%v", restarted.Input.Bytes, err)
	}
}

func TestLocatedEvidenceStopsBeforeCardPreparationWithoutCheckedCache(t *testing.T) {
	ctx, db := cardInputAuditTestDB(t)
	defer func() { _ = db.Close() }()
	seedCardInputAuditSnapshot(t, ctx, db, "complete")
	seedCardInputAuditAsset(t, ctx, db, "asset:located", "current", "image", `{"present":true}`)
	if _, err := db.ExecContext(ctx, `alter table location_observation add column id integer`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `insert into location_observation(id,asset_id,latitude,longitude,horizontal_accuracy) values(1,'asset:located',52.36,4.89,8)`); err != nil {
		t.Fatal(err)
	}
	cacheDir, input, operations := seedLocatedCheckedArtifacts(t, ctx, db)
	if err := os.RemoveAll(filepath.Join(cacheDir, "place-evidence")); err != nil {
		t.Fatal(err)
	}
	classifier, err := newModelClassifier("fixture-model", "https://models.example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectCardInput(ctx, db, true, CardInputAuditInspectOptions{
		CardInputAuditInventoryOptions: CardInputAuditInventoryOptions{SourceLibraryID: "source:synthetic"}, CacheDir: cacheDir,
		PlaceEvidenceOperations: operations,
	}, classifier, input.AssetID)
	if err != nil || inspection.StopReason != cardInputAuditStopMissingPlace || inspection.RenderedRequest != nil {
		t.Fatalf("inspection=%#v error=%v", inspection, err)
	}
	if _, _, err := prepareClassifyCardRequestFromCache(ctx, Paths{CacheDir: cacheDir}, input, classifier, operations); !errors.Is(err, errCardInputNotReady) {
		t.Fatalf("classify preparation error=%v", err)
	}
	if _, err := prepareApprovedCardFromArchive(ctx, db, ApprovedCardPrepareOptions{
		CacheDir: cacheDir, SourceLibraryID: "source:synthetic", PlaceEvidenceOperations: operations,
	}, classifier, input.AssetID, 1); err == nil || err.Error() != cardInputAuditStopMissingPlace {
		t.Fatalf("approved-card preparation error=%v", err)
	}
}

func seedLocatedCheckedArtifacts(t *testing.T, ctx context.Context, db *sql.DB) (string, classifyInput, []place.CheckedOperation) {
	t.Helper()
	root := t.TempDir()
	cacheDir := filepath.Join(root, "checked-cache")
	if err := os.Mkdir(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(root, "original.jpg")
	originalBytes := []byte("synthetic located original")
	if err := os.WriteFile(originalPath, originalBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	originalSHA := sha256.Sum256(originalBytes)
	if _, err := db.ExecContext(ctx, `insert into asset_resource values('resource:located','asset:located','local_original','public.jpeg','original.jpg',?,?,?,1,0)`, originalPath, len(originalBytes), hex.EncodeToString(originalSHA[:])); err != nil {
		t.Fatal(err)
	}
	input, err := loadCardInputAuditInput(ctx, db, "source:synthetic", "asset:located")
	if err != nil {
		t.Fatal(err)
	}
	oldExtract := extractImageMetadata
	extractImageMetadata = func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf(`{"extractor_version":"imageio-v1","original_sha256":%q,"container":{"type":"dictionary","dictionary":{"Make":{"type":"string","string":"Example"}}},"images":[{"index":0,"properties":{"type":"dictionary","dictionary":{}}}]}`, hex.EncodeToString(originalSHA[:]))), nil
	}
	t.Cleanup(func() { extractImageMetadata = oldExtract })
	metadataStore, err := imagemetadata.NewStore(filepath.Join(cacheDir, "image-metadata"), extractImageMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadataStore.Load(ctx, originalPath, hex.EncodeToString(originalSHA[:])); err != nil {
		t.Fatal(err)
	}
	request, err := input.currentStillRequest()
	if err != nil {
		t.Fatal(err)
	}
	currentBytes := syntheticImageBytes(t)
	resolver, err := photos.NewCurrentStillResolver(filepath.Join(cacheDir, "originals"), func(_ context.Context, _ photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		if err := os.WriteFile(destination, currentBytes, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		sum := sha256.Sum256(currentBytes)
		return photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 2, PixelHeight: 2, Size: int64(len(currentBytes)), SHA256: hex.EncodeToString(sum[:])}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Lease != nil {
		resolved.Lease.Close()
	}
	operations := []place.CheckedOperation{{
		ProviderIdentity: "synthetic-reverse", Operation: "reverse", CoordinateVariant: "source-coordinate",
		SelectionPolicy: place.SelectionPolicy{RequestedLimit: 1, BoundedReverse: true},
		Parser:          syntheticArchiveCheckedEvidenceParser,
	}}
	writeLocatedCheckedEvidence(t, cacheDir, input, operations[0])
	return cacheDir, input, operations
}

func writeLocatedCheckedEvidence(t *testing.T, cacheDir string, input classifyInput, operation place.CheckedOperation) {
	t.Helper()
	cacheRoot := filepath.Join(cacheDir, "place-evidence")
	if err := os.Mkdir(cacheRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	evidenceInput := place.Input{AssetID: input.AssetID, TakenAt: input.CreationDate, Location: place.Coordinate{Latitude: input.Latitude, Longitude: input.Longitude}, AccuracyMeters: input.AccuracyMeters}
	request := []byte("GET /synthetic-reverse")
	response := []byte(`{"synthetic":"place"}`)
	headers := []byte("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n")
	identity := place.CheckedEvidenceCacheIdentity(evidenceInput, operation, request)
	dir := filepath.Join(cacheRoot, identity)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}
	record := place.EvidenceRecord{
		Input: evidenceInput, ProviderIdentity: operation.ProviderIdentity, Operation: operation.Operation,
		SelectionPolicy: operation.SelectionPolicy, CoordinateVariant: operation.CoordinateVariant,
		ParserVersion: "photos-place-evidence-v3", PreAuthRequestFile: "request.raw", PreAuthRequestSHA256: digest(request),
		RawResponseFile: "response.raw", RawResponseSHA256: digest(response), RawHeadersFile: "headers.raw", RawHeadersSHA256: digest(headers), HTTPStatus: 200,
		Address:         &place.Address{Formatted: "Synthetic Place", Source: "synthetic"},
		Candidates:      []place.EvidenceCandidate{{ProviderIndex: 0, ProviderID: "synthetic-place", Name: "Synthetic Place", Source: "synthetic"}},
		CompletionState: "complete", CacheIdentity: identity,
		StartedAt: "2026-01-01T00:00:00Z", CompletedAt: "2026-01-01T00:00:00Z",
	}
	record.SelectionPolicy.LimitReached = true
	record.SelectionPolicy.MoreResultsNotRequested = true
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{"record.json": data, "request.raw": request, "response.raw": response, "headers.raw": headers} {
		if err := os.WriteFile(filepath.Join(dir, name), value, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func syntheticArchiveCheckedEvidenceParser(_ []byte, _ int, _ place.Input) (*place.Address, []place.EvidenceCandidate, error) {
	return &place.Address{Formatted: "Synthetic Place", Source: "synthetic"}, []place.EvidenceCandidate{{ProviderIndex: 0, ProviderID: "synthetic-place", Name: "Synthetic Place", Source: "synthetic"}}, nil
}
