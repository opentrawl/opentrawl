package archive

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestSyncInitializesArchiveAndStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	paths := Paths{DataDir: root, Database: filepath.Join(root, "photoscrawl.db")}
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	before, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if before.State != "missing" {
		t.Fatalf("state before sync = %q, want missing", before.State)
	}

	_, err = Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider: fakeProvider{snapshot: photos.LibrarySnapshot{
			Provider:      "fake",
			PhotosVersion: "fixture",
		}},
		Now: fixedClock("2026-05-28T10:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}

	after, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != "empty" {
		t.Fatalf("state after sync = %q, want empty", after.State)
	}
	if len(after.Counts) != 4 ||
		!hasStatusCountValue(after.Counts, "photos", 0) ||
		!hasStatusCountValue(after.Counts, "queued_for_classify", 0) ||
		!hasStatusCountValue(after.Counts, "queued_needs_download", 0) ||
		!hasStatusCountValue(after.Counts, "classification_queue_pending", 0) {
		t.Fatalf("status counts after sync = %#v", after.Counts)
	}
	if after.Freshness == nil || after.Freshness.LastSync == "" {
		t.Fatalf("freshness after sync = %#v", after.Freshness)
	}
}

func TestStatusRejectsIncompatibleArchiveWithoutMigration(t *testing.T) {
	ctx := context.Background()
	paths := Paths{Database: filepath.Join(t.TempDir(), "photos.db")}
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion - 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Status(ctx, paths)
	if !errors.Is(err, ArchiveIncompatibleError{}) {
		t.Fatalf("status error = %v, want incompatible archive", err)
	}

	readStore, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readStore.Close() }()
	version, err := readStore.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion-1 {
		t.Fatalf("schema version after status = %d, want %d", version, SchemaVersion-1)
	}
}
