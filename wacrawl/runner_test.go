package wacrawl

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/config"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/wacrawl/internal/backup"
	wastore "github.com/openclaw/wacrawl/internal/store"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestRunBackupStoreNoneFreshBackupVerbsDoNotCreateArchive(t *testing.T) {
	ctx := context.Background()
	setGitIdentity(t)
	stateRoot := t.TempDir()
	archivePath := filepath.Join(stateRoot, "wacrawl", "wacrawl.db")

	cfg := seedBackupConfig(t, ctx)
	writeConfig(t, stateRoot, Config{Backup: cfg})

	code, stdout, stderr := captureRun(t, []string{"backup", "init", "--no-push", "--json", "--state-root", stateRoot}, New())
	if code != 0 || !strings.Contains(stdout, `"recipient"`) {
		t.Fatalf("backup init code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertNoArchive(t, archivePath, "backup init")

	code, stdout, stderr = captureRun(t, []string{"status", "--json", "--state-root", stateRoot}, New())
	if code != 0 {
		t.Fatalf("status after backup init code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status control.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, stdout)
	}
	if status.State != "missing" {
		t.Fatalf("status state after backup init = %q, want missing\n%s", status.State, stdout)
	}
	assertNoArchive(t, archivePath, "status after backup init")

	code, stdout, stderr = captureRun(t, []string{"backup", "status", "--json", "--state-root", stateRoot}, New())
	if code != 0 || !strings.Contains(stdout, `"manifest"`) {
		t.Fatalf("backup status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertNoArchive(t, archivePath, "backup status")

	code, stdout, stderr = captureRun(t, []string{"backup", "snapshots", "--json", "--state-root", stateRoot}, New())
	if code != 0 || !strings.Contains(stdout, `"snapshots"`) {
		t.Fatalf("backup snapshots code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertNoArchive(t, archivePath, "backup snapshots")
	t.Logf("archive_exists_after_backup_store_none=false archive_path=%s", archivePath)
}

func TestRunStatusOmitsSinceForEmptyArchive(t *testing.T) {
	ctx := context.Background()
	stateRoot := t.TempDir()
	archivePath := filepath.Join(stateRoot, "wacrawl", "wacrawl.db")
	st, err := wastore.Open(ctx, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := captureRun(t, []string{"status", "--json", "--state-root", stateRoot}, New())
	if code != 0 {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status control.Status
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status JSON: %v\n%s", err, stdout)
	}
	if status.State != "empty" {
		t.Fatalf("status state = %q, want empty\n%s", status.State, stdout)
	}
	if countIDPresent(status.Counts, "since") {
		t.Fatalf("empty archive status should omit since count: %#v", status.Counts)
	}
}

func TestRunSearchWhoAmbiguousRefusesWithCandidates(t *testing.T) {
	stateRoot := t.TempDir()
	createAmbiguousWhoArchive(t, stateRoot)

	code, stdout, stderr := captureRun(t, []string{"search", "needle", "--who", "CASEY", "--json", "--state-root", stateRoot}, New())
	if code != 4 || stderr != "" {
		t.Fatalf("ambiguous JSON code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope output.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("ambiguous JSON: %v\n%s", err, stdout)
	}
	candidates, ok := envelope.Error.Fields["candidates"].([]any)
	if envelope.Error.Code != "ambiguous_who" || !ok || len(candidates) != 2 {
		t.Fatalf("ambiguous error = %#v", envelope.Error)
	}
	if !strings.Contains(stdout, "Casey One") || !strings.Contains(stdout, "Casey Two") {
		t.Fatalf("ambiguous candidates missing from JSON:\n%s", stdout)
	}

	code, stdout, stderr = captureRun(t, []string{"search", "needle", "--who", "CASEY", "--state-root", stateRoot}, New())
	if code != 4 || stdout != "" {
		t.Fatalf("ambiguous text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"--who matched more than one person",
		"Casey One",
		"Casey Two",
		"Retry with one listed identifier: search needle --who casey-two@s.whatsapp.net",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("ambiguous text missing %q:\n%s", want, stderr)
		}
	}
}

func seedBackupConfig(t *testing.T, ctx context.Context) backup.Config {
	t.Helper()
	remote := filepath.Join(t.TempDir(), "remote.git")
	runGit(t, "", "init", "--bare", remote)
	repo := filepath.Join(t.TempDir(), "backup")
	identity := filepath.Join(t.TempDir(), "age.key")
	cfg, _, err := backup.Init(ctx, backup.Options{
		Repo:       repo,
		Remote:     remote,
		Identity:   identity,
		Push:       false,
		SaveConfig: func(backup.Config) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	source := openSyntheticBackupStore(t, ctx)
	result, err := backup.Push(ctx, source, backup.Options{Config: cfg, Push: false})
	if err != nil {
		t.Fatal(err)
	}
	if result.Messages != 1 {
		t.Fatalf("seed backup result = %+v", result)
	}
	return cfg
}

func openSyntheticBackupStore(t *testing.T, ctx context.Context) *wastore.Store {
	t.Helper()
	st, err := wastore.Open(ctx, filepath.Join(t.TempDir(), "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	err = st.ReplaceAll(
		ctx,
		wastore.ImportStats{FinishedAt: now},
		[]wastore.Contact{{JID: "alice@s.whatsapp.net", Phone: "+15550100", FullName: "Alice Example", UpdatedAt: now}},
		[]wastore.Chat{{JID: "alice@s.whatsapp.net", Kind: "dm", Name: "Alice Example", LastMessageAt: now, MessageCount: 1}},
		nil,
		nil,
		[]wastore.Message{{SourcePK: 1, ChatJID: "alice@s.whatsapp.net", ChatName: "Alice Example", MessageID: "seed", SenderJID: "alice@s.whatsapp.net", SenderName: "Alice Example", Timestamp: now, Text: "seed message", RawType: 0, MessageType: "text"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func writeConfig(t *testing.T, stateRoot string, cfg Config) {
	t.Helper()
	if err := config.WriteTOML(filepath.Join(stateRoot, "wacrawl", "config.toml"), cfg, 0o600); err != nil {
		t.Fatal(err)
	}
}

func setGitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "OpenTrawl Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "OpenTrawl Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- tests pass fixed Git commands and temporary paths.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func assertNoArchive(t *testing.T, archivePath, step string) {
	t.Helper()
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("%s created archive: err=%v path=%s", step, err, archivePath)
	}
}

func countIDPresent(counts []control.Count, id string) bool {
	for _, count := range counts {
		if count.ID == id {
			return true
		}
	}
	return false
}

func createAmbiguousWhoArchive(t *testing.T, stateRoot string) {
	t.Helper()
	ctx := context.Background()
	st, err := wastore.Open(ctx, filepath.Join(stateRoot, "wacrawl", "wacrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	contacts := []wastore.Contact{
		{JID: "casey-one@s.whatsapp.net", FullName: "Casey One"},
		{JID: "casey-two@s.whatsapp.net", FullName: "Casey Two"},
	}
	chats := []wastore.Chat{
		{JID: "casey-one@s.whatsapp.net", Kind: "dm", Name: "Casey One", LastMessageAt: now, MessageCount: 1},
		{JID: "casey-two@s.whatsapp.net", Kind: "dm", Name: "Casey Two", LastMessageAt: now, MessageCount: 1},
	}
	messages := []wastore.Message{
		{SourcePK: 1, ChatJID: "casey-one@s.whatsapp.net", ChatName: "Casey One", MessageID: "casey-one", SenderJID: "casey-one@s.whatsapp.net", SenderName: "Casey One", Timestamp: now, RawType: 0, MessageType: "text", Text: "needle one"},
		{SourcePK: 2, ChatJID: "casey-two@s.whatsapp.net", ChatName: "Casey Two", MessageID: "casey-two", SenderJID: "casey-two@s.whatsapp.net", SenderName: "Casey Two", Timestamp: now.Add(time.Minute), RawType: 0, MessageType: "text", Text: "needle two"},
	}
	if err := st.ReplaceAll(ctx, wastore.ImportStats{FinishedAt: now}, contacts, chats, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
}
