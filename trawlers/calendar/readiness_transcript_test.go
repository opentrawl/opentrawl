package calcrawl

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestCalendarReadinessTypedTranscripts(t *testing.T) {
	cases := []struct {
		name        string
		fixture     func(*testing.T) calendarReadinessFixture
		statusState string
	}{
		{name: "readable", fixture: readableCalendarReadinessFixture, statusState: "ok"},
		{name: "Full Disk Access denied", fixture: deniedCalendarReadinessFixture, statusState: "missing"},
		{name: "unavailable", fixture: unavailableCalendarReadinessFixture, statusState: "missing"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := test.fixture(t)
			request, archive := calendarReadinessRequest(t, fixture)
			if archive != nil {
				defer func() { _ = archive.Close() }()
			}

			logCalendarInputs(t, fixture, []string{"calendar", "status"})
			status := captureCalendarStatus(t, request)
			if status.State != test.statusState {
				t.Fatalf("status state = %q, want %q", status.State, test.statusState)
			}
			if test.statusState == "ok" && status.Summary != "Recently synced." {
				t.Fatalf("status summary = %q, want recently synced", status.Summary)
			}
			if test.statusState == "missing" && status.Summary != "Archive has not been synced." {
				t.Fatalf("status summary = %q, want archive missing", status.Summary)
			}
			statusOutput := renderCalendarCommand(t, fixture, "calendar", "status")
			assertCalendarStatusRender(t, status, statusOutput)
		})
	}
}

type calendarReadinessFixture struct {
	home       string
	stateRoot  string
	sourcePath string
	paths      trawlkit.Paths
	stateSetup string
}

type calendarSyntheticInput struct {
	path    string
	present bool
	kind    string
	mode    fs.FileMode
	size    int64
}

func readableCalendarReadinessFixture(t *testing.T) calendarReadinessFixture {
	stateRoot := setupCalendarFixture(t)
	home := filepath.Dir(stateRoot)
	sourcePath := calendarSourcePath(home)
	now := time.Now().UTC()
	if err := os.Chtimes(sourcePath, now, now); err != nil {
		t.Fatal(err)
	}
	paths := calendarReadinessPaths(stateRoot)
	writeStore, err := ckstore.Open(context.Background(), ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	_, syncErr := New().Sync(context.Background(), &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	})
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if syncErr != nil {
		t.Fatal(syncErr)
	}
	return calendarReadinessFixture{
		home:       home,
		stateRoot:  stateRoot,
		sourcePath: sourcePath,
		paths:      paths,
		stateSetup: "set Calendar source mtime to current UTC, then sync the synthetic source into the synthetic archive",
	}
}

func deniedCalendarReadinessFixture(t *testing.T) calendarReadinessFixture {
	stateRoot := setupCalendarFixture(t)
	home := filepath.Dir(stateRoot)
	sourcePath := calendarSourcePath(home)
	if os.Geteuid() == 0 {
		if err := os.Remove(sourcePath); err != nil {
			t.Fatal(err)
		}
		// A non-regular path fails before opening SQLite, including for root.
		if err := os.Mkdir(sourcePath, 0); err != nil {
			t.Fatal(err)
		}
	} else if err := os.Chmod(sourcePath, 0); err != nil {
		t.Fatal(err)
	}
	paths := calendarReadinessPaths(stateRoot)
	return calendarReadinessFixture{
		home:       home,
		stateRoot:  stateRoot,
		sourcePath: sourcePath,
		paths:      paths,
		stateSetup: "replace the source file with a mode-zero directory when root, otherwise chmod the source file to mode zero",
	}
}

func unavailableCalendarReadinessFixture(t *testing.T) calendarReadinessFixture {
	stateRoot := setupCalendarFixture(t)
	home := filepath.Dir(stateRoot)
	sourcePath := calendarSourcePath(home)
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	return calendarReadinessFixture{
		home:       home,
		stateRoot:  stateRoot,
		sourcePath: sourcePath,
		paths:      calendarReadinessPaths(stateRoot),
		stateSetup: "remove the Calendar source database",
	}
}

func calendarReadinessPaths(stateRoot string) trawlkit.Paths {
	return trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "calendar", "calendar.db"),
		Config:  filepath.Join(stateRoot, "calendar", "config.toml"),
		Logs:    filepath.Join(stateRoot, "calendar", "logs"),
	}
}

func calendarReadinessRequest(t *testing.T, fixture calendarReadinessFixture) (*trawlkit.Request, *ckstore.Store) {
	t.Helper()
	t.Setenv("HOME", fixture.home)
	t.Setenv("TZ", "UTC")
	var archive *ckstore.Store
	if _, err := os.Stat(fixture.paths.Archive); err == nil {
		var openErr error
		archive, openErr = ckstore.OpenReadOnly(context.Background(), fixture.paths.Archive)
		if openErr != nil {
			t.Fatal(openErr)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return &trawlkit.Request{Store: archive, Paths: fixture.paths, Format: ckoutput.Text, Out: &bytes.Buffer{}}, archive
}

func captureCalendarStatus(t *testing.T, request *trawlkit.Request) *control.Status {
	t.Helper()
	status, err := New().Status(context.Background(), request)
	t.Logf("typed status=%#v error=%v", status, err)
	if err != nil {
		t.Fatalf("typed status: %v", err)
	}
	if status == nil {
		t.Fatal("typed status is nil")
	}
	return status
}

func logCalendarInputs(t *testing.T, fixture calendarReadinessFixture, statusArgv []string) {
	t.Helper()
	t.Logf("synthetic fixture setup=%q", fixture.stateSetup)
	for index, statement := range calendarSchemaStatements() {
		t.Logf("synthetic schema statement[%d]=%q", index, statement)
	}
	data := calendarFixtureData()
	t.Logf("synthetic fixture stores=%#v", data.Stores)
	t.Logf("synthetic fixture calendars=%#v", data.Calendars)
	t.Logf("synthetic fixture events=%#v", data.Events)
	t.Logf("synthetic fixture tasks=%#v", data.Tasks)
	t.Logf("synthetic fixture identities=%#v", data.Identities)
	t.Logf("synthetic fixture participants=%#v", data.Participants)
	t.Logf("synthetic fixture locations=%#v", data.Locations)
	t.Logf("synthetic input home=%q state_root=%q source_path=%q archive_path=%q config_path=%q logs_path=%q status_argv=%q", fixture.home, fixture.stateRoot, fixture.sourcePath, fixture.paths.Archive, fixture.paths.Config, fixture.paths.Logs, statusArgv)
	for _, path := range []string{
		fixture.stateRoot,
		fixture.sourcePath,
		fixture.sourcePath + "-wal",
		fixture.sourcePath + "-shm",
		fixture.paths.Archive,
		fixture.paths.Archive + "-wal",
		fixture.paths.Archive + "-shm",
		fixture.paths.Config,
		fixture.paths.Logs,
	} {
		input := readCalendarInput(t, fixture, path)
		t.Logf("synthetic input path=%q present=%t kind=%q mode=%#o size=%d", input.path, input.present, input.kind, input.mode.Perm(), input.size)
	}
}

func readCalendarInput(t *testing.T, fixture calendarReadinessFixture, path string) calendarSyntheticInput {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return calendarSyntheticInput{path: path}
		}
		t.Fatalf("stat synthetic input %q: %v", path, err)
	}
	input := calendarSyntheticInput{path: path, present: true, mode: info.Mode()}
	if info.IsDir() {
		input.kind = "directory"
		return input
	}
	if !info.Mode().IsRegular() {
		input.kind = info.Mode().String()
		return input
	}
	input.kind = "file"
	input.size = info.Size()
	return input
}

func renderCalendarCommand(t *testing.T, fixture calendarReadinessFixture, args ...string) string {
	t.Helper()
	stdout, stderr, code := runCalcrawlForTest(t, fixture.stateRoot, args...)
	t.Logf("rendered argv=%q stdout=%q stderr=%q result=exit=%d", args, stdout, stderr, code)
	if code != 0 {
		t.Fatalf("rendered command exited %d", code)
	}
	if stderr != "" {
		t.Fatalf("rendered command stderr = %q", stderr)
	}
	return stdout
}

func assertCalendarStatusRender(t *testing.T, status *control.Status, output string) {
	t.Helper()
	if !strings.Contains(output, "Status: "+status.State) {
		t.Fatalf("rendered status = %q, does not describe typed state %#v", output, status)
	}
	if !strings.Contains(output, status.Summary) {
		t.Fatalf("rendered status = %q, does not describe typed summary %q", output, status.Summary)
	}
}

func calendarSourcePath(home string) string {
	return filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar", "Calendar.sqlitedb")
}
