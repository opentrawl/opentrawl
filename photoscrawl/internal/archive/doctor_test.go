package archive

import (
	"context"
	"path/filepath"
	"testing"
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
		t.Fatalf("archive check before init = %#v", check)
	}

	if _, err := Init(ctx, paths); err != nil {
		t.Fatal(err)
	}
	result, err = Doctor(ctx, paths, DoctorOptions{LibraryPath: filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")})
	if err != nil {
		t.Fatal(err)
	}
	if check := doctorCheck(result.Checks, "archive"); check.State != "ok" || check.Remedy != "" {
		t.Fatalf("archive check after init = %#v", check)
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
