package crawlkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/openclaw/crawlkit/whomatch"
)

type testCrawler struct {
	id       string
	surface  string
	shortRef bool
	cfg      *testConfig
	verbs    []Verb
	statusFn func(context.Context, *Request) (*control.Status, error)
	doctorFn func(context.Context, *Request) (*Doctor, error)
	searchFn func(context.Context, *Request, Query) (SearchResult, error)
	whoFn    func(context.Context, *Request, string) ([]whomatch.Candidate, error)
	syncFn   func(context.Context, *Request) (*SyncReport, error)
}

type testStatusCrawler struct {
	verbs []Verb
}

type testConfig struct {
	Required string `toml:"required"`
}

func (c *testConfig) Validate() error {
	if c.Required == "bad" {
		return ConfigFieldError{
			Field: "required",
			Fix:   "set required to ok",
			Err:   errors.New("required must be ok"),
		}
	}
	return nil
}

func (c *testCrawler) Info() Info {
	id := c.id
	if id == "" {
		id = "testcrawl"
	}
	info := Info{
		ID:          id,
		Surface:     firstText(c.surface, "test"),
		DisplayName: "Test",
		Description: "Synthetic test crawler.",
		ShortRefs:   c.shortRef,
	}
	if c.cfg != nil {
		info.Config = c.cfg
	}
	return info
}

func (c *testCrawler) Status(ctx context.Context, req *Request) (*control.Status, error) {
	if c.statusFn != nil {
		return c.statusFn(ctx, req)
	}
	status := control.NewStatus(c.Info().ID, "ready")
	status.State = "ok"
	return &status, nil
}

func (c *testCrawler) Doctor(ctx context.Context, req *Request) (*Doctor, error) {
	if c.doctorFn != nil {
		return c.doctorFn(ctx, req)
	}
	return &Doctor{Checks: []Check{{ID: "archive", State: "ok", Message: "archive readable"}}}, nil
}

func (c *testCrawler) Verbs() []Verb {
	return c.verbs
}

func (c *testCrawler) Search(ctx context.Context, req *Request, q Query) (SearchResult, error) {
	if c.searchFn != nil {
		return c.searchFn(ctx, req, q)
	}
	results := []Hit{{Ref: c.Info().ID + ":1", Time: time.Unix(0, 0).UTC(), Snippet: q.Text}}
	return SearchResult{Results: results, TotalMatches: len(results)}, nil
}

func (c *testCrawler) Who(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
	if c.whoFn != nil {
		return c.whoFn(ctx, req, person)
	}
	return []whomatch.Candidate{{
		Who:         "Ada Example",
		Identifiers: []string{"ada@example.com"},
		LastSeen:    time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		Messages:    2,
	}}, nil
}

func (c *testCrawler) Sync(ctx context.Context, req *Request) (*SyncReport, error) {
	if c.syncFn != nil {
		return c.syncFn(ctx, req)
	}
	return &SyncReport{Added: 1}, nil
}

func (c *testStatusCrawler) Info() Info {
	return Info{ID: "testcrawl", Surface: "test", DisplayName: "Test"}
}

func (c *testStatusCrawler) Status(ctx context.Context, req *Request) (*control.Status, error) {
	status := control.NewStatus(c.Info().ID, "ready")
	status.State = "ok"
	return &status, nil
}

func (c *testStatusCrawler) Doctor(ctx context.Context, req *Request) (*Doctor, error) {
	return &Doctor{Checks: []Check{{ID: "archive", State: "ok", Message: "archive readable"}}}, nil
}

func (c *testStatusCrawler) Verbs() []Verb {
	return c.verbs
}

func TestRunMetadataGeneratesManifestWithFlagsAndStateRoot(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{
		verbs: []Verb{{
			Name:    "archive import",
			Help:    "Import an archive.",
			Args:    []string{"PATH"},
			Mutates: true,
		}},
	}
	source.verbs[0].Flags = func(fs *flag.FlagSet) {
		fs.String("path", "", "archive path")
	}
	code, stdout, _ := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("code = %d stdout=%s", code, stdout)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	if manifest.SchemaVersion != 2 {
		t.Fatalf("schema_version = %d", manifest.SchemaVersion)
	}
	if manifest.Paths.DefaultDatabase != filepath.Join(stateRoot, "testcrawl", "testcrawl.db") {
		t.Fatalf("default database = %q", manifest.Paths.DefaultDatabase)
	}
	if _, err := os.Stat(filepath.Join(stateRoot, "testcrawl", "logs", "current.log")); !os.IsNotExist(err) {
		t.Fatalf("metadata wrote a run log: err=%v", err)
	}
	cmd := manifest.Commands["archive_import"]
	if !cmd.Mutates || len(cmd.Flags) != 1 || cmd.Flags[0].Name != "path" || cmd.Flags[0].Usage != "archive path" {
		t.Fatalf("archive_import command = %#v", cmd)
	}
	searchFlags := map[string]string{}
	for _, flag := range manifest.Commands["search"].Flags {
		searchFlags[flag.Name] = flag.Default
	}
	for _, name := range []string{"after", "before", "limit", "who"} {
		if _, ok := searchFlags[name]; !ok {
			t.Fatalf("search flag %q missing from manifest: %#v", name, manifest.Commands["search"].Flags)
		}
	}
	if searchFlags["limit"] != "20" {
		t.Fatalf("search limit default = %q", searchFlags["limit"])
	}
	if slices.Contains(manifest.Capabilities, "short_refs") {
		t.Fatalf("capabilities unexpectedly include short_refs: %#v", manifest.Capabilities)
	}
	if got, want := strings.Join(cmd.Argv[1:], " "), "archive import PATH --json --state-root "+stateRoot; got != want {
		t.Fatalf("archive_import argv suffix = %q, want %q", got, want)
	}
	for name, command := range manifest.Commands {
		if !argvHasStateRoot(command.Argv, stateRoot) {
			t.Fatalf("command %q argv does not pin state root: %#v", name, command.Argv)
		}
	}
}

func TestRunMetadataAdvertisesDeclaredShortRefs(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{shortRef: true}
	code, stdout, stderr := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	if !slices.Contains(manifest.Capabilities, "short_refs") {
		t.Fatalf("capabilities missing short_refs: %#v", manifest.Capabilities)
	}
}

func TestRunConfigDecodeValidateAndJSONEnvelope(t *testing.T) {
	stateRoot := t.TempDir()
	cfgDir := filepath.Join(stateRoot, "testcrawl")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("required = \"bad\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &testCrawler{cfg: &testConfig{}}
	code, stdout, _ := runForTest([]string{"status", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 {
		t.Fatalf("code = %d stdout=%s", code, stdout)
	}
	var envelope ckoutput.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("error envelope = %s err=%v", stdout, err)
	}
	if envelope.Error.Code != "config_invalid" || !strings.Contains(envelope.Error.Message, "required") || envelope.Error.Remedy != "set required to ok" {
		t.Fatalf("error envelope = %#v", envelope.Error)
	}
	var raw map[string]map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("raw error envelope = %s err=%v", stdout, err)
	}
	if raw["error"]["field"] != "required" {
		t.Fatalf("config field was not rendered in envelope: %s", stdout)
	}
	if source.cfg.Required != "bad" {
		t.Fatalf("config was not decoded: %#v", source.cfg)
	}
	logData, err := os.ReadFile(filepath.Join(stateRoot, "testcrawl", "logs", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "run_failed") || !strings.Contains(string(logData), "finish: outcome=error") {
		t.Fatalf("config failure was not recorded in the run log:\n%s", string(logData))
	}
}

func TestRunDBOpenNilStoreAndReadOnly(t *testing.T) {
	stateRoot := t.TempDir()
	statusSawNil := false
	statusSawLog := false
	source := &testCrawler{
		statusFn: func(ctx context.Context, req *Request) (*control.Status, error) {
			statusSawNil = req.Store == nil
			statusSawLog = req.Log != nil
			status := control.NewStatus("testcrawl", "not synced yet")
			status.State = "missing"
			return &status, nil
		},
	}
	code, stdout, _ := runForTest([]string{"status", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 || !statusSawNil || !statusSawLog {
		t.Fatalf("status code=%d sawNil=%t sawLog=%t stdout=%s", code, statusSawNil, statusSawLog, stdout)
	}
	code, stdout, _ = runForTest([]string{"search", "--json", "--state-root", stateRoot, "hello"}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "archive does not exist") {
		t.Fatalf("missing archive code=%d stdout=%s", code, stdout)
	}

	createArchive(t, stateRoot)
	source.searchFn = func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		if req.Store == nil {
			t.Fatal("search Store is nil")
		}
		if _, err := req.Store.DB().ExecContext(ctx, `create table should_fail(id text)`); err == nil {
			t.Fatal("read store accepted a write")
		}
		results := []Hit{{Ref: "testcrawl:1", Time: time.Unix(0, 0).UTC(), Snippet: q.Text}}
		return SearchResult{Results: results, TotalMatches: len(results)}, nil
	}
	code, stdout, _ = runForTest([]string{"search", "--json", "--state-root", stateRoot, "hello"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, `"results"`) {
		t.Fatalf("search code=%d stdout=%s", code, stdout)
	}
}

func TestRunSearchJSONEnvelopeAndFlags(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	var got Query
	source := &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		got = q
		results := []Hit{{Ref: "testcrawl:1", Time: time.Unix(0, 0).UTC(), Snippet: q.Text}}
		return SearchResult{Results: results, TotalMatches: 12, Truncated: true}, nil
	}}
	code, stdout, stderr := runForTest([]string{"search", "--json", "--state-root", stateRoot, "hello"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("default search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "hello" || got.Limit != 20 {
		t.Fatalf("default query = %#v", got)
	}
	var envelope struct {
		Query        string `json:"query"`
		Results      []Hit  `json:"results"`
		TotalMatches int    `json:"total_matches"`
		Truncated    bool   `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	if envelope.Query != "hello" || envelope.TotalMatches != 12 || !envelope.Truncated || len(envelope.Results) != 1 {
		t.Fatalf("search envelope = %#v", envelope)
	}

	code, stdout, stderr = runForTest([]string{"search", "needle", "--json", "--limit", "3", "--after", "2026-01-01T00:00:00Z", "--before", "2026-01-31T00:00:00Z", "--who", "Ada", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("flagged search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "needle" || got.Limit != 3 || got.Who != "Ada" || got.After.Format(time.RFC3339) != "2026-01-01T00:00:00Z" || got.Before.Format(time.RFC3339) != "2026-01-31T00:00:00Z" {
		t.Fatalf("flagged query = %#v", got)
	}

	func() {
		previousLocal := time.Local
		fixedLocal := time.FixedZone("UTC+2", 2*60*60)
		time.Local = fixedLocal
		defer func() { time.Local = previousLocal }()

		code, stdout, stderr = runForTest([]string{"search", "needle", "--json", "--before", "2026-01-31", "--state-root", stateRoot}, source, runOptions{})
		if code != 0 {
			t.Fatalf("date-only before search code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		wantBefore := time.Date(2026, 1, 31, 23, 59, 59, 0, fixedLocal).UTC().Format(time.RFC3339)
		if got.Before.Format(time.RFC3339) != wantBefore {
			t.Fatalf("date-only before = %s, want %s", got.Before.Format(time.RFC3339), wantBefore)
		}
	}()

	code, stdout, stderr = runForTest([]string{"search", "needle", "--json", "--all", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("all search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "needle" || got.Limit != 0 {
		t.Fatalf("all query = %#v", got)
	}

	code, stdout, stderr = runForTest([]string{"search", "--json", "--who", "Ada", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("filter-only search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "" || got.Who != "Ada" {
		t.Fatalf("filter-only query = %#v", got)
	}

	code, stdout, stderr = runForTest([]string{"search", "needle", "--json", "--who", "", "--state-root", stateRoot}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "search --who requires an identity") || stderr != "" {
		t.Fatalf("empty who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"search", "needle", "--json", "--limit", "0", "--state-root", stateRoot}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "--limit must be at least 1") || stderr != "" {
		t.Fatalf("bad limit code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"search", "--json", "--state-root", stateRoot, "--", "--help"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("dash query search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "--help" {
		t.Fatalf("dash query = %#v", got)
	}

	code, stdout, stderr = runForTest([]string{"search", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "search needs a query or filter") || stderr != "" {
		t.Fatalf("empty search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunSearchTextShowsTruncationSummary(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		return SearchResult{
			Results:      []Hit{{Ref: "testcrawl:1", Time: time.Unix(0, 0).UTC(), Snippet: q.Text}},
			TotalMatches: 3,
			Truncated:    true,
		}, nil
	}}
	code, stdout, stderr := runForTest([]string{"search", "--state-root", stateRoot, "hello"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("search text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		`Search "hello": showing 1 of 3.`,
		"Open: testcrawl open REF",
		"Narrow results with --who, --after, or --before.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("search text missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunSyncJSONIncludesZeroCounts(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	opts := childTestOptions(t, "zero_sync")
	code, stdout, stderr := runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	if code != 0 {
		t.Fatalf("zero sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{`"added": 0`, `"updated": 0`, `"removed": 0`} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("zero sync JSON missing %s:\n%s", want, stdout)
		}
	}
}

func TestRunSpineSyncVerbParsesDeclaredFlags(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	includeArchived := false
	backupRepo := ""
	source := &testCrawler{verbs: []Verb{{
		Name: "sync",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&includeArchived, "include-archived", false, "include archived items")
			fs.StringVar(&backupRepo, "backup-repo", "", "backup repository")
		},
	}}}
	opts := childTestOptions(t, "sync_flag")
	code, stdout, stderr := runForTest([]string{"sync", "--include-archived", "--backup-repo", "synthetic", "--json", "--state-root", stateRoot}, source, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 7`) {
		t.Fatalf("sync flag code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunMetadataAdvertisesSpineVerbFlags(t *testing.T) {
	stateRoot := t.TempDir()
	includeArchived := false
	backupRepo := ""
	source := &testCrawler{verbs: []Verb{{
		Name: "sync",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&includeArchived, "include-archived", false, "include archived items")
			fs.StringVar(&backupRepo, "backup-repo", "", "backup repository")
		},
	}}}
	code, stdout, stderr := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	syncCommand := manifest.Commands["sync"]
	if syncCommand.Title != "Sync" || !syncCommand.Mutates || len(syncCommand.Flags) != 2 {
		t.Fatalf("sync command = %#v", syncCommand)
	}
	flags := map[string]control.Flag{}
	for _, flag := range syncCommand.Flags {
		flags[flag.Name] = flag
	}
	if flag := flags["include-archived"]; flag.Usage != "include archived items" || flag.Default != "false" {
		t.Fatalf("sync bool flag = %#v", flag)
	}
	if flag := flags["backup-repo"]; flag.Usage != "backup repository" || flag.Default != "" {
		t.Fatalf("sync string flag = %#v", flag)
	}
	if got, want := strings.Join(syncCommand.Argv[1:], " "), "sync --json --state-root "+stateRoot; got != want {
		t.Fatalf("sync argv suffix = %q, want %q", got, want)
	}
}

func TestRunSpineSearchVerbPreservesPositionalQuery(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	exact := false
	var got Query
	source := &testCrawler{
		verbs: []Verb{{
			Name: "search",
			Flags: func(fs *flag.FlagSet) {
				fs.BoolVar(&exact, "exact", false, "match exactly")
			},
		}},
		searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
			got = q
			return SearchResult{Results: []Hit{{Ref: "testcrawl:1", Time: time.Unix(0, 0).UTC(), Snippet: q.Text}}, TotalMatches: 1}, nil
		},
	}
	code, stdout, stderr := runForTest([]string{"search", "exact", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 || got.Text != "exact" || exact {
		t.Fatalf("bare query code=%d exact=%t query=%#v stdout=%s stderr=%s", code, exact, got, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"search", "needle", "--exact", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 || got.Text != "needle" || !exact {
		t.Fatalf("flagged query code=%d exact=%t query=%#v stdout=%s stderr=%s", code, exact, got, stdout, stderr)
	}

	exact = false
	got = Query{}
	code, stdout, stderr = runForTest([]string{"search", "--exact", "--json", "--state-root", stateRoot, "--", "--literal"}, source, runOptions{})
	if code != 0 || got.Text != "--literal" || !exact {
		t.Fatalf("dash query code=%d exact=%t query=%#v stdout=%s stderr=%s", code, exact, got, stdout, stderr)
	}
}

func TestRunSpineWhoVerbDropsDelimiterBeforeArityCheck(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	exact := false
	var gotPerson string
	source := &testCrawler{
		verbs: []Verb{{
			Name: "who",
			Flags: func(fs *flag.FlagSet) {
				fs.BoolVar(&exact, "exact", false, "match exactly")
			},
		}},
		whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
			gotPerson = person
			return []whomatch.Candidate{{Who: "Ada Example", Identifiers: []string{"ada@example.com"}}}, nil
		},
	}
	code, stdout, stderr := runForTest([]string{"who", "--exact", "--json", "--state-root", stateRoot, "--", "Ada"}, source, runOptions{})
	if code != 0 || gotPerson != "Ada" || !exact {
		t.Fatalf("who delimiter code=%d exact=%t person=%q stdout=%s stderr=%s", code, exact, gotPerson, stdout, stderr)
	}
}

func TestRunRejectsIllegalSpineVerbDeclaration(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{
		Name:    "sync",
		Help:    "Sync archived items.",
		Args:    []string{"PATH"},
		Mutates: true,
		Timeout: time.Second,
		Run: func(ctx context.Context, req *Request) error {
			return nil
		},
	}}}
	wantMessage := "invalid sync Verb declaration"
	wantDetail := "spine verb declarations may only set Name and Flags"
	wantRemedy := "Remove Help, Run, Mutates, Timeout, and Args from the sync Verb declaration."

	code, stdout, stderr := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantDetail) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("metadata invalid declaration code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"sync", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantDetail) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("sync invalid declaration code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"status", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stdout != "" || !strings.Contains(stderr, wantMessage) || !strings.Contains(stderr, wantDetail) || !strings.Contains(stderr, wantRemedy) {
		t.Fatalf("status invalid declaration code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunRejectsSpineVerbFlagCollision(t *testing.T) {
	stateRoot := t.TempDir()
	cases := []struct {
		name        string
		source      Crawler
		args        []string
		wantMessage string
		wantRemedy  string
	}{
		{
			name: "search flag",
			source: &testCrawler{verbs: []Verb{{
				Name: "search",
				Flags: func(fs *flag.FlagSet) {
					fs.Int("limit", 99, "crawler-owned limit")
				},
			}}},
			args:        []string{"search", "needle", "--json", "--state-root", stateRoot},
			wantMessage: "crawler flag --limit collides with a runner-owned flag",
			wantRemedy:  "Remove --limit from the search Verb declaration; the runner owns that flag.",
		},
		{
			name: "global flag",
			source: &testCrawler{verbs: []Verb{{
				Name: "sync",
				Flags: func(fs *flag.FlagSet) {
					fs.Bool("json", false, "crawler-owned JSON mode")
				},
			}}},
			args:        []string{"sync", "--json", "--state-root", stateRoot},
			wantMessage: "crawler flag --json collides with a runner-owned flag",
			wantRemedy:  "Remove --json from the sync Verb declaration; the runner owns that flag.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, tc.source, runOptions{})
			if code != 1 || stderr != "" || !strings.Contains(stdout, tc.wantMessage) || !strings.Contains(stdout, tc.wantRemedy) {
				t.Fatalf("metadata collision code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}

			code, stdout, stderr = runForTest(tc.args, tc.source, runOptions{})
			if code != 1 || stderr != "" || !strings.Contains(stdout, tc.wantMessage) || !strings.Contains(stdout, tc.wantRemedy) {
				t.Fatalf("invocation collision code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
}

func TestRunUnsupportedSpineVerbDeclarationKeepsInvocationUsageError(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testStatusCrawler{verbs: []Verb{{Name: "sync"}}}

	code, stdout, stderr := runForTest([]string{"sync", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 2 || stderr != "" || !strings.Contains(stdout, "source does not support sync") || strings.Contains(stdout, "invalid sync Verb declaration") {
		t.Fatalf("unsupported sync invocation code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, "invalid sync Verb declaration: source does not implement Syncer") || !strings.Contains(stdout, "Implement crawlkit.Syncer or remove the sync Verb declaration.") {
		t.Fatalf("unsupported sync metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunRejectsDuplicateSpineVerbDeclaration(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{Name: "sync"}, {Name: "sync"}}}
	wantMessage := "invalid sync Verb declaration: declared more than once"
	wantRemedy := "Keep one sync Verb declaration and remove the duplicate."

	code, stdout, stderr := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("duplicate metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTest([]string{"sync", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("duplicate sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunStatusTextRendersEveryDeclaredCount(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{statusFn: func(ctx context.Context, req *Request) (*control.Status, error) {
		status := control.NewStatus("testcrawl", "ready")
		status.State = "ok"
		status.Counts = []control.Count{
			control.NewCount("events", "events", 12),
			control.NewCount("calendars", "calendars", 2),
			control.NewCount("since", "since", 2018),
		}
		return &status, nil
	}}
	code, stdout, stderr := runForTest([]string{"status", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"Events: 12", "Calendars: 2", "Since: 2018"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status text missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunWhoTextUsesNeutralCountHeader(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
		return []whomatch.Candidate{{
			Who:         "Ada Example",
			Identifiers: []string{"ada@example.com"},
			LastSeen:    time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
			Messages:    3,
		}}, nil
	}}
	code, stdout, stderr := runForTest([]string{"who", "Ada", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "items") || strings.Contains(stdout, "events") {
		t.Fatalf("who text count header is not neutral:\n%s", stdout)
	}
}

func TestWriteContactsTextUsesGenericHeading(t *testing.T) {
	var buf bytes.Buffer
	contacts := &control.ContactExport{Contacts: []control.Contact{{
		DisplayName:  "Ada Example",
		PhoneNumbers: []string{"+15550100"},
	}}}
	if err := writeResult(&buf, ckoutput.Text, "contacts", contacts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Contacts: showing 1 of 1.") || strings.Contains(buf.String(), "with phone numbers") {
		t.Fatalf("contacts text is not generic:\n%s", buf.String())
	}
}

func TestWriteResultNormalizesEmptyJSONArrays(t *testing.T) {
	cases := []struct {
		name  string
		label string
		value any
		key   string
	}{
		{name: "search", label: "search", value: searchOutput{Query: "empty"}, key: "results"},
		{name: "doctor", label: "doctor", value: &Doctor{}, key: "checks"},
		{name: "contacts", label: "contacts", value: (*control.ContactExport)(nil), key: "contacts"},
		{name: "who", label: "who", value: whoOutput{Query: "Ada"}, key: "candidates"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := writeResult(&buf, ckoutput.JSON, tc.label, tc.value); err != nil {
				t.Fatal(err)
			}
			var raw map[string]any
			if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
				t.Fatalf("%s json = %s err=%v", tc.name, buf.String(), err)
			}
			if _, ok := raw[tc.key].([]any); !ok {
				t.Fatalf("%s %q is not an array: %s", tc.name, tc.key, buf.String())
			}
		})
	}
}

func TestRunAmbiguousSourceIsUsageError(t *testing.T) {
	stateRoot := t.TempDir()
	code, stdout, stderr := runSourcesForTest([]string{"shared", "status", "--json", "--state-root", stateRoot}, []Crawler{
		&testCrawler{id: "one", surface: "shared"},
		&testCrawler{id: "two", surface: "shared"},
	}, runOptions{})
	if code != 2 || stderr != "" || !strings.Contains(stdout, "ambiguous source") || !strings.Contains(stdout, "one, two") {
		t.Fatalf("ambiguous source code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunSearchWhoResolutionJSONContract(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	var got Query
	source := &testCrawler{
		whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
			if person != "Ada" {
				t.Fatalf("who query = %q", person)
			}
			return []whomatch.Candidate{{
				Who:         "Ada Example",
				Identifiers: []string{"ada@example.com"},
				LastSeen:    time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
				Messages:    3,
			}}, nil
		},
		searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
			got = q
			return SearchResult{
				Results:      []Hit{{Ref: "testcrawl:1", Time: time.Unix(0, 0).UTC(), Who: "Ada Example", Snippet: q.Text}},
				TotalMatches: 1,
			}, nil
		},
	}
	code, stdout, stderr := runForTest([]string{"search", "needle", "--json", "--state-root", stateRoot, "--who", "Ada"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("search --who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Who != "Ada" || got.WhoResolved == nil || got.WhoResolved.Who != "Ada Example" || !containsString(got.WhoResolved.Identifiers, "ada@example.com") {
		t.Fatalf("query who resolution = %#v", got)
	}
	var envelope struct {
		Query       string       `json:"query"`
		WhoResolved *WhoResolved `json:"who_resolved"`
		Results     []Hit        `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	if envelope.Query != "needle" || envelope.WhoResolved == nil || envelope.WhoResolved.Who != "Ada Example" || !containsString(envelope.WhoResolved.Identifiers, "ada@example.com") || len(envelope.Results) != 1 {
		t.Fatalf("search envelope = %#v", envelope)
	}
	if strings.Contains(stdout, `"WhoResolved"`) {
		t.Fatalf("search envelope leaked Go field names: %s", stdout)
	}
}

func TestRunSearchWhoTextHeadingShowsResolvedPerson(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{
		whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
			return []whomatch.Candidate{{
				Who:         "Ada Example",
				Identifiers: []string{"ada@example.com"},
				LastSeen:    time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
				Messages:    3,
			}}, nil
		},
		searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
			return SearchResult{
				Results:      []Hit{{Ref: "testcrawl:1", Time: time.Unix(0, 0).UTC(), Who: "Ada Example", Snippet: q.Text}},
				TotalMatches: 1,
			}, nil
		},
	}
	code, stdout, stderr := runForTest([]string{"search", "needle", "--state-root", stateRoot, "--who", "Ada"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("search --who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `Search "needle" with Ada Example: showing 1 of 1.`) {
		t.Fatalf("search heading missing resolved person:\n%s", stdout)
	}
}

func TestRunWhoJSONContract(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
		return []whomatch.Candidate{{
			Who:         "Ada Example",
			Identifiers: []string{"ada@example.com"},
			LastSeen:    time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
			Messages:    3,
		}}, nil
	}}
	code, stdout, stderr := runForTest([]string{"who", "Ada", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("who json = %s err=%v", stdout, err)
	}
	for _, key := range []string{"query", "candidates"} {
		if _, ok := root[key]; !ok {
			t.Fatalf("who json missing %q: %s", key, stdout)
		}
	}
	if _, ok := root["Candidates"]; ok {
		t.Fatalf("who json leaked Go field names: %s", stdout)
	}
	var envelope struct {
		Query      string `json:"query"`
		Candidates []struct {
			Who         string   `json:"who"`
			Identifiers []string `json:"identifiers"`
			LastSeen    string   `json:"last_seen"`
			Messages    int64    `json:"messages"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("who envelope = %s err=%v", stdout, err)
	}
	if envelope.Query != "Ada" || len(envelope.Candidates) != 1 || envelope.Candidates[0].Who != "Ada Example" || !containsString(envelope.Candidates[0].Identifiers, "ada@example.com") || envelope.Candidates[0].LastSeen != "2026-07-02T12:00:00Z" || envelope.Candidates[0].Messages != 3 {
		t.Fatalf("who envelope = %#v", envelope)
	}
}

func TestRunSearchWhoResolutionErrorsUseContractExitCodes(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
		return []whomatch.Candidate{
			{Who: "Ada Example", Identifiers: []string{"ada@example.com"}},
			{Who: "Ada Other", Identifiers: []string{"ada.other@example.com"}},
		}, nil
	}}
	code, stdout, stderr := runForTest([]string{"search", "needle", "--json", "--state-root", stateRoot, "--who", "Ada"}, source, runOptions{})
	if code != 4 || stderr != "" || !strings.Contains(stdout, `"code":"ambiguous_who"`) || !strings.Contains(stdout, `"candidates"`) {
		t.Fatalf("ambiguous who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	source.whoFn = func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
		return nil, nil
	}
	code, stdout, stderr = runForTest([]string{"search", "needle", "--json", "--state-root", stateRoot, "--who", "Ada"}, source, runOptions{})
	if code != 5 || stderr != "" || !strings.Contains(stdout, `"code":"unknown_who"`) {
		t.Fatalf("unknown who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunSearchWhoAmbiguousTextListsCandidatesAndRetry(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{
		whoFn: func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
			return []whomatch.Candidate{
				{Who: "Ada Example", Identifiers: []string{"ada@example.com"}},
				{Who: "Ada Other", Identifiers: []string{"ada.other@example.com"}},
			}, nil
		},
		searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
			t.Fatal("search ran after ambiguous --who")
			return SearchResult{}, nil
		},
	}
	code, stdout, stderr := runForTest([]string{"search", "needle", "--state-root", stateRoot, "--who", "Ada"}, source, runOptions{})
	if code != 4 || stdout != "" {
		t.Fatalf("ambiguous who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"Ada Example",
		"Ada Other",
		"Retry with one listed identifier: search needle --who ada@example.com",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("ambiguous who text missing %q:\n%s", want, stderr)
		}
	}
}

func TestRunTextErrorsUseStderr(t *testing.T) {
	stateRoot := t.TempDir()
	code, stdout, stderr := runForTest([]string{"search", "--state-root", stateRoot, "hello"}, &testCrawler{}, runOptions{})
	if code != 2 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("text error wrote stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "Error:") || !strings.Contains(stderr, "archive does not exist") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRunMetadataSkipsConfigValidation(t *testing.T) {
	stateRoot := t.TempDir()
	cfgDir := filepath.Join(stateRoot, "testcrawl")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("required = \"bad\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := &testCrawler{cfg: &testConfig{}}
	code, stdout, stderr := runForTest([]string{"metadata", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("metadata json = %s err=%v", stdout, err)
	}
	if manifest.ID != "testcrawl" {
		t.Fatalf("manifest ID = %q", manifest.ID)
	}
}

func TestRunHelpUsesSharedUsage(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "top", args: []string{"--help", "--state-root", stateRoot}, want: "Commands:\n"},
		{name: "verb", args: []string{"status", "--help", "--state-root", stateRoot}, want: "test status:"},
		{name: "help command", args: []string{"help", "search", "--state-root", stateRoot}, want: "--limit VALUE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTest(tc.args, source, runOptions{})
			if code != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if stderr != "" {
				t.Fatalf("help wrote stderr: %q", stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Fatalf("help missing %q:\n%s", tc.want, stdout)
			}
			if !strings.Contains(stdout, "Diagnostics: run with -v, or read ") {
				t.Fatalf("help missing diagnostics line:\n%s", stdout)
			}
		})
	}

	source = &testCrawler{verbs: []Verb{{
		Name: "archive import",
		Help: "Import an archive.",
		Args: []string{"PATH"},
		Flags: func(fs *flag.FlagSet) {
			fs.Bool("dry-run", false, "check without importing")
			fs.String("path", "", "archive path")
			fs.Int("limit", 20, "maximum rows")
		},
	}}}
	code, stdout, stderr := runForTest([]string{"help", "archive", "import", "--state-root", stateRoot}, source, runOptions{})
	if code != 0 {
		t.Fatalf("bespoke help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"--dry-run", "--path VALUE", "--limit VALUE"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("bespoke help missing %q:\n%s", want, stdout)
		}
	}
	for _, bad := range []string{"--dry-run false", "--path  ", "--limit 20"} {
		if strings.Contains(stdout, bad) {
			t.Fatalf("bespoke help contains bad flag syntax %q:\n%s", bad, stdout)
		}
	}
	if !strings.Contains(stdout, "maximum rows (default 20)") {
		t.Fatalf("bespoke help missing default in summary:\n%s", stdout)
	}
}

func TestRunWorldMustChangeUsesSharedErrorBody(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{statusFn: func(ctx context.Context, req *Request) (*control.Status, error) {
		return nil, cklog.WorldMustChange{
			Message: "cannot read source",
			Remedy:  "grant Full Disk Access",
		}
	}}
	code, stdout, stderr := runForTest([]string{"status", "--json", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stderr != "" {
		t.Fatalf("json world error code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope ckoutput.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("error envelope = %s err=%v", stdout, err)
	}
	if envelope.Error.Message != "cannot read source" || envelope.Error.Remedy != "grant Full Disk Access" {
		t.Fatalf("world error envelope = %#v", envelope.Error)
	}

	code, stdout, stderr = runForTest([]string{"status", "--state-root", stateRoot}, source, runOptions{})
	if code != 1 || stdout != "" {
		t.Fatalf("text world error code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "cannot read source") || !strings.Contains(stderr, "grant Full Disk Access") {
		t.Fatalf("text world error missing remedy: %q", stderr)
	}
}

func TestRunTextOutputUsesSharedRenderers(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "metadata", args: []string{"metadata", "--state-root", stateRoot}, want: "Metadata\n"},
		{name: "status", args: []string{"status", "--state-root", stateRoot}, want: "Status: ok\nready\n"},
		{name: "doctor", args: []string{"doctor", "--state-root", stateRoot}, want: "Doctor checks:\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, _ := runForTest(tc.args, source, runOptions{})
			if code != 0 {
				t.Fatalf("code=%d stdout=%s", code, stdout)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Fatalf("stdout missing %q:\n%s", tc.want, stdout)
			}
			if strings.Contains(stdout, "&{") || strings.Contains(stdout, "{[") {
				t.Fatalf("text output leaked Go struct formatting:\n%s", stdout)
			}
		})
	}

	createArchive(t, stateRoot)
	code, stdout, _ := runForTest([]string{"search", "--state-root", stateRoot, "hello"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, `Search "hello": showing 1 of 1.`) || strings.Contains(stdout, "&{") {
		t.Fatalf("search text code=%d stdout=%s", code, stdout)
	}

	opts := childTestOptions(t, "progress")
	code, stdout, stderr := runForTest([]string{"sync", "--state-root", stateRoot}, source, opts)
	if code != 0 || !strings.Contains(stdout, "Sync complete") || strings.Contains(stdout, "&{") {
		t.Fatalf("sync text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunBespokeVerbReceivesPositionalArgs(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	var sawArgs []string
	dryRun := false
	source := &testCrawler{verbs: []Verb{{
		Name: "archive import",
		Help: "Import an archive.",
		Args: []string{"PATH"},
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&dryRun, "dry-run", false, "check without importing")
		},
		Run: func(ctx context.Context, req *Request) error {
			sawArgs = append([]string(nil), req.Args...)
			_, err := req.Out.Write([]byte("imported\n"))
			return err
		},
	}}}
	code, stdout, _ := runForTest([]string{"archive", "import", "--dry-run", "--state-root", stateRoot, "/tmp/archive.zip"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("code=%d stdout=%s", code, stdout)
	}
	if !dryRun {
		t.Fatal("bespoke flag was not parsed")
	}
	if strings.Join(sawArgs, " ") != "/tmp/archive.zip" {
		t.Fatalf("req.Args = %#v", sawArgs)
	}

	dryRun = false
	sawArgs = nil
	code, stdout, _ = runForTest([]string{"archive", "import", "--state-root", stateRoot, "/tmp/archive.zip", "--dry-run"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("flag after positional code=%d stdout=%s", code, stdout)
	}
	if !dryRun {
		t.Fatal("bespoke flag after positional was not parsed")
	}
	if strings.Join(sawArgs, " ") != "/tmp/archive.zip" {
		t.Fatalf("flag after positional req.Args = %#v", sawArgs)
	}
}

func TestRunDeadlineCancelsContextAwareRead(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		<-ctx.Done()
		return SearchResult{}, ctx.Err()
	}}
	start := time.Now()
	code, stdout, _ := runForTest([]string{"search", "--json", "--state-root", stateRoot, "wait"}, source, runOptions{readTimeout: 20 * time.Millisecond})
	if code != 1 || time.Since(start) > time.Second {
		t.Fatalf("deadline code=%d elapsed=%s stdout=%s", code, time.Since(start), stdout)
	}
	if !strings.Contains(stdout, "deadline_exceeded") {
		t.Fatalf("deadline envelope = %s", stdout)
	}
}

func TestRunDeadlineInterruptsBlockedSQLiteRead(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		var value int
		err := req.Store.DB().QueryRowContext(ctx, `
			with recursive cnt(x) as (
				select 0
				union all
				select x+1 from cnt where x < 100000000
			)
			select max(x) from cnt
		`).Scan(&value)
		return SearchResult{}, err
	}}
	start := time.Now()
	code, stdout, _ := runForTest([]string{"search", "--json", "--state-root", stateRoot, "sqlite"}, source, runOptions{readTimeout: 20 * time.Millisecond})
	if code != 1 || time.Since(start) > 2*time.Second {
		t.Fatalf("sqlite interrupt code=%d elapsed=%s stdout=%s", code, time.Since(start), stdout)
	}
}

func TestRunLockAndExitCodeMap(t *testing.T) {
	lock, err := acquireRunLock(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := acquireRunLock(filepath.Dir(lock.path)); err == nil {
		t.Fatal("second lock succeeded")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	lock2, err := acquireRunLock(filepath.Dir(lock.path))
	if err != nil {
		t.Fatalf("lock after close: %v", err)
	}
	defer func() { _ = lock2.Close() }()
	if exitCodeFor(nil) != 0 || exitCodeFor(usageError{err: errors.New("bad")}) != 2 || exitCodeFor(partialError{err: errors.New("some failed")}) != 3 {
		t.Fatal("basic exit code map is wrong")
	}
	if exitCodeFor(whoAmbiguityError{message: "ambiguous"}) != 4 || exitCodeFor(whoAmbiguityError{message: "ambiguous", code: 5}) != 5 {
		t.Fatal("who ambiguity exit codes are wrong")
	}
}

func TestSignalBridgeCancelsOnInterrupt(t *testing.T) {
	ctx, stop := defaultSignalContext(context.Background())
	defer stop()
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("signal bridge did not cancel")
	}
}

func TestRunChildWireReexecProgressWatchdogAndStaleLock(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	opts := childTestOptions(t, "progress")
	code, stdout, stderr := runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("progress child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	opts = childTestOptions(t, "hold")
	code, stdout, stderr = runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	if code != 1 || !strings.Contains(stdout, "made no progress") {
		t.Fatalf("watchdog code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	logData, err := os.ReadFile(filepath.Join(stateRoot, "testcrawl", "logs", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "sync_progress") || !strings.Contains(string(logData), "finish: outcome=error") {
		t.Fatalf("watchdog run log did not record progress and finished error:\n%s", string(logData))
	}

	opts = childTestOptions(t, "progress")
	code, stdout, stderr = runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("rerun after killed child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunChildWireLogFramesResetWatchdog(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	opts := childTestOptions(t, "log_heartbeat")
	start := time.Now()
	code, stdout, stderr := runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	elapsed := time.Since(start)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("log heartbeat child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if elapsed < opts.watchdog {
		t.Fatalf("log heartbeat finished before watchdog window, elapsed=%s watchdog=%s", elapsed, opts.watchdog)
	}
	logData, err := os.ReadFile(filepath.Join(stateRoot, "testcrawl", "logs", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "log_heartbeat") {
		t.Fatalf("log heartbeat did not reach run log:\n%s", logText)
	}
	if strings.Contains(logText, "sync_progress") {
		t.Fatalf("log heartbeat child emitted progress:\n%s", logText)
	}
}

func TestRunMutatingBespokeVerbUsesWireReexec(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{verbs: []Verb{{
		Name:    "archive import",
		Help:    "Import an archive.",
		Args:    []string{"PATH"},
		Mutates: true,
	}}}
	opts := childTestOptions(t, "bespoke")
	code, stdout, stderr := runForTest([]string{"archive", "import", "--json", "--state-root", stateRoot, "/tmp/archive.zip"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "bespoke:/tmp/archive.zip") {
		t.Fatalf("bespoke child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	opts = childTestOptions(t, "bespoke")
	code, stdout, stderr = runForTest([]string{"archive", "import", "--json", "--state-root", stateRoot, "--", "/tmp/archive.zip", "--literal"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "bespoke:/tmp/archive.zip --literal") {
		t.Fatalf("bespoke child with -- code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunMutatingVerbTimeoutControlsWatchdog(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{verbs: []Verb{{
		Name:    "archive import",
		Help:    "Import an archive.",
		Args:    []string{"PATH"},
		Mutates: true,
		Timeout: 30 * time.Millisecond,
	}}}
	opts := childTestOptions(t, "bespoke_hold")
	opts.watchdog = time.Second
	start := time.Now()
	code, stdout, stderr := runForTest([]string{"archive", "import", "--json", "--state-root", stateRoot, "/tmp/archive.zip"}, source, opts)
	if code != 1 || !strings.Contains(stdout, "made no progress for 30ms") {
		t.Fatalf("bespoke watchdog code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("bespoke watchdog used global timeout, elapsed=%s", time.Since(start))
	}
}

func TestRunMutatingVerbTimeoutIsProgressWatchdog(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{verbs: []Verb{{
		Name:    "archive import",
		Help:    "Import an archive.",
		Args:    []string{"PATH"},
		Mutates: true,
		Timeout: 250 * time.Millisecond,
	}}}
	opts := childTestOptions(t, "bespoke_progress_past_timeout")
	opts.watchdog = time.Second
	start := time.Now()
	code, stdout, stderr := runForTest([]string{"archive", "import", "--json", "--state-root", stateRoot, "/tmp/archive.zip"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "progressed") {
		t.Fatalf("bespoke progress watchdog code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if time.Since(start) < 250*time.Millisecond {
		t.Fatalf("bespoke verb completed before the custom watchdog threshold, elapsed=%s", time.Since(start))
	}
}

func TestRunWatchdogKillsChildProcessGroup(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	marker := filepath.Join(t.TempDir(), "grandchild-alive")
	opts := childTestOptions(t, "tree")
	opts.childEnv = append(opts.childEnv, "CRAWLKIT_TREE_MARKER="+marker)
	code, stdout, stderr := runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	if code != 1 || !strings.Contains(stdout, "made no progress") {
		t.Fatalf("process tree watchdog code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("grandchild escaped process group kill: stat err=%v", err)
	}
}

func TestRunChildWireArchiveBusyLockPathRoundTrips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("parent-death process semantics differ on Windows")
	}
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	marker := filepath.Join(t.TempDir(), "lock-holder-pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestRunnerLockParentHelper", "--", stateRoot, marker) // #nosec G204 -- test helper path and marker are controlled by the test.
	cmd.Env = append(os.Environ(), "CRAWLKIT_LOCK_PARENT=1")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	childPID := waitForPIDMarker(t, marker, done, func() string { return stderr.String() })
	defer killProcessAndWaitForLock(t, childPID, filepath.Join(stateRoot, "testcrawl"))

	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("parent did not exit after kill stdout=%s stderr=%s", stdout.String(), stderr.String())
	}

	opts := childTestOptions(t, "progress")
	code, retryStdout, retryStderr := runForTest([]string{"sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	var envelope struct {
		Error struct {
			Code     string `json:"code"`
			LockPath string `json:"lock_path"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(retryStdout), &envelope); err != nil {
		t.Fatalf("archive_busy error is not JSON: code=%d stdout=%s stderr=%s err=%v", code, retryStdout, retryStderr, err)
	}
	wantLockPath := filepath.Join(stateRoot, "testcrawl", "run.lock")
	if code != 1 || envelope.Error.Code != "archive_busy" || envelope.Error.LockPath != wantLockPath {
		t.Fatalf("second run while child holds lock code=%d stdout=%s stderr=%s", code, retryStdout, retryStderr)
	}
	if strings.Contains(retryStdout, `"lock":`) {
		t.Fatalf("archive_busy envelope used the old lock field: %s", retryStdout)
	}
}

func TestRunChildWireForwardsLogLinesVerbatim(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	line := "2026-07-07 10:20:30 crawler WARN child_warn: message=kept"
	opts := childTestOptions(t, "odd_log")
	opts.childEnv = append(opts.childEnv, "CRAWLKIT_ODD_LOG_LINE="+line)
	code, stdout, stderr := runForTest([]string{"-v", "sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("odd log child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, line) {
		t.Fatalf("forwarded log line missing:\nwant: %s\nstderr:\n%s", line, stderr)
	}
}

func TestRunChildVerboseLoggingSurvivesWireReexec(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verbose   string
		wantDebug bool
	}{
		{name: "info", verbose: "-v"},
		{name: "debug", verbose: "-vv", wantDebug: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateRoot := t.TempDir()
			createArchive(t, stateRoot)
			opts := childTestOptions(t, "logs")
			code, stdout, stderr := runForTest([]string{tc.verbose, "sync", "--json", "--state-root", stateRoot}, &testCrawler{}, opts)
			if code != 0 {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			logData, err := os.ReadFile(filepath.Join(stateRoot, "testcrawl", "logs", "current.log"))
			if err != nil {
				t.Fatal(err)
			}
			logText := string(logData)
			if !strings.Contains(logText, "child_info") || !strings.Contains(stderr, "child_info") {
				t.Fatalf("child info log did not survive re-exec\nlog:\n%s\nstderr:\n%s", logText, stderr)
			}
			if strings.Contains(stderr, "raw_child_stderr") {
				t.Fatalf("raw child stderr bypassed the log wire:\n%s", stderr)
			}
			logHasDebug := strings.Contains(logText, "child_debug")
			stderrHasDebug := strings.Contains(stderr, "child_debug")
			if tc.wantDebug && (!logHasDebug || !stderrHasDebug) {
				t.Fatalf("child debug log did not survive -vv\nlog:\n%s\nstderr:\n%s", logText, stderr)
			}
			if !tc.wantDebug && (logHasDebug || stderrHasDebug) {
				t.Fatalf("child debug log was written without -vv\nlog:\n%s\nstderr:\n%s", logText, stderr)
			}
		})
	}
}

func TestRunChildWatchdogCoversResultThenHang(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestRunnerFrameThenHangHelper")
	configureChildCommand(cmd)
	cmd.Env = append(os.Environ(), "CRAWLKIT_FRAME_THEN_HANG=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	result := waitForChild(context.Background(), cmd, stdout, stderr.String, 30*time.Millisecond, 20*time.Millisecond, nil, 0, io.Discard)
	if result.err == nil || !strings.Contains(result.err.Error(), "made no progress") {
		t.Fatalf("result err = %v output=%s stderr=%s", result.err, result.output, stderr.String())
	}
	if string(result.output) != "{}" {
		t.Fatalf("result output = %q", result.output)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("waitForChild hung after result frame, elapsed=%s", time.Since(start))
	}
}

func TestPathExistsPreservesStatErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions are not portable on Windows")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(dir, 0o700) }()
	exists, err := pathExists(filepath.Join(dir, "config.toml"))
	if err == nil {
		t.Fatalf("pathExists returned exists=%t err=nil for an unreadable path", exists)
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("pathExists err = %v, want permission denied", err)
	}
}

func TestRunnerChildHelper(t *testing.T) {
	if os.Getenv("CRAWLKIT_RUNNER_CHILD") != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	includeArchived := false
	backupRepo := ""
	source := &testCrawler{
		verbs: []Verb{
			{
				Name:    "archive import",
				Help:    "Import an archive.",
				Args:    []string{"PATH"},
				Mutates: true,
				Run: func(ctx context.Context, req *Request) error {
					switch os.Getenv("CRAWLKIT_CHILD_MODE") {
					case "bespoke_hold":
						select {}
					case "bespoke_progress_past_timeout":
						for i := 0; i < 5; i++ {
							req.Progress(Progress{Phase: "import", Done: int64(i + 1), Total: 5, Message: "still importing"})
							select {
							case <-ctx.Done():
								return ctx.Err()
							case <-time.After(80 * time.Millisecond):
							}
						}
						_, err := req.Out.Write([]byte("progressed\n"))
						return err
					case "bespoke":
						req.Progress(Progress{Phase: "import", Done: 1, Total: 1, Message: "imported archive"})
						_, err := req.Out.Write([]byte("bespoke:" + strings.Join(req.Args, " ") + "\n"))
						return err
					default:
						return errors.New("wrong child mode for bespoke verb")
					}
				},
			},
			{
				Name: "sync",
				Flags: func(fs *flag.FlagSet) {
					fs.BoolVar(&includeArchived, "include-archived", false, "include archived items")
					fs.StringVar(&backupRepo, "backup-repo", "", "backup repository")
				},
			},
		},
		syncFn: func(ctx context.Context, req *Request) (*SyncReport, error) {
			switch os.Getenv("CRAWLKIT_CHILD_MODE") {
			case "zero_sync":
				return &SyncReport{}, nil
			case "progress":
				req.Progress(Progress{Phase: "sync", Done: 1, Total: 1, Message: "synced one item"})
				return &SyncReport{Added: 1}, nil
			case "sync_flag":
				if !includeArchived {
					return nil, errors.New("sync flag was not parsed")
				}
				if backupRepo != "synthetic" {
					return nil, errors.New("sync string flag was not parsed")
				}
				req.Progress(Progress{Phase: "sync", Done: 1, Total: 1, Message: "synced archived items"})
				return &SyncReport{Added: 7}, nil
			case "log_heartbeat":
				for i := 0; i < 5; i++ {
					if err := req.Log.Info("log_heartbeat", "iteration="+strconv.Itoa(i+1)); err != nil {
						return nil, err
					}
					select {
					case <-ctx.Done():
						return nil, ctx.Err()
					case <-time.After(25 * time.Millisecond):
					}
				}
				return &SyncReport{Added: 1}, nil
			case "logs":
				if stderr := os.NewFile(2, "stderr"); stderr != nil {
					_, _ = stderr.WriteString("raw_child_stderr\n")
				}
				if err := req.Log.Info("child_info", "message=info"); err != nil {
					return nil, err
				}
				if err := req.Log.Debug("child_debug", "message=debug"); err != nil {
					return nil, err
				}
				return &SyncReport{Added: 1}, nil
			case "odd_log":
				line := os.Getenv("CRAWLKIT_ODD_LOG_LINE")
				if line == "" {
					return nil, errors.New("missing odd log line")
				}
				if err := writeChildFrame(os.Stdout, childLogFrame(line)); err != nil {
					return nil, err
				}
				return &SyncReport{Added: 1}, nil
			case "hold":
				select {}
			case "tree":
				marker := os.Getenv("CRAWLKIT_TREE_MARKER")
				if marker == "" {
					return nil, errors.New("missing tree marker")
				}
				cmd := exec.Command(os.Args[0], "-test.run=TestRunnerGrandchildHelper", "--", marker) // #nosec G204 -- test helper path and marker are controlled by the test.
				cmd.Env = append(os.Environ(), "CRAWLKIT_RUNNER_GRANDCHILD=1")
				cmd.Stdout = io.Discard
				cmd.Stderr = io.Discard
				if err := cmd.Start(); err != nil {
					return nil, err
				}
				req.Progress(Progress{Phase: "tree", Done: 1, Total: 1, Message: "grandchild started"})
				select {}
			case "lock_hold":
				marker := os.Getenv("CRAWLKIT_LOCK_MARKER")
				if marker == "" {
					return nil, errors.New("missing lock marker")
				}
				if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
					return nil, err
				}
				select {}
			default:
				return nil, errors.New("unknown child mode")
			}
		},
	}
	code := runner{opts: defaultRunOptions()}.run(args, []Crawler{source})
	os.Exit(code)
}

func TestRunnerLockParentHelper(t *testing.T) {
	if os.Getenv("CRAWLKIT_LOCK_PARENT") != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	if len(args) != 2 {
		os.Exit(2)
	}
	opts := childTestOptions(t, "lock_hold")
	opts.watchdog = time.Hour
	opts.childEnv = append(opts.childEnv, "CRAWLKIT_LOCK_MARKER="+args[1])
	code := runner{opts: opts}.run([]string{"sync", "--json", "--state-root", args[0]}, []Crawler{&testCrawler{}})
	os.Exit(code)
}

func TestRunnerFrameThenHangHelper(t *testing.T) {
	if os.Getenv("CRAWLKIT_FRAME_THEN_HANG") != "1" {
		return
	}
	stdout := os.NewFile(1, "stdout")
	if stdout == nil {
		os.Exit(1)
	}
	if err := writeChildFrame(stdout, childResultFrame("{}", nil)); err != nil {
		os.Exit(1)
	}
	select {}
}

func TestRunnerGrandchildHelper(t *testing.T) {
	if os.Getenv("CRAWLKIT_RUNNER_GRANDCHILD") != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	if len(args) != 1 {
		os.Exit(2)
	}
	time.Sleep(250 * time.Millisecond)
	if err := os.WriteFile(args[0], []byte("alive\n"), 0o600); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func waitForPIDMarker(t *testing.T, marker string, parentDone <-chan error, stderr func() string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-parentDone:
			t.Fatalf("parent exited before child held the lock: err=%v stderr=%s", err, stderr())
		default:
		}
		data, err := os.ReadFile(marker)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatalf("lock marker contained invalid pid %q: %v", strings.TrimSpace(string(data)), err)
			}
			return pid
		}
		if !os.IsNotExist(err) {
			t.Fatalf("read lock marker: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for child lock marker; stderr=%s", stderr())
	return 0
}

func killProcessAndWaitForLock(t *testing.T, pid int, base string) {
	t.Helper()
	process, err := os.FindProcess(pid)
	if err == nil {
		_ = process.Signal(os.Kill)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lock, err := acquireRunLock(base)
		if err == nil {
			_ = lock.Close()
			return
		}
		if _, ok := err.(lockHeldError); !ok {
			t.Fatalf("probe run lock after killing child: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run lock stayed held after killing child pid %d", pid)
}

func runForTest(argv []string, source Crawler, opts runOptions) (int, string, string) {
	return runSourcesForTest(argv, []Crawler{source}, opts)
}

func runSourcesForTest(argv []string, sources []Crawler, opts runOptions) (int, string, string) {
	var stdout, stderr bytes.Buffer
	opts.stdout = &stdout
	opts.stderr = &stderr
	if opts.signalContext == nil {
		opts.signalContext = func(ctx context.Context) (context.Context, context.CancelFunc) {
			return ctx, func() {}
		}
	}
	code := runner{opts: opts}.run(argv, sources)
	return code, stdout.String(), stderr.String()
}

func createArchive(t *testing.T, stateRoot string) {
	t.Helper()
	path := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	st, err := ckstore.Open(context.Background(), ckstore.Options{
		Path:   path,
		Schema: `create table if not exists things(id text primary key);`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func childTestOptions(t *testing.T, mode string) runOptions {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "CRAWLKIT_RUNNER_CHILD=1", "CRAWLKIT_CHILD_MODE="+mode)
	return runOptions{
		executable:      exe,
		childPrefixArgs: []string{"-test.run=TestRunnerChildHelper", "--"},
		childEnv:        env,
		watchdog:        80 * time.Millisecond,
		killGrace:       20 * time.Millisecond,
	}
}

func argsAfterDoubleDash(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func argvHasStateRoot(argv []string, stateRoot string) bool {
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--state-root" && argv[i+1] == stateRoot {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
