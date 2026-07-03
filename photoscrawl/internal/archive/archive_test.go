package archive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/openclaw/photoscrawl/internal/photos"
)

func TestSyncInitializesArchiveAndStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	paths := Paths{DataDir: root, Database: filepath.Join(root, "photos.sqlite")}
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
	if len(after.Counts) != 1 || after.Counts[0].ID != "photos" || after.Counts[0].Value != 0 {
		t.Fatalf("status counts after sync = %#v", after.Counts)
	}
	if after.Freshness == nil || after.Freshness.LastSync == "" {
		t.Fatalf("freshness after sync = %#v", after.Freshness)
	}
}
