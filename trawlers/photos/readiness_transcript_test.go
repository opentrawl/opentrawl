//go:build darwin

package photoscrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

type photosReadinessInput struct {
	Home         string
	StateRoot    string
	LibraryPath  string
	DatabasePath string
	ConfigPath   string
	ArchivePath  string
	SourceState  string
	CrawlerArgs  []string
}

type photosPathMetadata struct {
	Path        string
	Mode        uint32
	Permissions uint32
	UID         uint32
	GID         uint32
	Size        int64
	Directory   bool
	StatError   string
}

func TestPhotosSourceReadinessTranscripts(t *testing.T) {
	tests := []struct {
		name             string
		setup            func(*testing.T, string)
		wantSourceState  string
		wantAccessState  string
		wantRemedyPhrase string
	}{
		{
			name:            "readable source store",
			setup:           createReadablePhotosLibrary,
			wantSourceState: "ok",
			wantAccessState: "ok",
		},
		{
			name:             "full disk access denied",
			setup:            makeDeniedPhotosLibrary,
			wantSourceState:  "ok",
			wantAccessState:  "fail",
			wantRemedyPhrase: "full disk access",
		},
		{
			name:             "source store unavailable",
			setup:            func(*testing.T, string) {},
			wantSourceState:  "missing",
			wantAccessState:  "missing",
			wantRemedyPhrase: "library_path",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home, libraryPath := preparePhotosReadinessFixture(t, test.setup)
			input := photosReadinessInput{
				Home:         home,
				StateRoot:    filepath.Join(home, ".opentrawl"),
				LibraryPath:  libraryPath,
				DatabasePath: filepath.Join(libraryPath, "database", "Photos.sqlite"),
				ConfigPath:   filepath.Join(home, ".opentrawl", "photos", "config.toml"),
				ArchivePath:  filepath.Join(home, ".opentrawl", "photos", "photos.db"),
				SourceState:  test.name,
				CrawlerArgs:  []string{"photos", "doctor", "--json"},
			}
			readPhotosReadinessInput(t, input)
			typed := readTypedPhotosDoctor(t, input)
			rendered, renderedOutput := renderPhotosDoctor(t, typed)
			if !reflect.DeepEqual(rendered, typed) {
				t.Fatalf("rendered doctor = %#v, typed doctor = %#v", rendered, typed)
			}

			sourceCheck := photosCheckByID(rendered.Checks, "source_store")
			if sourceCheck.State != test.wantSourceState {
				t.Fatalf("source_store state = %q, want %q", sourceCheck.State, test.wantSourceState)
			}
			accessCheck := photosCheckByID(rendered.Checks, "full_disk_access")
			if accessCheck.State != test.wantAccessState {
				t.Fatalf("full_disk_access state = %q, want %q", accessCheck.State, test.wantAccessState)
			}
			if test.wantRemedyPhrase != "" && !strings.Contains(strings.ToLower(accessCheck.Remedy), test.wantRemedyPhrase) {
				t.Fatalf("full_disk_access remedy = %q, want %q", accessCheck.Remedy, test.wantRemedyPhrase)
			}
			assertNoPhotoKitReadinessRemedy(t, typed, renderedOutput)
		})
	}
}

func preparePhotosReadinessFixture(t *testing.T, setup func(*testing.T, string)) (string, string) {
	t.Helper()
	home := t.TempDir()
	libraryPath := filepath.Join(home, "Pictures", "Synthetic Photos Library.photoslibrary")
	setup(t, libraryPath)
	configPath := filepath.Join(home, ".opentrawl", "photos", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("library_path = %q\n", libraryPath)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, libraryPath
}

func makeDeniedPhotosLibrary(t *testing.T, libraryPath string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode-zero fixture")
	}
	createReadablePhotosLibrary(t, libraryPath)
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

func createReadablePhotosLibrary(t *testing.T, libraryPath string) {
	t.Helper()
	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("synthetic sqlite canary"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readPhotosReadinessInput(t *testing.T, input photosReadinessInput) {
	t.Helper()
	configBytes, err := os.ReadFile(input.ConfigPath)
	if err != nil {
		t.Fatalf("read synthetic config: %v", err)
	}
	databaseBytes, databaseErr := os.ReadFile(input.DatabasePath)
	fileMetadata := readPhotosPathMetadata(input.DatabasePath)
	databaseDirectoryMetadata := readPhotosPathMetadata(filepath.Dir(input.DatabasePath))
	libraryDirectoryMetadata := readPhotosPathMetadata(input.LibraryPath)
	libraryParentMetadata := readPhotosPathMetadata(filepath.Dir(input.LibraryPath))
	t.Logf("raw typed input effective_uid=%d home=%q state_root=%q library_path=%q database_path=%q config_path=%q archive_path=%q source_state=%q crawler_args=%q config_bytes=%q database_bytes=%q database_read_error=%v file_metadata=%#v database_directory_metadata=%#v library_directory_metadata=%#v library_parent_metadata=%#v", os.Geteuid(), input.Home, input.StateRoot, input.LibraryPath, input.DatabasePath, input.ConfigPath, input.ArchivePath, input.SourceState, input.CrawlerArgs, configBytes, databaseBytes, databaseErr, fileMetadata, databaseDirectoryMetadata, libraryDirectoryMetadata, libraryParentMetadata)
}

func readPhotosPathMetadata(path string) photosPathMetadata {
	metadata := photosPathMetadata{Path: path}
	info, err := os.Lstat(path)
	if err != nil {
		metadata.StatError = err.Error()
		return metadata
	}
	metadata.Mode = uint32(info.Mode())
	metadata.Permissions = uint32(info.Mode().Perm())
	metadata.Size = info.Size()
	metadata.Directory = info.IsDir()
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		metadata.UID = stat.Uid
		metadata.GID = stat.Gid
	}
	return metadata
}

func readTypedPhotosDoctor(t *testing.T, input photosReadinessInput) trawlkit.Doctor {
	t.Helper()
	t.Setenv("HOME", input.Home)
	source := New()
	source.cfg.LibraryPath = input.LibraryPath
	paths := trawlkit.Paths{
		Archive: input.ArchivePath,
		Config:  input.ConfigPath,
		Logs:    filepath.Join(input.StateRoot, "photos", "logs"),
	}
	doctor, err := source.Doctor(context.Background(), &trawlkit.Request{Paths: paths, Out: io.Discard, Format: output.JSON})
	if err != nil {
		t.Fatalf("typed doctor: %v", err)
	}
	t.Logf("typed doctor value=%#v", *doctor)
	return *doctor
}

func renderPhotosDoctor(t *testing.T, doctor trawlkit.Doctor) (trawlkit.Doctor, []byte) {
	t.Helper()
	var rendered bytes.Buffer
	renderErr := output.Write(&rendered, output.JSON, "doctor", &doctor)
	raw := append([]byte(nil), rendered.Bytes()...)
	t.Logf("rendered doctor output=%q render_error=%v", raw, renderErr)
	if renderErr != nil {
		t.Fatalf("render doctor: %v", renderErr)
	}
	if len(raw) == 0 {
		t.Fatal("rendered doctor output is empty")
	}
	var result trawlkit.Doctor
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("rendered doctor JSON: %v", err)
	}
	return result, raw
}

func photosCheckByID(checks []trawlkit.Check, id string) trawlkit.Check {
	for _, check := range checks {
		if check.ID == id {
			return check
		}
	}
	return trawlkit.Check{ID: id}
}

func assertNoPhotoKitReadinessRemedy(t *testing.T, typed trawlkit.Doctor, rendered []byte) {
	t.Helper()
	typedText := strings.ToLower(fmt.Sprintf("%#v", typed))
	combined := bytes.ToLower(append([]byte(typedText), rendered...))
	if bytes.Contains(combined, []byte("photokit")) || bytes.Contains(combined, []byte("authorization")) {
		t.Fatalf("typed or rendered source readiness boundary mentions PhotoKit authorisation: %s", combined)
	}
}
