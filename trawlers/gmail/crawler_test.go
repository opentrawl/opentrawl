package gogcrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	ckoutput "github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestCrawlerSyncSearchOpenWhoAndContacts(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	stateRoot := t.TempDir()
	paths := crawlkit.Paths{
		Archive: filepath.Join(stateRoot, "gmail", "gmail.db"),
		Config:  filepath.Join(stateRoot, "gmail", "config.toml"),
		Logs:    filepath.Join(stateRoot, "gmail", "logs"),
	}
	source := New()
	source.syncQuery = "project"
	source.syncMax = 25
	source.backupRepoPath = filepath.Join(stateRoot, "gmail", "backup")

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	report, err := source.Sync(ctx, &crawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(crawlkit.Progress) {},
	})
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 3 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v, want 3 added, 0 updated, 0 removed", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	search, err := source.Search(ctx, readRequest(readStore, paths), crawlkit.Query{Text: "project", Limit: 2})
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 3 || !search.Truncated || len(search.Results) != 2 {
		t.Fatalf("search = %#v, want 2 of 3 truncated", search)
	}
	if search.Results[0].Ref != archive.RefPrefix+"m3" || search.Results[0].ShortRef == "" || search.Results[0].Who != "me (alice@example.com)" {
		t.Fatalf("search hit = %#v", search.Results[0])
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	candidates, err := source.Who(ctx, readRequest(readStore, paths), "alice")
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].Who != "me" || candidates[0].Messages != 3 {
		t.Fatalf("who candidates = %#v", candidates)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = source.Open(ctx, &crawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &openOut}, search.Results[0].Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	var opened archive.OpenResult
	if err := json.Unmarshal(openOut.Bytes(), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, openOut.String())
	}
	if opened.ID != "m3" || opened.Headers.ToAddress == "" || opened.Headers.CcAddress == "" {
		t.Fatalf("opened message = %#v", opened)
	}

	contacts, err := source.ContactExport(ctx, &crawlkit.Request{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 1 || contacts.Contacts[0].DisplayName != "Alice Example" || contacts.Contacts[0].PhoneNumbers[0] != "+15550101000" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func TestCrawlerStatusDoctorAndManifestFlags(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	stateRoot := t.TempDir()
	paths := crawlkit.Paths{
		Archive: filepath.Join(stateRoot, "gmail", "gmail.db"),
		Config:  filepath.Join(stateRoot, "gmail", "config.toml"),
		Logs:    filepath.Join(stateRoot, "gmail", "logs"),
	}
	source := New()

	missing, err := source.Status(ctx, &crawlkit.Request{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if missing.State != "missing" || len(missing.Counts) != 3 {
		t.Fatalf("missing status = %#v", missing)
	}
	doctor, err := source.Doctor(ctx, &crawlkit.Request{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(doctor.Checks, "gog_binary", "ok") || !hasCheck(doctor.Checks, "gog_auth", "ok") || !hasCheck(doctor.Checks, "archive", "fail") {
		t.Fatalf("doctor checks = %#v", doctor.Checks)
	}

	flags := map[string]bool{}
	verbs := source.Verbs()
	syncVerb, ok := verbByName(verbs, "sync")
	if len(verbs) != 2 || !ok || syncVerb.Flags == nil {
		t.Fatalf("verbs = %#v", verbs)
	}
	contactsVerb, ok := verbByName(verbs, "contacts_export")
	if !ok || contactsVerb.Store != crawlkit.StoreNone {
		t.Fatalf("contacts_export verb = %#v, ok=%v", contactsVerb, ok)
	}
	fs := flagSet("sync")
	syncVerb.Flags(fs)
	fs.VisitAll(func(f *flag.Flag) {
		flags[f.Name] = true
	})
	for _, name := range []string{"backup-repo", "query", "max"} {
		if !flags[name] {
			t.Fatalf("sync flags = %#v, missing %s", flags, name)
		}
	}
	if source.Info().Config != nil {
		t.Fatalf("gogcrawl should not declare root config: %#v", source.Info().Config)
	}
}

func TestMetadataManifestListsRegisteredVerbs(t *testing.T) {
	stateRoot := stateRootForRun(t)
	code, stdout, stderr := runGogcrawl(t, stateRoot, "metadata", "--json")
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("metadata JSON: %v\n%s", err, stdout)
	}
	wantCommands := []string{"contacts_export", "doctor", "metadata", "open", "search", "status", "sync", "who"}
	if got := sortedKeys(manifest.Commands); !equalStrings(got, wantCommands) {
		t.Fatalf("commands = %v, want %v", got, wantCommands)
	}
	wantCaps := []string{"contacts_export", "doctor", "metadata", "open", "search", "short_refs", "status", "sync", "who"}
	gotCaps := append([]string(nil), manifest.Capabilities...)
	sort.Strings(gotCaps)
	sort.Strings(wantCaps)
	if !equalStrings(gotCaps, wantCaps) {
		t.Fatalf("capabilities = %v, want %v", gotCaps, wantCaps)
	}
	if got := manifest.Commands["contacts_export"].Store; got != "none" {
		t.Fatalf("contacts_export store = %q, want none", got)
	}
	if got := manifest.Commands["sync"].Store; got != "write" {
		t.Fatalf("sync store = %q, want write", got)
	}
	if _, ok := manifest.Commands["version"]; ok {
		t.Fatal("version command survived in manifest")
	}
}

func TestRunContactsExportStoreNoneFreshNoArchive(t *testing.T) {
	installFakeGog(t)
	stateRoot := stateRootForRun(t)
	archivePath := archivePathForRun(stateRoot)
	code, stdout, stderr := runGogcrawl(t, stateRoot, "contacts", "export", "--json")
	if code != 0 {
		t.Fatalf("contacts export code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var export control.ContactExport
	if err := json.Unmarshal([]byte(stdout), &export); err != nil {
		t.Fatalf("contacts export JSON: %v\n%s", err, stdout)
	}
	if len(export.Contacts) != 1 || export.Contacts[0].DisplayName != "Alice Example" || export.Contacts[0].PhoneNumbers[0] != "+15550101000" {
		t.Fatalf("contacts = %#v", export.Contacts)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("contacts export created archive: err=%v path=%s", err, archivePath)
	}
	t.Logf("fresh contacts export archive absent at resolved state root: path=%s", archivePath)
}

func TestRunSyncCreatesArchiveAtResolvedStateRoot(t *testing.T) {
	installFakeGog(t)
	stateRoot := stateRootForRun(t)
	archivePath := archivePathForRun(stateRoot)
	code, stdout, stderr := runGogcrawl(t, stateRoot, "sync", "--query", "project", "--max", "25", "--json")
	if code != 0 {
		t.Fatalf("sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("sync archive missing: err=%v path=%s", err, archivePath)
	}
	t.Logf("sync archive exists at resolved state root: path=%s", archivePath)
}

func readRequest(st *ckstore.Store, paths crawlkit.Paths) *crawlkit.Request {
	return &crawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
}

func openReadStore(t *testing.T, ctx context.Context, path string) *ckstore.Store {
	t.Helper()
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func hasCheck(checks []crawlkit.Check, id, state string) bool {
	for _, check := range checks {
		if check.ID == id && check.State == state {
			return true
		}
	}
	return false
}

func verbByName(verbs []crawlkit.Verb, name string) (crawlkit.Verb, bool) {
	for _, verb := range verbs {
		if verb.Name == name {
			return verb, true
		}
	}
	return crawlkit.Verb{}, false
}

func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	return fs
}

func runGogcrawl(t *testing.T, stateRoot string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", filepath.Dir(stateRoot))
	oldStdout := os.Stdout
	oldStderr := os.Stderr
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()
	code := crawlkit.Run(args, []crawlkit.Crawler{New()})
	if err := stdoutW.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdoutR.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrR.Close(); err != nil {
		t.Fatal(err)
	}
	return code, string(stdout), string(stderr)
}

func stateRootForRun(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".opentrawl")
}

func archivePathForRun(stateRoot string) string {
	return filepath.Join(stateRoot, "gmail", "gmail.db")
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func installFakeGog(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	path := filepath.Join(dir, "gog")
	if err := os.WriteFile(path, []byte(fakeGogScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOG_FAKE_LOG", log)
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
  expires="${GOG_FAKE_AUTH_EXPIRES:-2030-01-02T03:04:05Z}"
  valid="${GOG_FAKE_AUTH_VALID:-true}"
  printf 'alice@example.com\tmain\tgmail\t%s\t%s\t\toauth\n' "$expires" "$valid"
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
