//go:build darwin

package archive

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorDarwinSourceChecksUseExplicitLibrary(t *testing.T) {
	t.Parallel()
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("fixture sqlite canary"), 0o644); err != nil {
		t.Fatal(err)
	}

	checks := sourceStoreChecks(libraryPath)
	if check := doctorCheck(checks, "source_store"); check.State != "ok" {
		t.Fatalf("source_store check = %#v", check)
	}
	if check := doctorCheck(checks, "full_disk_access"); check.State != "ok" {
		t.Fatalf("full_disk_access check = %#v", check)
	}
}
