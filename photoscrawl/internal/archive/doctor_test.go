package archive

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/openclaw/photoscrawl/internal/photos"
)

func TestDoctorReportsArchiveState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := t.TempDir()
	paths := Paths{DataDir: root, Database: filepath.Join(root, "photos.sqlite")}

	result, err := Doctor(ctx, paths, DoctorOptions{LibraryPath: filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")})
	if err != nil {
		t.Fatal(err)
	}
	if check := doctorCheck(result.Checks, "archive"); check.State != "missing" || check.Remedy != archiveRemedy {
		t.Fatalf("archive check before sync = %#v", check)
	}

	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider: fakeProvider{snapshot: photos.LibrarySnapshot{
			Provider:      "fake",
			PhotosVersion: "fixture",
		}},
		Now: fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	result, err = Doctor(ctx, paths, DoctorOptions{LibraryPath: filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")})
	if err != nil {
		t.Fatal(err)
	}
	if check := doctorCheck(result.Checks, "archive"); check.State != "ok" || check.Remedy != "" {
		t.Fatalf("archive check after sync = %#v", check)
	}
}

func doctorCheck(checks []DoctorCheck, id string) DoctorCheck {
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	return DoctorCheck{}
}
