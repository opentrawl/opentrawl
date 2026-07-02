package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

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
	wantKeys := []string{"schema_version", "contract_version", "id", "display_name", "version", "capabilities"}
	if len(root) != len(wantKeys) {
		t.Fatalf("metadata keys = %#v, want %v", root, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := root[key]; !ok {
			t.Fatalf("metadata missing key %q: %#v", key, root)
		}
	}
	var payload struct {
		SchemaVersion   int      `json:"schema_version"`
		ContractVersion int      `json:"contract_version"`
		ID              string   `json:"id"`
		DisplayName     string   `json:"display_name"`
		Version         string   `json:"version"`
		Capabilities    []string `json:"capabilities"`
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
	if slices.Contains(payload.Capabilities, "open") {
		t.Fatalf("metadata capabilities must not advertise open: %#v", payload.Capabilities)
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

func TestDoctorJSONUsesChecksShape(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		source := readableTelegramSource(t)
		db := seedArchive(t, 1, time.Now())
		stdout, stderr, err := runCLI(t, "--db", db, "doctor", "--path", source, "--json")
		if err != nil {
			t.Fatalf("doctor: %v stderr=%s", err, stderr)
		}
		checks := decodeDoctorChecks(t, stdout)
		if len(checks) != 2 {
			t.Fatalf("checks = %#v", checks)
		}
		for _, check := range checks {
			if check.State != "ok" {
				t.Fatalf("check = %#v, want ok", check)
			}
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
			if check.State != "fail" || check.Message == "" || check.Remedy == "" {
				t.Fatalf("failing check needs message and remedy: %#v", check)
			}
		}
	})
}

func TestSearchJSONIsBoundedAndReportsTruncation(t *testing.T) {
	t.Run("default limit", func(t *testing.T) {
		db := seedSearchArchive(t, 25)
		payload := runSearchJSON(t, db, "search", "launch", "--json")
		if len(payload.Results) != 20 || payload.TotalMatches != 25 || !payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
		assertSearchResultShape(t, payload.Results[0])
	})

	t.Run("explicit limit without truncation", func(t *testing.T) {
		db := seedSearchArchive(t, 25)
		payload := runSearchJSON(t, db, "search", "--limit", "30", "launch", "--json")
		if len(payload.Results) != 25 || payload.TotalMatches != 25 || payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
	})

	t.Run("clamped limit", func(t *testing.T) {
		db := seedSearchArchive(t, 205)
		payload := runSearchJSON(t, db, "search", "--limit", "500", "launch", "--json")
		if len(payload.Results) != 200 || payload.TotalMatches != 205 || !payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
	})
}

type statusJSON struct {
	AppID     string `json:"app_id"`
	State     string `json:"state"`
	Summary   string `json:"summary"`
	Freshness struct {
		LastSync string `json:"last_sync"`
	} `json:"freshness"`
	Counts []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Value int64  `json:"value"`
	} `json:"counts"`
	Auth struct {
		Authorized bool    `json:"authorized"`
		Expires    *string `json:"expires"`
	} `json:"auth"`
}

type doctorCheckJSON struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

type searchJSON struct {
	Query   string `json:"query"`
	Results []struct {
		Ref     string `json:"ref"`
		Time    string `json:"time"`
		Who     string `json:"who"`
		Where   string `json:"where"`
		Snippet string `json:"snippet"`
	} `json:"results"`
	TotalMatches int  `json:"total_matches"`
	Truncated    bool `json:"truncated"`
}

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func runStatusJSON(t *testing.T, db string) statusJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, "--db", db, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	var status statusJSON
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status json = %s err=%v", stdout, err)
	}
	return status
}

func assertStatusState(t *testing.T, status statusJSON, state string) {
	t.Helper()
	if status.AppID != "telecrawl" || status.State != state || status.Summary == "" || !status.Auth.Authorized || status.Auth.Expires != nil {
		t.Fatalf("status = %#v, want state %q", status, state)
	}
	if len(status.Counts) != 3 {
		t.Fatalf("counts = %#v, want 3", status.Counts)
	}
	want := []string{"messages", "chats", "since"}
	for i, count := range status.Counts {
		if count.ID != want[i] || count.Label != want[i] {
			t.Fatalf("counts = %#v, want ids %v", status.Counts, want)
		}
	}
}

func decodeDoctorChecks(t *testing.T, stdout string) []doctorCheckJSON {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout, err)
	}
	if len(root) != 1 {
		t.Fatalf("doctor keys = %#v, want only checks", root)
	}
	var payload struct {
		Checks []doctorCheckJSON `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout, err)
	}
	return payload.Checks
}

func runSearchJSON(t *testing.T, db string, args ...string) searchJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, append([]string{"--db", db}, args...)...)
	if err != nil {
		t.Fatalf("search: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	var payload searchJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	return payload
}

func assertSearchResultShape(t *testing.T, result struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}) {
	t.Helper()
	if !strings.HasPrefix(result.Ref, "telecrawl:msg/") || result.Who == "" || result.Where == "" || result.Snippet == "" {
		t.Fatalf("bad search result = %#v", result)
	}
	if _, err := time.Parse(time.RFC3339, result.Time); err != nil {
		t.Fatalf("search result time = %q err=%v", result.Time, err)
	}
}

func seedArchive(t *testing.T, messages int, finishedAt time.Time) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var chats []store.Chat
	var rows []store.Message
	if messages > 0 {
		chats = []store.Chat{{JID: "100", Kind: "chat", Name: "example chat", LastMessageAt: time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC), MessageCount: messages}}
		for i := 0; i < messages; i++ {
			rows = append(rows, store.Message{
				SourcePK:   int64(i + 1),
				ChatJID:    "100",
				ChatName:   "example chat",
				MessageID:  fmt.Sprintf("0:%d", i+1),
				SenderJID:  "200",
				SenderName: "Example Sender",
				Timestamp:  time.Date(2020, 1, 2, 12, i, 0, 0, time.UTC),
				Text:       "synthetic launch note",
			})
		}
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: finishedAt, FinishedAt: finishedAt}, nil, chats, nil, nil, nil, rows); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSearchArchive(t *testing.T, count int) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	chats := []store.Chat{{JID: "100", Kind: "chat", Name: "example chat", LastMessageAt: now, MessageCount: count}}
	messages := make([]store.Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, store.Message{
			SourcePK:   int64(i + 1),
			ChatJID:    "100",
			ChatName:   "example chat",
			MessageID:  fmt.Sprintf("0:%d", i+1),
			SenderJID:  "200",
			SenderName: "Example Sender",
			Timestamp:  now.Add(time.Duration(i) * time.Minute),
			Text:       fmt.Sprintf("synthetic launch note %03d", i+1),
		})
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, nil, chats, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func readableTelegramSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".tempkeyEncrypted"), []byte("synthetic-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "account-1", "postbox", "db", "db_sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("SQLite format 3\x00synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
