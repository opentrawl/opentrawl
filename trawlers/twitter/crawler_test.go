package birdcrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const twitterTestRunSubcommand = "twitter-test-run"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == twitterTestRunSubcommand {
		if os.Getenv("BIRDCRAWL_TEST_DISABLE_NETWORK") == "1" {
			xapiHTTPClient = &http.Client{Transport: &failingXTransport{}}
			xapiBaseURL = "https://offline.invalid"
		}
		os.Exit(trawlkit.Run(os.Args[2:], []trawlkit.Crawler{New()}))
	}
	if len(os.Args) > 1 && os.Args[1] == trawlkit.HiddenWireSubcommand {
		os.Exit(trawlkit.Run(os.Args[1:], []trawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestOpenRecordCallsItsLoaderOnce(t *testing.T) {
	assertOpenRecordLoaderCall(t, "crawler.go", "loadOpenPost")
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

func TestSetupRequirementMapping(t *testing.T) {
	ready := xSetupRequirement(archiveReadinessReady)
	if ready.ID != "archive_import" || ready.Kind != control.SetupKindArchiveImport || ready.State != control.SetupStateReady || ready.Action != control.SetupActionNone || len(ready.Command) != 0 {
		t.Fatalf("ready requirement = %#v", ready)
	}
	missing := xSetupRequirement(archiveReadinessMissing)
	if missing.State != control.SetupStateNeedsAction || missing.Action != control.SetupActionChooseArchive || len(missing.Command) != 0 {
		t.Fatalf("missing requirement = %#v", missing)
	}
	schema := xSetupRequirement(archiveReadinessNeedsSync)
	if schema.State != control.SetupStateReady || schema.Action != control.SetupActionNone || len(schema.Command) != 0 {
		t.Fatalf("schema requirement = %#v", schema)
	}
	invalid := xSetupRequirement(archiveReadinessInvalid)
	if invalid.State != control.SetupStateUnavailable || invalid.Action != control.SetupActionNone || len(invalid.Command) != 0 {
		t.Fatalf("invalid requirement = %#v", invalid)
	}
}

func TestGeneratedManifestListsRunnerVerbs(t *testing.T) {
	stateRoot := stateRootForRun(t)
	out := runBirdcrawl(t, stateRoot, "metadata", "--json")
	var manifest control.Manifest
	if err := json.Unmarshal(out, &manifest); err != nil {
		t.Fatal(err)
	}
	wantCommands := []string{
		"bookmarks",
		"import_archive",
		"likes",
		"mentions",
		"metadata",
		"open",
		"search",
		"spend",
		"stats",
		"status",
		"sync",
		"tweets",
	}
	gotCommands := mapKeys(manifest.Commands)
	if !equalStrings(gotCommands, wantCommands) {
		t.Fatalf("commands = %v, want %v", gotCommands, wantCommands)
	}
	wantCaps := []string{
		"bookmarks",
		"import_archive",
		"likes",
		"mentions",
		"metadata",
		"open",
		"search",
		"short_refs",
		"spend",
		"stats",
		"status",
		"sync",
		"tweets",
	}
	gotCaps := append([]string(nil), manifest.Capabilities...)
	sort.Strings(gotCaps)
	sort.Strings(wantCaps)
	if !equalStrings(gotCaps, wantCaps) {
		t.Fatalf("capabilities = %v, want %v", gotCaps, wantCaps)
	}
	wantConfig := filepath.Join(stateRoot, "twitter", "config.toml")
	if manifest.Paths.DefaultConfig != wantConfig {
		t.Fatalf("default config = %q, want %q", manifest.Paths.DefaultConfig, wantConfig)
	}
	if manifest.Paths.ConfigEnv != "" {
		t.Fatalf("config env survived in manifest: %q", manifest.Paths.ConfigEnv)
	}
	if _, ok := manifest.Commands["version"]; ok {
		t.Fatal("version command survived in manifest")
	}
}

func TestSpendFiguresReachable(t *testing.T) {
	stateRoot := stateRootForRun(t)
	month := time.Now().UTC().Format("2006-01")
	seedSpend(t, stateRoot, month, 2_500_000)
	out := runBirdcrawl(t, stateRoot, "spend", "--json")
	var got spendEnvelope
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Month != month {
		t.Fatalf("month = %q, want %q", got.Month, month)
	}
	if got.SpentUSD != "2.50" || got.MonthlyBudgetUSD != "10.00" || got.RemainingUSD != "7.50" {
		t.Fatalf("spend = %#v, want spent 2.50 cap 10.00 remaining 7.50", got)
	}
}

func TestHandlerUsageErrorExitsTwo(t *testing.T) {
	result := runBirdcrawlRaw(t, stateRootForRun(t), "import", "archive")
	if result.code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, "import archive takes exactly one path") {
		t.Fatalf("stderr missing usage error:\n%s", result.stderr)
	}
}

func TestSharedShortRefsRoundTrip(t *testing.T) {
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "birdcrawl.db")
	rawStore, err := ckstore.Open(ctx, ckstore.Options{Path: archivePath})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rawStore.Close() }()

	st, err := store.Use(ctx, rawStore, nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if _, err := st.ImportArchive(ctx, store.ImportBatch{Tweets: []store.Tweet{{
		ID:           "short-round-trip",
		CreatedAt:    now,
		AuthorID:     "owner",
		AuthorHandle: "example_owner",
		AuthorName:   "Owner Example",
		Text:         "needle tweet for shared short refs",
	}}, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	req := &trawlkit.Request{Store: rawStore, Paths: trawlkit.Paths{Archive: archivePath}, Format: output.JSON, Out: &out}
	crawler := New()
	records, err := crawler.ShortRefRecords(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.AssignShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	search, err := crawler.Search(ctx, req, trawlkit.Query{Text: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	fillTestShortRefs(t, ctx, req, search.Results)
	if len(search.Results) != 1 || search.Results[0].ShortRef == "" {
		t.Fatalf("search results = %#v", search.Results)
	}
	out.Reset()
	if err := crawler.Open(ctx, req, search.Results[0].ShortRef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "needle tweet for shared short refs") {
		t.Fatalf("open output = %s", out.String())
	}

	fullRecord, err := crawler.OpenRecord(ctx, req, search.Results[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	shortRecord, err := crawler.OpenRecord(ctx, req, search.Results[0].ShortRef)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(fullRecord, shortRecord) || shortRecord.OpenRef != search.Results[0].Ref || shortRecord.Data.GetTypeUrl() != "type.googleapis.com/trawl.source.twitter.open.v1.TwitterRecord" || shortRecord.Presentation == nil {
		t.Fatalf("open records full=%#v short=%#v", fullRecord, shortRecord)
	}
	fullValue, err := crawler.handler(ctx, req).loadOpenPost(search.Results[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	shortValue, err := crawler.handler(ctx, req).loadOpenPost(search.Results[0].ShortRef)
	if err != nil {
		t.Fatal(err)
	}
	captureLegacy := func(caseName, ref string) {
		goldens := map[string]string{"json": "315396663e7f92386dad0338d890835a7b60eca866ba55f1e5deb90c783f68c9", "text": "5cf48e9bf39e27151b3e2e1d9dd5e09c2dd63820d13b20df8b895793e848a91d"}
		for _, format := range []struct {
			name  string
			value output.Format
		}{{"json", output.JSON}, {"text", output.Text}} {
			var stdout bytes.Buffer
			legacyReq := *req
			legacyReq.Format, legacyReq.Out = format.value, &stdout
			openErr := crawler.Open(ctx, &legacyReq, ref)
			assertLegacyOpenGolden(t, stdout.Bytes(), openErr, goldens[format.name])
			writeLegacyOpenEvidence(t, "twitter", caseName, format.name, stdout.Bytes(), openErr)
			if openErr != nil {
				t.Fatal(openErr)
			}
		}
	}
	writeRuntimeOpenEvidence(t, "twitter", "full", search.Results[0].Ref, map[string]any{"result": fullValue.result, "aliases": fullValue.aliases, "owner_author_id": fullValue.ownerAuthorID}, fullRecord)
	writeRuntimeOpenEvidence(t, "twitter", "short", search.Results[0].ShortRef, map[string]any{"result": shortValue.result, "aliases": shortValue.aliases, "owner_author_id": shortValue.ownerAuthorID}, shortRecord)
	captureLegacy("full", search.Results[0].Ref)
	captureLegacy("short", search.Results[0].ShortRef)
	_, err = crawler.OpenRecord(ctx, req, "zzzzz")
	var unknown *cliError
	if !errors.As(err, &unknown) || unknown.name != "unknown_short_ref" {
		t.Fatalf("unknown short ref error = %#v", err)
	}
	if _, err := rawStore.DB().ExecContext(ctx, `insert into short_refs(alias, full_ref, canonical_ref) values (?, ?, ?), (?, ?, ?)`, "zzzzz", search.Results[0].Ref, search.Results[0].Ref, "zzzzz", "twitter:tweet/missing", "twitter:tweet/missing"); err != nil {
		t.Fatal(err)
	}
	_, err = crawler.OpenRecord(ctx, req, "zzzzz")
	var ambiguous *cliError
	if !errors.As(err, &ambiguous) || ambiguous.name != "ambiguous_short_ref" {
		t.Fatalf("ambiguous short ref error = %#v", err)
	}
	_, err = crawler.OpenRecord(ctx, req, "photos:asset/example")
	var invalid *cliError
	if !errors.As(err, &invalid) || invalid.name != "invalid_ref" {
		t.Fatalf("foreign ref error = %#v", err)
	}
	_, err = crawler.OpenRecord(ctx, req, "twitter:tweet/missing")
	var missing *cliError
	if !errors.As(err, &missing) || missing.name != "not_found" {
		t.Fatalf("missing tweet error = %#v", err)
	}
	_, err = crawler.OpenRecord(ctx, &trawlkit.Request{Paths: trawlkit.Paths{Archive: archivePath + ".missing"}}, search.Results[0].Ref)
	if err == nil {
		t.Fatal("missing archive open succeeded")
	}
}

func TestDirectVersionVerbRejected(t *testing.T) {
	result := runBirdcrawlRaw(t, stateRootForRun(t), "version")
	if result.code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout:\n%s\nstderr:\n%s", result.code, result.stdout, result.stderr)
	}
	if !strings.Contains(result.stderr, `unknown verb "version"`) {
		t.Fatalf("stderr missing rejected version verb:\n%s", result.stderr)
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

func TestRunnerConfigPathAcceptsExistingBudgetShape(t *testing.T) {
	stateRoot := stateRootForRun(t)
	base := filepath.Join(stateRoot, "twitter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(base, "config.toml")
	if err := os.WriteFile(configPath, []byte("monthly_budget_usd = \"10\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := runBirdcrawl(t, stateRoot, "status", "--json")
	var status control.Status
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatal(err)
	}
	if status.ConfigPath != configPath {
		t.Fatalf("config path = %q, want %q", status.ConfigPath, configPath)
	}
}

type birdcrawlResult struct {
	stdout []byte
	stderr string
	code   int
}

func runBirdcrawl(t *testing.T, stateRoot string, args ...string) []byte {
	t.Helper()
	result := runBirdcrawlRaw(t, stateRoot, args...)
	if result.code != 0 {
		t.Fatalf("birdcrawl %v exited %d\nstdout:\n%s\nstderr:\n%s", args, result.code, result.stdout, result.stderr)
	}
	return result.stdout
}

func runBirdcrawlRaw(t *testing.T, stateRoot string, args ...string) birdcrawlResult {
	t.Helper()
	t.Setenv("HOME", filepath.Dir(stateRoot))
	var stdout, stderr bytes.Buffer
	command := exec.Command(os.Args[0], append([]string{twitterTestRunSubcommand}, args...)...)
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return birdcrawlResult{stdout: stdout.Bytes(), stderr: stderr.String()}
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	return birdcrawlResult{stdout: stdout.Bytes(), stderr: stderr.String(), code: exitErr.ExitCode()}
}

func stateRootForRun(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ".opentrawl")
}

func seedSpend(t *testing.T, stateRoot, month string, micros int64) {
	t.Helper()
	base := filepath.Join(stateRoot, "twitter")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "config.toml"), []byte("monthly_budget_usd = \"10\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), filepath.Join(base, "twitter.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	at := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if err := st.AddSpend(context.Background(), month, micros, at); err != nil {
		t.Fatal(err)
	}
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
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
