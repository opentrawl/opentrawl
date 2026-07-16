package gogcrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const gmailTestRunSubcommand = "gmail-test-run"

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "open_record.go", "loadOpenMessage")
}

func assertOpenRecordLoaderCall(t *testing.T, path, loader string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil || function.Name.Name != "OpenRecord" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == loader {
				calls++
			}
			return true
		})
	}
	if calls != 1 {
		t.Fatalf("OpenRecord %s calls = %d, want 1", loader, calls)
	}
}

func TestStatusUsesOnlyArchiveState(t *testing.T) {
	crawler := New()
	crawler.gog.Binary = filepath.Join(t.TempDir(), "missing-gog")
	status, err := crawler.Status(context.Background(), &trawlkit.Request{Paths: trawlkit.Paths{Archive: filepath.Join(t.TempDir(), "gmail.db")}})
	if err != nil || status.State != "missing" || len(status.SetupRequirements) != 0 {
		t.Fatalf("archive-only status = %#v, %v", status, err)
	}
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == gmailTestRunSubcommand {
		os.Exit(trawlkit.Run(os.Args[2:], []trawlkit.Crawler{New()}))
	}
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		os.Exit(trawlkit.Run(os.Args[1:], []trawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestCrawlerSyncSearchOpenWhoAndContacts(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	stateRoot := t.TempDir()
	paths := trawlkit.Paths{
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
	syncReq := &trawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(trawlkit.Progress) {},
	}
	report, err := source.Sync(ctx, syncReq)
	if err == nil {
		records, recordsErr := source.ShortRefRecords(ctx, syncReq)
		if recordsErr != nil {
			err = recordsErr
		} else if _, assignErr := syncReq.AssignShortRefs(ctx, records); assignErr != nil {
			err = assignErr
		}
	}
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
	searchReq := readRequest(readStore, paths)
	search, err := source.Search(ctx, searchReq, trawlkit.Query{Text: "project", Limit: 2})
	fillTestShortRefs(t, ctx, searchReq, search.Results)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 3 || !search.Truncated || len(search.Results) != 2 {
		t.Fatalf("search = %#v, want 2 of 3 truncated", search)
	}
	hit := search.Results[0]
	if hit.Ref != archive.RefPrefix+"m3" || hit.ShortRef == "" || hit.AnchorID != "subject" || hit.Summary.Title == "" || hit.Summary.Subtitle != "me (alice@example.com)" {
		t.Fatalf("search hit = %#v", search.Results[0])
	}
	if len(hit.Evidence) == 0 || hit.Evidence[0].Label != "Subject" || hit.Evidence[0].Field == nil || hit.Evidence[0].Field.Name != "subject" || !hasMatchedRun(hit.Evidence[0].Field.Value) {
		t.Fatalf("search evidence = %#v", hit.Evidence)
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
	err = source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &openOut}, search.Results[0].Ref)
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

	readStore = openReadStore(t, ctx, paths.Archive)
	fullRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, search.Results[0].Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	readStore = openReadStore(t, ctx, paths.Archive)
	shortRecord, err := source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, search.Results[0].ShortRef)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != search.Results[0].Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.gmail.open.v1.GmailRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	load := func(ref string) archive.OpenResult {
		readStore = openReadStore(t, ctx, paths.Archive)
		value, loadErr := source.loadOpenMessage(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		return value
	}
	captureLegacy := func(caseName, ref string) {
		goldens := map[string]string{"json": "e60f0336ff1674d0973d371d58c9336df023a99033c40799af322a3f04eef94d", "text": "f7511db26391841b66969110bc676096a71bf3add0cda0f5e0e6143ae2e3c997"}
		for _, format := range []struct {
			name  string
			value ckoutput.Format
		}{{"json", ckoutput.JSON}, {"text", ckoutput.Text}} {
			readStore = openReadStore(t, ctx, paths.Archive)
			var stdout bytes.Buffer
			openErr := source.Open(ctx, &trawlkit.Request{Store: readStore, Paths: paths, Format: format.value, Out: &stdout}, ref)
			_ = readStore.Close()
			assertLegacyOpenGolden(t, stdout.Bytes(), openErr, goldens[format.name])
			writeLegacyOpenEvidence(t, "gmail", caseName, format.name, stdout.Bytes(), openErr)
			if openErr != nil {
				t.Fatal(openErr)
			}
		}
	}
	writeRuntimeOpenEvidence(t, "gmail", "full", search.Results[0].Ref, load(search.Results[0].Ref), fullRecord)
	writeRuntimeOpenEvidence(t, "gmail", "short", search.Results[0].ShortRef, load(search.Results[0].ShortRef), shortRecord)
	captureLegacy("full", search.Results[0].Ref)
	captureLegacy("short", search.Results[0].ShortRef)
	assertOpenRecordError := func(ref, want string) {
		readStore = openReadStore(t, ctx, paths.Archive)
		_, err = source.OpenRecord(ctx, &trawlkit.Request{Store: readStore, Paths: paths}, ref)
		_ = readStore.Close()
		var typed commandError
		if !errors.As(err, &typed) || typed.name != want {
			t.Fatalf("open %q error = %#v, want %q", ref, err, want)
		}
	}
	assertOpenRecordError("zzzzz", "unknown_short_ref")
	writeStore, err = ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", search.Results[0].Ref, search.Results[0].Ref, "zzzzz", archive.RefPrefix+"missing", archive.RefPrefix+"missing"); err != nil {
		_ = writeStore.Close()
		t.Fatal(err)
	}
	if err := writeStore.Close(); err != nil {
		t.Fatal(err)
	}
	assertOpenRecordError("zzzzz", "ambiguous_short_ref")
	assertOpenRecordError("calendar:event/example", "message_not_found")
	assertOpenRecordError("gmail:msg/", "message_not_found")
	assertOpenRecordError("gmail:msg/missing", "message_not_found")
	_, err = source.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: paths.Archive + ".missing"}}, search.Results[0].Ref)
	var archiveFailure commandError
	if !errors.As(err, &archiveFailure) || archiveFailure.name != "archive_missing" {
		t.Fatalf("missing archive error = %#v", err)
	}

	contacts, err := source.ContactExport(ctx, &trawlkit.Request{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 1 || contacts.Contacts[0].DisplayName != "Alice Example" || contacts.Contacts[0].PhoneNumbers[0] != "+15550101000" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func hasMatchedRun(runs []trawlkit.TextRun) bool {
	for _, run := range runs {
		if run.Matched {
			return true
		}
	}
	return false
}

func TestCrawlerStatusAndManifestFlags(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	stateRoot := t.TempDir()
	paths := trawlkit.Paths{
		Archive: filepath.Join(stateRoot, "gmail", "gmail.db"),
		Config:  filepath.Join(stateRoot, "gmail", "config.toml"),
		Logs:    filepath.Join(stateRoot, "gmail", "logs"),
	}
	source := New()

	missing, err := source.Status(ctx, &trawlkit.Request{Paths: paths})
	if err != nil {
		t.Fatal(err)
	}
	if missing.State != "missing" || len(missing.Counts) != 4 {
		t.Fatalf("missing status = %#v", missing)
	}
	flags := map[string]bool{}
	verbs := source.Verbs()
	syncVerb, ok := verbByName(verbs, "sync")
	if len(verbs) != 2 || !ok || syncVerb.Flags == nil {
		t.Fatalf("verbs = %#v", verbs)
	}
	contactsVerb, ok := verbByName(verbs, "contacts_export")
	if !ok || contactsVerb.Store != trawlkit.StoreNone {
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
	wantCommands := []string{"contacts_export", "metadata", "open", "search", "status", "sync", "who"}
	if got := sortedKeys(manifest.Commands); !equalStrings(got, wantCommands) {
		t.Fatalf("commands = %v, want %v", got, wantCommands)
	}
	wantCaps := []string{"contacts_export", "metadata", "open", "search", "short_refs", "status", "sync", "who"}
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

func readRequest(st *ckstore.Store, paths trawlkit.Paths) *trawlkit.Request {
	return &trawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
}

func fillTestShortRefs(t *testing.T, ctx context.Context, req *trawlkit.Request, hits []trawlkit.Hit) {
	t.Helper()
	refs := make([]string, 0, len(hits))
	for _, hit := range hits {
		refs = append(refs, hit.Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	for i := range hits {
		hits[i].ShortRef = aliases[hits[i].Ref]
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

func verbByName(verbs []trawlkit.Verb, name string) (trawlkit.Verb, bool) {
	for _, verb := range verbs {
		if verb.Name == name {
			return verb, true
		}
	}
	return trawlkit.Verb{}, false
}

func flagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	return fs
}

func runGogcrawl(t *testing.T, stateRoot string, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("HOME", filepath.Dir(stateRoot))
	var stdout, stderr bytes.Buffer
	command := exec.Command(os.Args[0], append([]string{gmailTestRunSubcommand}, args...)...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return 0, stdout.String(), stderr.String()
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return exitErr.ExitCode(), stdout.String(), stderr.String()
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
