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

func TestDoctorDarwinSourceChecksReportReadinessStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		setup          func(*testing.T, string)
		sourceState    string
		fullDiskState  string
		fullDiskMsg    string
		fullDiskRemedy string
	}{
		{
			name:          "readable",
			setup:         writeDoctorSQLiteFixture,
			sourceState:   "ok",
			fullDiskState: "ok",
		},
		{
			name:           "full disk access denied",
			setup:          writeDeniedDoctorSQLiteFixture,
			sourceState:    "ok",
			fullDiskState:  "fail",
			fullDiskMsg:    "cannot read the Photos database",
			fullDiskRemedy: fullDiskAccessRemedy,
		},
		{
			name:           "source store unavailable",
			setup:          func(*testing.T, string) {},
			sourceState:    "missing",
			fullDiskState:  "missing",
			fullDiskMsg:    "Photos database not found",
			fullDiskRemedy: libraryPathRemedy,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
			test.setup(t, libraryPath)

			checks := sourceStoreChecks(libraryPath)
			if check := doctorCheck(checks, "source_store"); check.State != test.sourceState {
				t.Fatalf("source_store check = %#v, want state %q", check, test.sourceState)
			}
			check := doctorCheck(checks, "full_disk_access")
			if check.State != test.fullDiskState || check.Message != test.fullDiskMsg || check.Remedy != test.fullDiskRemedy {
				t.Fatalf("full_disk_access check = %#v, want state=%q message=%q remedy=%q", check, test.fullDiskState, test.fullDiskMsg, test.fullDiskRemedy)
			}
		})
	}
}

func writeDoctorSQLiteFixture(t *testing.T, libraryPath string) {
	t.Helper()
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("synthetic sqlite canary"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDeniedDoctorSQLiteFixture(t *testing.T, libraryPath string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-zero fixture")
	}
	writeDoctorSQLiteFixture(t, libraryPath)
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if err := os.Chmod(dbPath, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dbPath, 0o644) })
	file, err := os.Open(dbPath)
	if err == nil {
		_ = file.Close()
		t.Fatal("synthetic denied database remained readable")
	}
	if !os.IsPermission(err) {
		t.Fatalf("opening denied synthetic database = %v, want permission denied", err)
	}
}
