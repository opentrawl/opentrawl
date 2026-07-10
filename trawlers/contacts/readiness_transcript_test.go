//go:build darwin

package clawdex

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"

	_ "github.com/mattn/go-sqlite3"
)

func TestAppleReadinessTypedBoundary(t *testing.T) {
	tests := []struct {
		name          string
		state         string
		wantState     string
		wantMessage   string
		wantSourceFix string
	}{
		{
			name:          "ready",
			state:         "ready",
			wantState:     "ok",
			wantMessage:   "Apple Contacts source is ready for first import",
			wantSourceFix: "trawl contacts import apple",
		},
		{
			name:          "Full Disk Access required",
			state:         "needs_full_disk_access",
			wantState:     "fail",
			wantMessage:   "Apple Contacts needs Full Disk Access",
			wantSourceFix: "grant Full Disk Access to Trawl or the terminal running it in System Settings > Privacy & Security > Full Disk Access",
		},
		{
			name:        "unavailable",
			state:       "unavailable",
			wantState:   "missing",
			wantMessage: "Apple Contacts source is unavailable",
		},
		{
			name:        "invalid",
			state:       "invalid",
			wantState:   "invalid",
			wantMessage: "Apple Contacts source is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := syntheticAppleDoctorFixture(t, tt.state)
			fixture.log(t)
			if fixture.skipReason != "" {
				t.Skip(fixture.skipReason)
			}

			t.Setenv("HOME", fixture.home)
			request := &trawlkit.Request{
				Paths:  fixture.paths,
				Format: ckoutput.JSON,
				Out:    &bytes.Buffer{},
			}
			typed, typedErr := New().Doctor(t.Context(), request)
			t.Logf("typed doctor value before rendering: doctor=%#v error=%q", typed, errorText(typedErr))
			if typedErr != nil {
				t.Fatalf("typed doctor error = %v", typedErr)
			}
			if typed == nil {
				t.Fatal("typed doctor value is nil")
			}
			typedValue := *typed

			var rendered bytes.Buffer
			renderErr := ckoutput.Write(&rendered, ckoutput.JSON, "doctor", typed)
			t.Logf("raw rendered doctor output: bytes=%q error=%q", rendered.Bytes(), errorText(renderErr))
			if renderErr != nil {
				t.Fatalf("render doctor error = %v", renderErr)
			}
			if len(rendered.Bytes()) == 0 {
				t.Fatal("rendered doctor output is empty")
			}

			var renderedValue trawlkit.Doctor
			if err := json.Unmarshal(rendered.Bytes(), &renderedValue); err != nil {
				t.Fatalf("rendered doctor JSON: %v\n%s", err, rendered.Bytes())
			}
			if !reflect.DeepEqual(typedValue, renderedValue) {
				t.Fatalf("typed and rendered doctor values differ: typed=%#v rendered=%#v", typedValue, renderedValue)
			}
			assertDoctorReadiness(t, typedValue, tt.wantState, tt.wantMessage, tt.wantSourceFix)
		})
	}
}

type appleDoctorFixture struct {
	home          string
	stateRoot     string
	paths         trawlkit.Paths
	sourceDir     string
	sourcePath    string
	sourceExists  bool
	sourceBytes   []byte
	sourceSchema  []string
	sourceMode    string
	archivePath   string
	archiveExists bool
	archiveState  string
	archiveError  string
	archiveBytes  []byte
	skipReason    string
}

func (f appleDoctorFixture) log(t *testing.T) {
	t.Helper()
	t.Logf("raw typed doctor input: HOME=%q state_root=%q crawler_args=%q addressbook_dir=%q addressbook_path=%q source_exists=%t source_mode=%q source_schema=%q source_bytes=%q archive_path=%q archive_exists=%t archive_state=%q archive_error=%q archive_bytes=%q skip_reason=%q", f.home, f.stateRoot, []string{"contacts", "doctor", "--json"}, f.sourceDir, f.sourcePath, f.sourceExists, f.sourceMode, f.sourceSchema, f.sourceBytes, f.archivePath, f.archiveExists, f.archiveState, f.archiveError, f.archiveBytes, f.skipReason)
}

func syntheticAppleDoctorFixture(t *testing.T, state string) appleDoctorFixture {
	t.Helper()
	home := t.TempDir()
	stateRoot := filepath.Join(home, ".opentrawl")
	sourceDir := filepath.Join(home, "Library", "Application Support", "AddressBook")
	sourcePath := filepath.Join(sourceDir, "AddressBook-v22.abcddb")
	fixture := appleDoctorFixture{
		home:        home,
		stateRoot:   stateRoot,
		paths:       trawlkit.Paths{Archive: filepath.Join(stateRoot, "contacts", "contacts.db"), Config: filepath.Join(stateRoot, "contacts", "config.toml"), Logs: filepath.Join(stateRoot, "contacts", "logs")},
		sourceDir:   sourceDir,
		sourcePath:  sourcePath,
		archivePath: filepath.Join(stateRoot, "contacts", "contacts.db"),
	}

	switch state {
	case "ready":
		fixture.sourceSchema = writeSyntheticAddressBook(t, sourcePath)
	case "needs_full_disk_access":
		if os.Geteuid() == 0 {
			fixture.skipReason = "root bypasses POSIX permission fixtures; refusing to claim Full Disk Access denial"
			return fixture
		}
		if err := os.MkdirAll(sourceDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(sourceDir, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(sourceDir, 0o700) })
	case "invalid":
		if err := os.MkdirAll(sourceDir, 0o700); err != nil {
			t.Fatal(err)
		}
		fixture.sourceBytes = []byte("synthetic invalid Apple source")
		if err := os.WriteFile(sourcePath, fixture.sourceBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	case "unavailable":
	default:
		t.Fatalf("unknown synthetic Apple state %q", state)
	}

	if info, err := os.Stat(sourceDir); err == nil {
		fixture.sourceMode = fmt.Sprintf("%#o", info.Mode().Perm())
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if info, err := os.Stat(sourcePath); err == nil {
		fixture.sourceExists = true
		fixture.sourceMode = fmt.Sprintf("%#o", info.Mode().Perm())
	} else if !os.IsNotExist(err) && !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	fixture.recordArchive(t)
	return fixture
}

func (f *appleDoctorFixture) recordArchive(t *testing.T) {
	t.Helper()
	_, err := os.Stat(f.archivePath)
	switch {
	case err == nil:
		f.archiveExists = true
		f.archiveState = "present"
		f.archiveBytes, err = os.ReadFile(f.archivePath)
		if err != nil {
			t.Fatal(err)
		}
	case os.IsNotExist(err):
		f.archiveState = "absent"
		f.archiveError = err.Error()
	default:
		f.archiveState = "unreadable"
		f.archiveError = err.Error()
	}
}

func writeSyntheticAddressBook(t *testing.T, path string) []string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`create table Z_PRIMARYKEY (Z_ENT integer, Z_NAME varchar, Z_SUPER integer)`,
		`insert into Z_PRIMARYKEY (Z_ENT, Z_NAME, Z_SUPER) values (22, 'ABCDContact', 17)`,
		`create table ZABCDRECORD (Z_PK integer primary key, Z_ENT integer, ZFIRSTNAME varchar, ZLASTNAME varchar, ZORGANIZATION varchar, ZUNIQUEID varchar)`,
		`create table ZABCDPHONENUMBER (Z_PK integer primary key, ZOWNER integer, ZFULLNUMBER varchar)`,
		`create table ZABCDEMAILADDRESS (Z_PK integer primary key, ZOWNER integer, ZADDRESS varchar)`,
		`create table ZABCDPOSTALADDRESS (Z_PK integer primary key, ZOWNER integer, ZSTREET varchar)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return statements
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func assertDoctorReadiness(t *testing.T, doctor trawlkit.Doctor, wantState, wantMessage, wantSourceFix string) {
	t.Helper()
	if len(doctor.Checks) != 3 {
		t.Fatalf("doctor checks = %#v, want apple source, archive and schema", doctor.Checks)
	}
	if got := []string{doctor.Checks[0].ID, doctor.Checks[1].ID, doctor.Checks[2].ID}; !equalStrings(got, []string{"apple_source", "archive", "schema"}) {
		t.Fatalf("doctor check order = %v", got)
	}
	source, archive, schema := doctor.Checks[0], doctor.Checks[1], doctor.Checks[2]
	if source.State != wantState || source.Message != wantMessage || source.Remedy != wantSourceFix {
		t.Fatalf("Apple source check = %#v", source)
	}
	wantArchiveFix := ""
	if wantSourceFix == "trawl contacts import apple" {
		wantArchiveFix = wantSourceFix
	}
	if archive.Remedy != wantArchiveFix || schema.Remedy != wantArchiveFix {
		t.Fatalf("archive remedies = %q, %q; want %q", archive.Remedy, schema.Remedy, wantArchiveFix)
	}
}
