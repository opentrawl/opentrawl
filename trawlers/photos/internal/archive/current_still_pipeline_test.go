package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func withSyntheticCurrentStill(t *testing.T) {
	t.Helper()
	old := newCurrentStillResolver
	mediaPath := filepath.Join(t.TempDir(), "synthetic-current-still.jpeg")
	newCurrentStillResolver = func(_ string, _ photos.CurrentStillExporter) (currentStillResolver, error) {
		return syntheticCurrentStillResolver{path: mediaPath}, nil
	}
	t.Cleanup(func() { newCurrentStillResolver = old })
}

type syntheticCurrentStillResolver struct{ path string }

func (r syntheticCurrentStillResolver) Resolve(_ context.Context, _ photos.CurrentStillRequest) (photos.CurrentStillResolution, error) {
	data := []byte("synthetic full-resolution current-still fixture")
	if err := os.WriteFile(r.path, data, 0o600); err != nil {
		return photos.CurrentStillResolution{}, err
	}
	digest := sha256.Sum256(data)
	return photos.CurrentStillResolution{Path: r.path, Source: "synthetic_current_still", Fact: photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: 4032, PixelHeight: 3024, Size: int64(len(data)), SHA256: fmt.Sprintf("%x", digest)}}, nil
}

func TestCurrentStillMIMEType(t *testing.T) {
	for _, test := range []struct{ mediaType, want string }{{"public.heic", "image/heic"}, {"public.heif", "image/heif"}, {"public.jpeg", "image/jpeg"}, {"public.png", "image/png"}} {
		if got, err := currentStillMIMEType(test.mediaType); err != nil || got != test.want {
			t.Fatalf("currentStillMIMEType(%q) = %q, %v", test.mediaType, got, err)
		}
	}
	if _, err := currentStillMIMEType(""); !errors.Is(err, errUnknownCurrentStillMIMEType) {
		t.Fatalf("unknown type error = %v", err)
	}
}

func TestCurrentStillRequestCanonicalizesFloatDerivedFraction(t *testing.T) {
	input := classifyInput{
		SourceLibraryID:  "source-library:synthetic",
		LocalIdentifier:  "synthetic-asset",
		ModificationDate: "2023-03-24T05:18:11.797890305Z",
	}
	request, err := input.currentStillRequest()
	if err != nil {
		t.Fatal(err)
	}
	modification, ok := request.Freshness.ExpectedModification()
	if !ok || modification.UnixSeconds != 1679635091 || modification.Microseconds != 797890 {
		t.Fatalf("request freshness = %#v", request.Freshness)
	}
}

func TestCurrentStillRequestUsesOnlyCompleteSnapshotFingerprint(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Synthetic Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	snapshot := firstCardSnapshot(photos.Asset{
		LocalIdentifier: "synthetic-no-modification",
		MediaType:       "image",
		CreationDate:    "2026-07-13T08:00:00Z",
		AddedDate:       "2026-07-13T08:01:00Z",
		TimezoneName:    "UTC",
		Width:           4032,
		Height:          3024,
	})
	result := syncFirstCardSnapshot(t, ctx, paths, libraryPath, snapshot, "2026-07-13T09:00:00Z")

	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	load := func() classifyInput {
		t.Helper()
		var inputs []classifyInput
		if err := db.WithTx(ctx, func(tx *sql.Tx) error {
			var loadErr error
			inputs, loadErr = loadClassifyInputs(ctx, tx, 1, "")
			return loadErr
		}); err != nil {
			t.Fatal(err)
		}
		if len(inputs) != 1 {
			t.Fatalf("classification inputs = %d, want 1", len(inputs))
		}
		return inputs[0]
	}

	input := load()
	if len(input.SourceFingerprint) != sha256.Size*2 || input.SourceFingerprint != strings.ToLower(input.SourceFingerprint) {
		t.Fatalf("complete snapshot fingerprint = %q", input.SourceFingerprint)
	}
	request, err := input.currentStillRequest()
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint, ok := request.Freshness.SourceFingerprint(); !ok || fingerprint != input.SourceFingerprint {
		t.Fatalf("request freshness = %#v", request.Freshness)
	}

	if _, err := db.DB().ExecContext(ctx, `update crawl_snapshot set completeness_state = 'partial' where id = ?`, result.SnapshotID); err != nil {
		t.Fatal(err)
	}
	incomplete := load()
	if incomplete.SourceFingerprint != "" {
		t.Fatalf("incomplete snapshot exposed fingerprint %q", incomplete.SourceFingerprint)
	}
	if _, err := incomplete.currentStillRequest(); err == nil {
		t.Fatal("incomplete snapshot produced current-still freshness")
	}
}

func TestCurrentStillRequestRejectsMalformedSourceFingerprint(t *testing.T) {
	for _, fingerprint := range []string{strings.Repeat("A", sha256.Size*2), " " + strings.Repeat("a", sha256.Size*2)} {
		input := classifyInput{SourceLibraryID: "source-library:synthetic", LocalIdentifier: "synthetic-asset", SourceFingerprint: fingerprint}
		if _, err := input.currentStillRequest(); err == nil {
			t.Fatalf("malformed source fingerprint %q was accepted", fingerprint)
		}
	}
}
