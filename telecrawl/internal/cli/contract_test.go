package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/telecrawl/internal/store"
)

func TestMetadataJSONUsesContractShape(t *testing.T) {
	stdout, stderr, err := runCLI(t, "metadata", "--json")
	if err != nil {
		t.Fatalf("metadata: %v stderr=%s", err, stderr)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("metadata json = %s err=%v", stdout, err)
	}
	wantKeys := []string{"schema_version", "contract_version", "id", "display_name", "version", "paths", "capabilities"}
	if len(root) != len(wantKeys) {
		t.Fatalf("metadata keys = %#v, want %v", root, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := root[key]; !ok {
			t.Fatalf("metadata missing key %q: %#v", key, root)
		}
	}
	var payload struct {
		SchemaVersion   int    `json:"schema_version"`
		ContractVersion int    `json:"contract_version"`
		ID              string `json:"id"`
		DisplayName     string `json:"display_name"`
		Version         string `json:"version"`
		Paths           struct {
			DefaultLogs string `json:"default_logs"`
		} `json:"paths"`
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("metadata json = %s err=%v", stdout, err)
	}
	if payload.SchemaVersion != 1 || payload.ContractVersion != 1 || payload.ID != "telecrawl" || payload.DisplayName != "Telegram" || payload.Version == "" {
		t.Fatalf("metadata = %#v", payload)
	}
	if !slices.Contains(payload.Capabilities, "contacts_export") {
		t.Fatalf("metadata capabilities = %#v, want contacts_export", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "open") {
		t.Fatalf("metadata capabilities = %#v, want open", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "who") {
		t.Fatalf("metadata capabilities = %#v, want who", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "short_refs") {
		t.Fatalf("metadata capabilities = %#v, want short_refs", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "verbose_logs") {
		t.Fatalf("metadata capabilities = %#v, want verbose_logs", payload.Capabilities)
	}
	if payload.Paths.DefaultLogs != defaultLogDir() {
		t.Fatalf("metadata paths.default_logs = %q, want %q", payload.Paths.DefaultLogs, defaultLogDir())
	}
}

func TestStatusJSONUsesContractShapeAndStates(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "telecrawl.db")
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "missing")
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatalf("status --json created missing archive: err=%v", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "telecrawl.db")
		st, err := store.Open(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "empty")
		if status.Counts[0].Value != 0 || status.Counts[1].Value != 0 || status.Counts[2].Value != 0 {
			t.Fatalf("empty counts = %#v", status.Counts)
		}
	})

	t.Run("ok", func(t *testing.T) {
		db := seedArchive(t, 1, time.Now().Add(-time.Hour))
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "ok")
		if status.Freshness.LastSync == "" {
			t.Fatalf("missing freshness: %#v", status)
		}
		if _, err := time.Parse(time.RFC3339, status.Freshness.LastSync); err != nil {
			t.Fatalf("last_sync = %q err=%v", status.Freshness.LastSync, err)
		}
		if status.Counts[0].Value != 1 || status.Counts[1].Value != 1 || status.Counts[2].Value != 2020 {
			t.Fatalf("ok counts = %#v", status.Counts)
		}
	})

	t.Run("stale", func(t *testing.T) {
		db := seedArchive(t, 1, time.Now().Add(-48*time.Hour))
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "stale")
		if status.Summary != "archive import is 2 days old; run telecrawl import to refresh." {
			t.Fatalf("stale summary = %q", status.Summary)
		}
	})

	t.Run("error", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "telecrawl.db")
		if err := os.WriteFile(db, []byte("not sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "error")
	})
}

func TestStatusJSONOmitsLogTail(t *testing.T) {
	db := seedSearchArchive(t, 1)
	if _, _, err := runCLI(t, "--db", db, "open", "not-a-ref"); err == nil {
		t.Fatal("open not-a-ref succeeded, want logged error")
	}
	stdout, stderr, err := runCLI(t, "--db", db, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	for _, forbidden := range []string{`"run_id"`, `"last_event"`, `"event"`, "event=", "visibility="} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("status log leaked %q:\n%s", forbidden, stdout)
		}
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("status json = %s err=%v", stdout, err)
	}
	if _, ok := root["log"]; ok {
		t.Fatalf("status json includes log field: %s", stdout)
	}
}

func TestDoctorJSONUsesChecksShape(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		source := readableTelegramSource(t)
		db := seedArchive(t, 1, time.Now())
		stdout, stderr, err := runCLI(t, "--db", db, "doctor", "--path", source, "--json")
		if err != nil {
			t.Fatalf("doctor: %v stderr=%s", err, stderr)
		}
		checks := decodeDoctorChecks(t, stdout)
		if len(checks) != 3 {
			t.Fatalf("checks = %#v", checks)
		}
		for _, check := range checks {
			if check.State != "ok" {
				t.Fatalf("check = %#v, want ok", check)
			}
		}
		if checks[2].ID != "sync_recency" || checks[2].Message != "Archive import is fresh." {
			t.Fatalf("sync recency check = %#v", checks[2])
		}
	})

	t.Run("stale import warns", func(t *testing.T) {
		source := readableTelegramSource(t)
		db := seedArchive(t, 1, time.Now().Add(-48*time.Hour))
		stdout, stderr, err := runCLI(t, "--db", db, "doctor", "--path", source, "--json")
		if err != nil {
			t.Fatalf("doctor: %v stderr=%s", err, stderr)
		}
		checks := decodeDoctorChecks(t, stdout)
		got := checks[len(checks)-1]
		if got.ID != "sync_recency" || got.State != "warn" || got.Message != "Archive import is 2 days old." || got.Remedy != "run telecrawl import" {
			t.Fatalf("stale check = %#v", got)
		}
	})

	t.Run("fail", func(t *testing.T) {
		dir := t.TempDir()
		stdout, stderr, err := runCLI(t, "--db", filepath.Join(dir, "missing.db"), "doctor", "--path", filepath.Join(dir, "missing-source"), "--json")
		if err != nil {
			t.Fatalf("doctor: %v stderr=%s", err, stderr)
		}
		checks := decodeDoctorChecks(t, stdout)
		if len(checks) != 2 {
			t.Fatalf("checks = %#v", checks)
		}
		for _, check := range checks {
			if check.State != "missing" || check.Message == "" || check.Remedy == "" {
				t.Fatalf("failing check needs message and remedy: %#v", check)
			}
		}
	})
}

func TestStatusHumanUsesShapedSummary(t *testing.T) {
	db := seedArchive(t, 1, time.Now().Add(-time.Hour))
	stdout, stderr, err := runCLI(t, "--db", db, "status")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	conformance.AssertHumanOutput(t, stdout)
	for _, disallowed := range []string{"db_path:", "last_import_at:", "oldest_message:", "unread_chats:"} {
		if strings.Contains(stdout, disallowed) {
			t.Fatalf("status leaked raw key %q:\n%s", disallowed, stdout)
		}
	}
	for _, want := range []string{"Status: ok", "archive is fresh", "Archive:", "Messages:", "Chats:", "Auth:", "Freshness:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, stdout)
		}
	}
}

func TestStatusHumanShowsAgeCommandAndGroupedCounts(t *testing.T) {
	staleDB := seedArchive(t, 1, time.Now().Add(-48*time.Hour))
	stdout, stderr, err := runCLI(t, "--db", staleDB, "status")
	if err != nil {
		t.Fatalf("status stale: %v stderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "archive import is 2 days old; run telecrawl import to refresh.") {
		t.Fatalf("stale status missing age and command:\n%s", stdout)
	}

	countDB := seedArchive(t, 1000, time.Now())
	stdout, stderr, err = runCLI(t, "--db", countDB, "status")
	if err != nil {
		t.Fatalf("status counts: %v stderr=%s", err, stderr)
	}
	for _, want := range []string{"Messages: 1,000", "First message: 2020"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Since:") {
		t.Fatalf("status kept old Since label:\n%s", stdout)
	}
}

func TestDoctorHumanUsesChecksList(t *testing.T) {
	source := readableTelegramSource(t)
	db := seedArchive(t, 1, time.Now())
	stdout, stderr, err := runCLI(t, "--db", db, "doctor", "--path", source)
	if err != nil {
		t.Fatalf("doctor: %v stderr=%s", err, stderr)
	}
	conformance.AssertHumanOutput(t, stdout)
	for _, disallowed := range []string{"path:", "sqlite_files:", "tdesktop_files:", "files_scanned:"} {
		if strings.Contains(stdout, disallowed) {
			t.Fatalf("doctor leaked raw key %q:\n%s", disallowed, stdout)
		}
	}
	for _, want := range []string{"Doctor checks:", "source store: ok", "archive: ok"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("doctor missing %q:\n%s", want, stdout)
		}
	}
}
