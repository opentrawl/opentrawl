package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func TestSyncBackupIngestAndShardIdempotence(t *testing.T) {
	fake := installFakeGog(t)
	dbPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	repoPath := filepath.Join(t.TempDir(), "backup")
	err := Run(context.Background(), []string{"sync", "--query", "from:me", "--max", "25", "--json", "--archive", dbPath, "--backup-repo", repoPath}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got := countLogLines(t, fake.log, "backup gmail push"); got != 1 {
		t.Fatalf("backup push calls = %d, want 1", got)
	}
	if got := countLogLines(t, fake.log, "backup cat"); got != 2 {
		t.Fatalf("backup cat calls = %d, want 2", got)
	}
	st, err := archive.OpenExisting(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := st.Status(context.Background())
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if status.Messages != 3 {
		_ = st.Close()
		t.Fatalf("messages after sync = %d, want 3", status.Messages)
	}
	open, err := st.OpenMessage(context.Background(), archive.RefPrefix+"m3")
	_ = st.Close()
	if err != nil {
		t.Fatal(err)
	}
	if open.Headers.ToAddress == "" || open.Headers.CcAddress == "" {
		t.Fatalf("headers = %#v", open.Headers)
	}
	clearLog(t, fake.log)
	err = Run(context.Background(), []string{"sync", "--query", "from:me", "--max", "25", "--json", "--archive", dbPath, "--backup-repo", repoPath}, &bytes.Buffer{}, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	if got := countLogLines(t, fake.log, "backup gmail push"); got != 1 {
		t.Fatalf("second backup push calls = %d, want 1", got)
	}
	if got := countLogLines(t, fake.log, "backup cat"); got != 0 {
		t.Fatalf("second backup cat calls = %d, want 0", got)
	}
}

func TestStatusMissingEmptyAndCorrupt(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	missingPath := filepath.Join(t.TempDir(), "missing.db")
	missing := runStatus(t, ctx, missingPath)
	if missing.State != "missing" {
		t.Fatalf("missing state = %q", missing.State)
	}
	emptyPath := filepath.Join(t.TempDir(), "empty.db")
	st, err := archive.Open(ctx, emptyPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	empty := runStatus(t, ctx, emptyPath)
	if empty.State != "empty" {
		t.Fatalf("empty state = %q", empty.State)
	}
	var search archive.SearchResult
	runJSON(t, ctx, []string{"search", "project", "--json", "--archive", emptyPath}, &search)
	if len(search.Results) != 0 || search.TotalMatches != 0 {
		t.Fatalf("empty search = %#v", search)
	}
	var stdout bytes.Buffer
	if err := Run(ctx, []string{"open", archive.RefPrefix + "missing", "--json", "--archive", emptyPath}, &stdout, &bytes.Buffer{}); err == nil {
		t.Fatal("open succeeded on empty archive")
	}
	corruptPath := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corruptPath, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := runStatus(t, ctx, corruptPath)
	if corrupt.State != "error" {
		t.Fatalf("corrupt state = %q", corrupt.State)
	}
}

func TestDoctorReportsMissingArchive(t *testing.T) {
	installFakeGog(t)
	var out doctorOutput
	runJSON(t, context.Background(), []string{"doctor", "--json", "--archive", filepath.Join(t.TempDir(), "missing.db")}, &out)
	found := false
	for _, check := range out.Checks {
		if check.ID == "archive" && check.State == "fail" && check.Remedy == "run gogcrawl sync" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor checks = %#v", out.Checks)
	}
}

func TestDoctorChecksGogVersion(t *testing.T) {
	installFakeGog(t)
	t.Setenv("GOG_FAKE_VERSION", "v0.30.9")
	var out doctorOutput
	runJSON(t, context.Background(), []string{"doctor", "--json", "--archive", filepath.Join(t.TempDir(), "missing.db")}, &out)
	found := false
	for _, check := range out.Checks {
		if check.ID == "gog_version" && check.State == "fail" && check.Remedy == "upgrade gogcli" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor checks = %#v", out.Checks)
	}
}

func TestContactsExportFiltersEmptyPhones(t *testing.T) {
	installFakeGog(t)
	var export control.ContactExport
	runJSON(t, context.Background(), []string{"contacts", "export", "--json"}, &export)
	if len(export.Contacts) != 1 {
		t.Fatalf("contacts = %#v", export.Contacts)
	}
	contact := export.Contacts[0]
	if contact.DisplayName != "Alice Example" || len(contact.PhoneNumbers) != 1 || contact.PhoneNumbers[0] != "+15550101000" {
		t.Fatalf("contact = %#v", contact)
	}
}

func TestMetadataDeclaresContactsExport(t *testing.T) {
	var manifest metadataEnvelope
	runJSON(t, context.Background(), []string{"metadata", "--json"}, &manifest)
	if manifest.ContractVersion != 1 || manifest.ID != "gogcrawl" {
		t.Fatalf("manifest = %#v", manifest)
	}
	if !contains(manifest.Capabilities, "contacts_export") {
		t.Fatalf("capabilities = %#v", manifest.Capabilities)
	}
}

func runStatus(t *testing.T, ctx context.Context, dbPath string) statusEnvelope {
	t.Helper()
	var out statusEnvelope
	runJSON(t, ctx, []string{"status", "--json", "--archive", dbPath}, &out)
	return out
}

func runJSON(t *testing.T, ctx context.Context, args []string, out any) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if err := Run(ctx, args, &stdout, &stderr); err != nil {
		t.Fatalf("Run(%v) failed: %v\nstdout=%s\nstderr=%s", args, err, stdout.String(), stderr.String())
	}
	if err := json.Unmarshal(stdout.Bytes(), out); err != nil {
		t.Fatalf("decode JSON for %v: %v\n%s", args, err, stdout.String())
	}
}

type fakeGogInstall struct {
	dir string
	log string
}

func installFakeGog(t *testing.T) fakeGogInstall {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	path := filepath.Join(dir, "gog")
	if err := os.WriteFile(path, []byte(fakeGogScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOG_FAKE_LOG", log)
	return fakeGogInstall{dir: dir, log: log}
}

func clearLog(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func countLogLines(t *testing.T, path, containsText string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, containsText) {
			count++
		}
	}
	return count
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

const fakeGogScript = `#!/bin/sh
printf '%s\n' "$*" >> "$GOG_FAKE_LOG"

if [ "$1" = "--version" ]; then
  if [ -n "$GOG_FAKE_VERSION" ]; then
    printf '%s\n' "$GOG_FAKE_VERSION"
  else
    printf 'v0.31.1 (test 2026-07-02T00:00:00Z)\n'
  fi
  exit 0
fi

if [ "$1" = "auth" ] && [ "$2" = "list" ]; then
  printf 'alice@example.com\tmain\tgmail\t2030-01-02T03:04:05Z\ttrue\t\toauth\n'
  exit 0
fi

if [ "$1" = "backup" ] && [ "$2" = "init" ]; then
  repo=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--repo" ]; then
      repo="$2"
      shift 2
      continue
    fi
    shift
  done
  mkdir -p "$repo/.git"
  printf '[core]\n\trepositoryformatversion = 0\n' > "$repo/.git/config"
  exit 0
fi

if [ "$1" = "backup" ] && [ "$2" = "gmail" ] && [ "$3" = "push" ]; then
  repo=""
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "--repo" ]; then
      repo="$2"
      shift 2
      continue
    fi
    shift
  done
  mkdir -p "$repo"
  cat > "$repo/manifest.json" <<'JSON'
{"services":{"gmail":{"shards":[
{"path":"data/gmail/account/labels.jsonl.gz.age","plaintext_sha256":"labels-hash","rows":1},
{"path":"data/gmail/account/messages/part-000001.jsonl.gz.age","plaintext_sha256":"messages-hash","rows":3}
]}}}
JSON
  exit 0
fi

if [ "$1" = "backup" ] && [ "$2" = "cat" ]; then
  shard=""
  for arg in "$@"; do
    case "$arg" in
      *.jsonl.gz.age) shard="$arg" ;;
    esac
  done
  case "$shard" in
    *labels.jsonl.gz.age)
      printf '{"id":"INBOX","name":"Inbox","type":"system"}\n'
      ;;
    *part-000001.jsonl.gz.age)
      cat <<'JSON'
{"id":"m3","threadId":"t3","historyId":"h3","internalDate":1783000991000,"labelIds":["INBOX"],"sizeEstimate":100,"raw":"RnJvbTogQWxpY2UgRXhhbXBsZSA8YWxpY2VAZXhhbXBsZS5jb20-DQpUbzogQm9iIEV4YW1wbGUgPGJvYkBleGFtcGxlLmNvbT4NCkNjOiBDYXJvbCBFeGFtcGxlIDxjYXJvbEBleGFtcGxlLmNvbT4NClN1YmplY3Q6IE5ld2VzdCBwcm9qZWN0IHN5bmMNCg0KTmV3ZXN0IHByb2plY3Qgc3luYyBib2R5Lg0K"}
{"id":"m2","threadId":"t2","historyId":"h2","internalDate":1782997391000,"labelIds":["SENT"],"sizeEstimate":100,"raw":"RnJvbTogQWxpY2UgRXhhbXBsZSA8YWxpY2VAZXhhbXBsZS5jb20-DQpUbzogQm9iIEV4YW1wbGUgPGJvYkBleGFtcGxlLmNvbT4NCkNjOiBDYXJvbCBFeGFtcGxlIDxjYXJvbEBleGFtcGxlLmNvbT4NClN1YmplY3Q6IE1pZGRsZSBwcm9qZWN0IHN5bmMNCg0KTWlkZGxlIHByb2plY3Qgc3luYyBib2R5Lg0K"}
{"id":"m1","threadId":"t1","historyId":"h1","internalDate":1782993791000,"labelIds":["ARCHIVE"],"sizeEstimate":100,"raw":"RnJvbTogQWxpY2UgRXhhbXBsZSA8YWxpY2VAZXhhbXBsZS5jb20-DQpUbzogQm9iIEV4YW1wbGUgPGJvYkBleGFtcGxlLmNvbT4NCkNjOiBDYXJvbCBFeGFtcGxlIDxjYXJvbEBleGFtcGxlLmNvbT4NClN1YmplY3Q6IE9sZCBwcm9qZWN0IHN5bmMNCg0KT2xkIHByb2plY3Qgc3luYyBib2R5Lg0K"}
JSON
      ;;
  esac
  exit 0
fi

if [ "$1" = "contacts" ] && [ "$2" = "list" ]; then
  cat <<'JSON'
{"contacts":[{"resource":"people/c1","name":"Alice Example","phone":"+15550101000"},{"resource":"people/c2","name":"Bob Example","phone":""}],"nextPageToken":""}
JSON
  exit 0
fi

exit 1
`
