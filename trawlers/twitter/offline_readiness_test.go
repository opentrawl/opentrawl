package birdcrawl

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestImportedArchiveIsReadyWithoutLiveSync(t *testing.T) {
	for _, withCredentials := range []bool{false, true} {
		t.Run(credentialsCaseName(withCredentials), func(t *testing.T) {
			transport := installFailingXTransport(t)
			stateRoot := stateRootForRun(t)
			if withCredentials {
				writeSyntheticCredentials(t, stateRoot)
			}

			importResult := runBirdcrawlRaw(t, stateRoot, "import", "archive", filepath.Join("internal", "archive", "testdata", "synthetic-dump"), "--json")
			assertSuccess(t, importResult, "import archive")
			var imported importEnvelope
			assertJSON(t, importResult.stdout, &imported)
			if imported.Tweets != 8 {
				t.Fatalf("imported tweets = %d, want 8", imported.Tweets)
			}

			statusResult := runBirdcrawlRaw(t, stateRoot, "status", "--json")
			assertSuccess(t, statusResult, "status --json")
			var status control.Status
			assertJSON(t, statusResult.stdout, &status)
			if status.State != "ok" {
				t.Fatalf("status state = %q, want ok: %s", status.State, statusResult.stdout)
			}
			if status.LastImportAt == "" || status.LastSyncAt != "" {
				t.Fatalf("status freshness = import %q, sync %q; want import only", status.LastImportAt, status.LastSyncAt)
			}
			if !strings.Contains(status.Summary, "archive dump imported") {
				t.Fatalf("status summary = %q, want local import summary", status.Summary)
			}
			if len(transport.requests) != 0 {
				t.Fatalf("offline request list = %#v, want empty", transport.requests)
			}
			t.Logf("import argv=%q result=%d stdout=%s stderr=%q", "import archive internal/archive/testdata/synthetic-dump --json", importResult.code, importResult.stdout, importResult.stderr)
			t.Logf("status argv=%q result=%d stdout=%s stderr=%q", "status --json", statusResult.code, statusResult.stdout, statusResult.stderr)
		})
	}
}

func TestMissingArchiveNeedsLocalImportOffline(t *testing.T) {
	for _, emptyDatabase := range []bool{false, true} {
		t.Run(archiveCaseName(emptyDatabase), func(t *testing.T) {
			transport := installFailingXTransport(t)
			stateRoot := stateRootForRun(t)
			if emptyDatabase {
				if err := os.MkdirAll(filepath.Join(stateRoot, "twitter"), 0o700); err != nil {
					t.Fatal(err)
				}
				st, err := store.Open(t.Context(), filepath.Join(stateRoot, "twitter", "twitter.db"))
				if err != nil {
					t.Fatal(err)
				}
				if err := st.Close(); err != nil {
					t.Fatal(err)
				}
			}

			statusResult := runBirdcrawlRaw(t, stateRoot, "status", "--json")
			assertSuccess(t, statusResult, "status --json")
			var status control.Status
			assertJSON(t, statusResult.stdout, &status)
			if status.State == "ok" || !strings.Contains(status.Summary, "import an X archive dump") {
				t.Fatalf("status = %#v, want import readiness failure", status)
			}
			if len(transport.requests) != 0 {
				t.Fatalf("offline request list = %#v, want empty", transport.requests)
			}
			t.Logf("status argv=%q result=%d stdout=%s stderr=%q", "status --json", statusResult.code, statusResult.stdout, statusResult.stderr)
		})
	}
}

func TestLiveSyncOnlyDataRequestsArchiveImportWithoutCallingItEmpty(t *testing.T) {
	transport := installFailingXTransport(t)
	stateRoot := stateRootForRun(t)
	importSyntheticArchive(t, stateRoot)
	removeArchiveImportMarker(t, stateRoot)

	statusResult := runBirdcrawlRaw(t, stateRoot, "status", "--json")
	assertSuccess(t, statusResult, "status --json")
	var status control.Status
	assertJSON(t, statusResult.stdout, &status)
	if status.State == "ok" || status.Summary != "local X data exists; import an X archive dump to establish archive readiness" {
		t.Fatalf("status = %#v, want a non-empty archive import remedy", status)
	}
	if strings.Contains(status.Summary, "archive is empty") {
		t.Fatalf("status summary claims local data is empty: %q", status.Summary)
	}
	if len(transport.requests) != 0 {
		t.Fatalf("offline request list = %#v, want empty", transport.requests)
	}
	t.Logf("status argv=%q result=%d stdout=%s stderr=%q requests=%#v", "status --json", statusResult.code, statusResult.stdout, statusResult.stderr, transport.requests)
}

func TestSchemaOutdatedArchiveRequestsUpgradeWithoutReimport(t *testing.T) {
	transport := installFailingXTransport(t)
	stateRoot := stateRootForRun(t)
	importSyntheticArchive(t, stateRoot)
	makeLegacySyncState(t, stateRoot)

	statusResult := runBirdcrawlRaw(t, stateRoot, "status", "--json")
	assertSuccess(t, statusResult, "status --json")
	var status control.Status
	assertJSON(t, statusResult.stdout, &status)
	if status.State != "error" || !strings.Contains(status.Summary, archiveSchemaUpgradeMessage) || !strings.Contains(status.Summary, "run trawl twitter sync") {
		t.Fatalf("status = %#v, want schema upgrade remedy", status)
	}
	if strings.Contains(status.Summary, "import archive") {
		t.Fatalf("status asks to re-import schema-outdated archive: %q", status.Summary)
	}
	if len(transport.requests) != 0 {
		t.Fatalf("offline request list = %#v, want empty", transport.requests)
	}
	t.Logf("status argv=%q result=%d stdout=%s stderr=%q", "status --json", statusResult.code, statusResult.stdout, statusResult.stderr)
}

func TestInvalidArchiveLeavesOfflineReadinessMissing(t *testing.T) {
	transport := installFailingXTransport(t)
	stateRoot := stateRootForRun(t)
	invalidArchive := filepath.Join(t.TempDir(), "invalid-export.zip")
	if err := os.WriteFile(invalidArchive, []byte("not an X archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	importResult := runBirdcrawlRaw(t, stateRoot, "import", "archive", invalidArchive, "--json")
	if importResult.code == 0 || len(importResult.stdout) == 0 || importResult.stderr != "" {
		t.Fatalf("invalid import result = %#v, want JSON failure on stdout", importResult)
	}
	if !strings.Contains(string(importResult.stdout), "not a valid zip file") {
		t.Fatalf("invalid import output = %s, want archive parse error", importResult.stdout)
	}
	statusResult := runBirdcrawlRaw(t, stateRoot, "status", "--json")
	assertSuccess(t, statusResult, "status --json")
	var status control.Status
	assertJSON(t, statusResult.stdout, &status)
	if status.State == "ok" || !strings.Contains(status.Summary, "import an X archive dump") {
		t.Fatalf("status = %#v, want import readiness failure", status)
	}
	if len(transport.requests) != 0 {
		t.Fatalf("offline request list = %#v, want empty", transport.requests)
	}
	t.Logf("import argv=%q result=%d stdout=%s stderr=%q", "import archive invalid-export.zip --json", importResult.code, importResult.stdout, importResult.stderr)
	t.Logf("status argv=%q result=%d stdout=%s stderr=%q requests=%#v", "status --json", statusResult.code, statusResult.stdout, statusResult.stderr, transport.requests)
}

type failingXTransport struct {
	requests []string
}

func (t *failingXTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req.Method+" "+req.URL.String())
	return nil, errors.New("network disabled in offline readiness test")
}

func installFailingXTransport(t *testing.T) *failingXTransport {
	t.Helper()
	t.Setenv("BIRDCRAWL_TEST_DISABLE_NETWORK", "1")
	transport := &failingXTransport{}
	oldClient, oldBaseURL := xapiHTTPClient, xapiBaseURL
	xapiHTTPClient = &http.Client{Transport: transport}
	xapiBaseURL = "https://offline.invalid"
	t.Cleanup(func() {
		xapiHTTPClient = oldClient
		xapiBaseURL = oldBaseURL
	})
	return transport
}

func writeSyntheticCredentials(t *testing.T, stateRoot string) {
	t.Helper()
	path := filepath.Join(stateRoot, "twitter", "credentials.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("client_id = \"client-example\"\nclient_secret = \"secret-example\"\naccess_token = \"access-example\"\nrefresh_token = \"refresh-example\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func importSyntheticArchive(t *testing.T, stateRoot string) {
	t.Helper()
	result := runBirdcrawlRaw(t, stateRoot, "import", "archive", filepath.Join("internal", "archive", "testdata", "synthetic-dump"), "--json")
	assertSuccess(t, result, "import archive")
	t.Logf("import argv=%q result=%d stdout=%s stderr=%q", "import archive internal/archive/testdata/synthetic-dump --json", result.code, result.stdout, result.stderr)
}

func removeArchiveImportMarker(t *testing.T, stateRoot string) {
	t.Helper()
	ctx := t.Context()
	archivePath := filepath.Join(stateRoot, "twitter", "twitter.db")
	st, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `delete from sync_state where source_name = 'twitter' and entity_id = 'archive_import'`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func makeLegacySyncState(t *testing.T, stateRoot string) {
	t.Helper()
	ctx := t.Context()
	archivePath := filepath.Join(stateRoot, "twitter", "twitter.db")
	st, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `drop table sync_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `create table sync_state (
		kind text primary key,
		cursor text,
		last_sync_at text,
		last_result text,
		coverage_note text
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `insert into sync_state(kind, cursor, last_sync_at, last_result, coverage_note) values ('archive_import', '2026-06-02T08:00:00Z', '2026-07-04T10:00:00Z', 'ok', 'synthetic archive')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `pragma user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertJSON(t *testing.T, data []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("json = %s: %v", data, err)
	}
}

func assertSuccess(t *testing.T, result birdcrawlResult, command string) {
	t.Helper()
	if result.code != 0 {
		t.Fatalf("%s exited %d\nstdout:\n%s\nstderr:\n%s", command, result.code, result.stdout, result.stderr)
	}
}

func credentialsCaseName(withCredentials bool) string {
	if withCredentials {
		return "credentials-present"
	}
	return "credentials-absent"
}

func archiveCaseName(emptyDatabase bool) string {
	if emptyDatabase {
		return "empty-database"
	}
	return "missing-database"
}
