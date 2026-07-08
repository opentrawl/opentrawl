package birdcrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
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
		"doctor",
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
		"doctor",
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
	req := &crawlkit.Request{Store: rawStore, Paths: crawlkit.Paths{Archive: archivePath}, Format: output.JSON, Out: &out}
	crawler := New()
	records, err := crawler.ShortRefRecords(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := req.RebuildShortRefs(ctx, records); err != nil {
		t.Fatal(err)
	}
	search, err := crawler.Search(ctx, req, crawlkit.Query{Text: "needle", Limit: 10})
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

func fillTestShortRefs(t *testing.T, ctx context.Context, req *crawlkit.Request, hits []crawlkit.Hit) {
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
	return birdcrawlResult{stdout: stdout, stderr: string(stderr), code: code}
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
