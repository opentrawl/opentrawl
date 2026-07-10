package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

type notesReadinessInput struct {
	name string

	home      string
	stateRoot string
	argv      []string

	sourcePath       string
	sourceExists     bool
	sourceState      string
	sourceMode       os.FileMode
	sourceFixture    []byte
	blockedParent    string
	blockedMode      os.FileMode
	blockedOwnerUID  uint32
	blockedOwnerGID  uint32
	effectiveUID     int
	effectiveGID     int
	archivePath      string
	archiveExists    bool
	archiveState     string
	archiveMode      os.FileMode
	archiveFixture   []byte
	archiveOpenError error
}

type notesReadinessCase struct {
	name          string
	archive       bool
	prepareSource func(t *testing.T, input *notesReadinessInput)
	wantSource    trawlkit.Check
	wantArchive   trawlkit.Check
}

func TestNotesDoctorReadinessTypedBeforeRender(t *testing.T) {
	cases := []notesReadinessCase{
		{
			name:    "readable source store",
			archive: true,
			prepareSource: func(t *testing.T, input *notesReadinessInput) {
				installSyntheticSource(t, input)
			},
			wantSource:  trawlkit.Check{ID: "source_store", State: "ok"},
			wantArchive: trawlkit.Check{ID: "archive", State: "ok"},
		},
		{
			name:    "Full Disk Access denied",
			archive: true,
			prepareSource: func(t *testing.T, input *notesReadinessInput) {
				installSyntheticSource(t, input)
				if os.Geteuid() == 0 {
					t.Skip("root bypasses filesystem permission denial; this case requires a non-root test user")
				}
				blockedDir := filepath.Dir(input.sourcePath)
				if err := os.Chmod(blockedDir, 0); err != nil {
					t.Fatal(err)
				}
				captureBlockedParent(t, input, blockedDir)
				input.sourceState = "permission denied by mode-zero parent"
				t.Cleanup(func() {
					if err := os.Chmod(blockedDir, 0o700); err != nil {
						t.Errorf("restore source directory permissions: %v", err)
					}
				})
			},
			wantSource: trawlkit.Check{
				ID:      "source_store",
				State:   "fail",
				Message: "cannot read the Apple Notes database",
				Remedy:  "grant Full Disk Access, then run trawl notes sync; or run trawl notes sync --store PATH",
			},
			wantArchive: trawlkit.Check{ID: "archive", State: "ok"},
		},
		{
			name:    "source store unavailable",
			archive: true,
			prepareSource: func(t *testing.T, input *notesReadinessInput) {
				if err := os.MkdirAll(filepath.Dir(input.sourcePath), 0o700); err != nil {
					t.Fatal(err)
				}
				input.sourceState = "missing synthetic NoteStore.sqlite"
			},
			wantSource: trawlkit.Check{
				ID:      "source_store",
				State:   "fail",
				Message: "cannot read the Apple Notes database",
				Remedy:  "grant Full Disk Access, then run trawl notes sync; or run trawl notes sync --store PATH",
			},
			wantArchive: trawlkit.Check{ID: "archive", State: "ok"},
		},
		{
			name: "missing archive with readable source",
			prepareSource: func(t *testing.T, input *notesReadinessInput) {
				installSyntheticSource(t, input)
			},
			wantSource:  trawlkit.Check{ID: "source_store", State: "ok"},
			wantArchive: trawlkit.Check{ID: "archive", State: "fail", Message: "archive has not been synced", Remedy: "run trawl notes sync"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := newReadinessInput(t, tc.name)
			t.Setenv("HOME", input.home)
			tc.prepareSource(t, &input)
			if tc.archive {
				installSyntheticArchive(t, &input)
			}
			readReadinessInput(t, input)

			typed, typedErr := callTypedDoctor(t, input)
			if typedErr != nil {
				t.Fatalf("typed doctor error = %v", typedErr)
			}
			if typed == nil {
				t.Fatal("typed doctor is nil")
			}
			t.Logf("typed doctor before rendering: %#v", *typed)

			var rendered bytes.Buffer
			renderErr := output.Write(&rendered, output.JSON, "doctor", typed)
			rawRendered := append([]byte(nil), rendered.Bytes()...)
			t.Logf("rendered doctor output: %q; rendering error: %v", rawRendered, renderErr)
			if renderErr != nil {
				t.Fatalf("render doctor: %v", renderErr)
			}
			if len(rawRendered) == 0 {
				t.Fatal("rendered doctor output is empty")
			}

			var decoded trawlkit.Doctor
			if err := json.Unmarshal(rawRendered, &decoded); err != nil {
				t.Fatalf("rendered doctor JSON: %v\nraw output: %s", err, rawRendered)
			}
			if !reflect.DeepEqual(decoded, *typed) {
				t.Fatalf("decoded doctor = %#v, typed doctor = %#v", decoded, *typed)
			}
			if got := checkByID(decoded.Checks, "source_store"); got != tc.wantSource {
				t.Fatalf("source_store check = %#v, want %#v", got, tc.wantSource)
			}
			if got := checkByID(decoded.Checks, "archive"); got != tc.wantArchive {
				t.Fatalf("archive check = %#v, want %#v", got, tc.wantArchive)
			}
		})
	}
}

func newReadinessInput(t *testing.T, name string) notesReadinessInput {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	return notesReadinessInput{
		name:           name,
		home:           home,
		stateRoot:      filepath.Join(home, ".opentrawl"),
		argv:           []string{"notes", "doctor", "--json"},
		sourcePath:     filepath.Join(home, "Library", "Group Containers", "group.com.apple.notes", "NoteStore.sqlite"),
		sourceState:    "not installed",
		archivePath:    filepath.Join(home, ".opentrawl", "notes", "notes.db"),
		archiveState:   "missing",
		archiveFixture: nil,
	}
}

func installSyntheticSource(t *testing.T, input *notesReadinessInput) {
	t.Helper()
	fixture := newFixture(t, false)
	fixture.close()
	data, err := os.ReadFile(fixture.path())
	if err != nil {
		t.Fatal(err)
	}
	input.sourceFixture = append([]byte(nil), data...)
	if err := os.MkdirAll(filepath.Dir(input.sourcePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input.sourcePath, input.sourceFixture, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(input.sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	input.sourceExists = true
	input.sourceMode = info.Mode()
	input.sourceState = "readable synthetic NoteStore.sqlite"
}

func installSyntheticArchive(t *testing.T, input *notesReadinessInput) {
	t.Helper()
	st, err := archive.Open(context.Background(), input.archivePath)
	if err != nil {
		input.archiveOpenError = err
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		input.archiveOpenError = err
		t.Fatal(err)
	}
	data, err := os.ReadFile(input.archivePath)
	if err != nil {
		input.archiveOpenError = err
		t.Fatal(err)
	}
	input.archiveFixture = append([]byte(nil), data...)
	info, err := os.Stat(input.archivePath)
	if err != nil {
		input.archiveOpenError = err
		t.Fatal(err)
	}
	input.archiveExists = true
	input.archiveMode = info.Mode()
	input.archiveState = "readable synthetic OpenTrawl archive"
}

func captureBlockedParent(t *testing.T, input *notesReadinessInput, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("blocked parent stat type = %T, want *syscall.Stat_t", info.Sys())
	}
	input.blockedParent = path
	input.blockedMode = info.Mode()
	input.blockedOwnerUID = owner.Uid
	input.blockedOwnerGID = owner.Gid
	input.effectiveUID = os.Geteuid()
	input.effectiveGID = os.Getegid()
	if input.blockedMode.Perm() != 0 {
		t.Fatalf("blocked parent mode = %s, want no permission bits", input.blockedMode)
	}
}

func readReadinessInput(t *testing.T, input notesReadinessInput) {
	t.Helper()
	if input.sourceExists && len(input.sourceFixture) == 0 {
		t.Fatal("source fixture bytes are empty")
	}
	if input.archiveExists && len(input.archiveFixture) == 0 {
		t.Fatal("archive fixture bytes are empty")
	}
	t.Logf("synthetic input name=%q HOME=%q state_root=%q argv=%q source_path=%q source_exists=%t source_state=%q source_mode=%s blocked_parent=%q blocked_mode=%s blocked_owner_uid=%d blocked_owner_gid=%d effective_uid=%d effective_gid=%d archive_path=%q archive_exists=%t archive_state=%q archive_mode=%s archive_open_error=%v source_fixture=%q archive_fixture=%q", input.name, input.home, input.stateRoot, input.argv, input.sourcePath, input.sourceExists, input.sourceState, input.sourceMode, input.blockedParent, input.blockedMode, input.blockedOwnerUID, input.blockedOwnerGID, input.effectiveUID, input.effectiveGID, input.archivePath, input.archiveExists, input.archiveState, input.archiveMode, input.archiveOpenError, input.sourceFixture, input.archiveFixture)
}

func callTypedDoctor(t *testing.T, input notesReadinessInput) (*trawlkit.Doctor, error) {
	t.Helper()
	paths := trawlkit.Paths{
		Archive: input.archivePath,
		Config:  filepath.Join(input.stateRoot, "notes", "config.toml"),
		Logs:    filepath.Join(input.stateRoot, "notes", "logs"),
	}
	req := &trawlkit.Request{Paths: paths, Format: output.JSON, Out: io.Discard}
	if input.archiveExists {
		st, err := ckstore.OpenReadOnly(context.Background(), input.archivePath)
		if err != nil {
			return nil, err
		}
		req.Store = st
		defer func() { _ = st.Close() }()
	}
	return New().Doctor(context.Background(), req)
}

func checkByID(checks []trawlkit.Check, id string) trawlkit.Check {
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	return trawlkit.Check{}
}
