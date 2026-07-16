package trawlkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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

	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

type testCrawler struct {
	id            string
	surface       string
	headlines     []string
	cfg           *testConfig
	verbs         []Verb
	statusFn      func(context.Context, *Request) (*control.Status, error)
	searchFn      func(context.Context, *Request, Query) (SearchResult, error)
	whoFn         func(context.Context, *Request, string) ([]whomatch.Candidate, error)
	syncFn        func(context.Context, *Request) (*SyncReport, error)
	prepareFn     func(context.Context, string) error
	readPrepareFn func(context.Context, string) error
}

type testStatusCrawler struct {
	verbs []Verb
}

type testSearchCrawler struct {
	testStatusCrawler
}

type writeOnlyArchivePreparer struct {
	testSearchCrawler
	prepareCalls int
}

type testContactCrawler struct {
	*testCrawler
	contactExportFn func(context.Context, *Request) (*control.ContactExport, error)
}

type testOpenContactCrawler struct {
	*testContactCrawler
}

type testShortRefCrawler struct {
	*testCrawler
	records []ShortRefRecord
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
		Headlines:   append([]string(nil), c.headlines...),
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

func (c *testCrawler) Verbs() []Verb {
	return c.verbs
}

func (c *testCrawler) Search(ctx context.Context, req *Request, q Query) (SearchResult, error) {
	if c.searchFn != nil {
		return c.searchFn(ctx, req, q)
	}
	results := []Hit{testSearchHit(c.Info().ID+":1", "Test result", q.Text)}
	return SearchResult{Results: results, TotalMatches: len(results)}, nil
}

func testSearchHit(ref, title, matchingText string) Hit {
	return Hit{
		Ref:      ref,
		Time:     time.Unix(0, 0).UTC(),
		AnchorID: MatchAnchorID,
		Summary:  ResultSummary{Title: title},
		Evidence: []EvidenceFragment{TextMatch("Matching text", matchingText)},
	}
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

// PrepareArchive makes testCrawler satisfy ArchivePreparer. Every existing
// test using testCrawler for a storeWrite verb now also exercises this hook;
// with prepareFn unset it is a true no-op, so those tests are unaffected.
func (c *testCrawler) PrepareArchive(ctx context.Context, path string) error {
	if c.prepareFn != nil {
		return c.prepareFn(ctx, path)
	}
	return nil
}

func (c *testCrawler) PrepareReadArchive(ctx context.Context, path string) error {
	if c.readPrepareFn != nil {
		return c.readPrepareFn(ctx, path)
	}
	return nil
}

func (c *writeOnlyArchivePreparer) PrepareArchive(context.Context, string) error {
	c.prepareCalls++
	return nil
}

func (c *testShortRefCrawler) ShortRefRecords(ctx context.Context, req *Request) ([]ShortRefRecord, error) {
	return append([]ShortRefRecord(nil), c.records...), nil
}

func (c *testStatusCrawler) Info() Info {
	return Info{ID: "testcrawl", Surface: "test", DisplayName: "Test"}
}

func (c *testStatusCrawler) Status(ctx context.Context, req *Request) (*control.Status, error) {
	status := control.NewStatus(c.Info().ID, "ready")
	status.State = "ok"
	return &status, nil
}

func (c *testStatusCrawler) Verbs() []Verb {
	return c.verbs
}

func (c *testSearchCrawler) Search(ctx context.Context, req *Request, q Query) (SearchResult, error) {
	return SearchResult{
		Results:      []Hit{testSearchHit(c.Info().ID+":1", "Test result", q.Text)},
		TotalMatches: 3,
		Truncated:    true,
	}, nil
}

func (c *testContactCrawler) ContactExport(ctx context.Context, req *Request) (*control.ContactExport, error) {
	if c.contactExportFn != nil {
		return c.contactExportFn(ctx, req)
	}
	return &control.ContactExport{Contacts: []control.Contact{{
		DisplayName:  "Ada Example",
		PhoneNumbers: []string{"+15550100"},
	}}}, nil
}

func (c *testOpenContactCrawler) Open(ctx context.Context, req *Request, ref string) error {
	return nil
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
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
	if !cmd.Mutates || cmd.Store != "write" || len(cmd.Flags) != 1 || cmd.Flags[0].Name != "path" || cmd.Flags[0].Usage != "archive path" {
		t.Fatalf("archive_import command = %#v", cmd)
	}
	for name, want := range map[string]string{
		"metadata": "none",
		"status":   "optional",
		"sync":     "write",
		"search":   "read",
		"who":      "read",
	} {
		if got := manifest.Commands[name].Store; got != want {
			t.Fatalf("%s store = %q, want %q", name, got, want)
		}
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
	if !slices.Contains(manifest.Capabilities, "short_refs") {
		t.Fatalf("capabilities missing short_refs: %#v", manifest.Capabilities)
	}
	if got, want := strings.Join(cmd.Argv[1:], " "), "archive import PATH --json"; got != want {
		t.Fatalf("archive_import argv suffix = %q, want %q", got, want)
	}
	assertNoInternalFlagTokens(t, stdout)
}

func TestRunMetadataDoesNotAdvertiseInternalStateFlags(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testOpenContactCrawler{testContactCrawler: &testContactCrawler{testCrawler: &testCrawler{
		verbs: []Verb{{
			Name: "sync",
			Flags: func(fs *flag.FlagSet) {
				fs.Bool("include-archived", false, "include archived items")
			},
		}},
	}}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertNoInternalFlagTokens(t, stdout)
}

func TestRunDeclaredStateRootFlagUsesCrawlerNamespaceAndChildWire(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{verbs: []Verb{
		{
			Name: "sync",
			Flags: func(fs *flag.FlagSet) {
				fs.String("state-root", "", "crawler-owned state root")
			},
		},
		{
			Name:    "archive import",
			Help:    "Import an archive.",
			Args:    []string{"PATH"},
			Mutates: true,
			Flags: func(fs *flag.FlagSet) {
				fs.String("state-root", "", "crawler-owned state root")
			},
		},
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	for _, command := range []string{"sync", "archive_import"} {
		if !commandHasFlag(manifest.Commands[command], "state-root") {
			t.Fatalf("%s manifest flags missing state-root: %#v", command, manifest.Commands[command].Flags)
		}
	}

	syncValue := "crawler-sync-root"
	syncProofPath := filepath.Join(t.TempDir(), "sync-proof.txt")
	opts := childTestOptions(t, "declared_state_root_sync")
	opts.childEnv = append(opts.childEnv,
		"TRAWLKIT_EXPECT_STATE_ROOT="+stateRoot,
		"TRAWLKIT_EXPECT_DECLARED_STATE_ROOT="+syncValue,
		"TRAWLKIT_DECLARED_STATE_ROOT_PROOF="+syncProofPath,
	)
	code, stdout, stderr = runForTestAt(stateRoot, []string{"sync", "--state-root", syncValue, "--json"}, source, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 13`) {
		t.Fatalf("declared state-root sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	syncProof := readProofFile(t, syncProofPath)
	assertProofLine(t, syncProof, "mode=sync")
	assertProofLine(t, syncProof, "declared_state_root="+syncValue)
	assertProofLine(t, syncProof, "wire_state_root_matches_parent=true")
	assertProofLine(t, syncProof, "request_state_root_matches_parent=true")
	t.Logf("declared-state-root sync proof:\n%s", syncProof)

	bespokeValue := "crawler-bespoke-root"
	bespokeProofPath := filepath.Join(t.TempDir(), "bespoke-proof.txt")
	opts = childTestOptions(t, "declared_state_root_bespoke")
	opts.childEnv = append(opts.childEnv,
		"TRAWLKIT_EXPECT_STATE_ROOT="+stateRoot,
		"TRAWLKIT_EXPECT_DECLARED_STATE_ROOT="+bespokeValue,
		"TRAWLKIT_DECLARED_STATE_ROOT_PROOF="+bespokeProofPath,
	)
	code, stdout, stderr = runForTestAt(stateRoot, []string{"archive", "import", "--state-root", bespokeValue, "--json", "/tmp/archive.zip"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "bespoke:/tmp/archive.zip") {
		t.Fatalf("declared state-root bespoke code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	bespokeProof := readProofFile(t, bespokeProofPath)
	assertProofLine(t, bespokeProof, "mode=bespoke")
	assertProofLine(t, bespokeProof, "declared_state_root="+bespokeValue)
	assertProofLine(t, bespokeProof, "wire_state_root_matches_parent=true")
	assertProofLine(t, bespokeProof, "request_state_root_matches_parent=true")
	t.Logf("declared-state-root bespoke proof:\n%s", bespokeProof)
}

func TestRunIgnoresChildStateEnvOutsideWireInvocation(t *testing.T) {
	envRoot := t.TempDir()
	t.Setenv(childStateRootEnv, envRoot)
	t.Setenv(childRunIDEnv, "env-run")
	code, stdout, stderr := runForTest([]string{"metadata", "--json"}, &testCrawler{}, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	if manifest.Paths.DefaultDatabase == filepath.Join(envRoot, "testcrawl", "testcrawl.db") {
		t.Fatalf("%s affected non-child metadata path: %s", childStateRootEnv, manifest.Paths.DefaultDatabase)
	}
}

func TestRunMetadataAdvertisesShortRefs(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
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

func TestExecuteVerbAssignsShortRefsAfterFailedSync(t *testing.T) {
	ctx := context.Background()
	ref := "testcrawl:item/partial"
	source := &testShortRefCrawler{
		testCrawler: &testCrawler{syncFn: func(context.Context, *Request) (*SyncReport, error) {
			return nil, errors.New("late sync failure")
		}},
		records: []ShortRefRecord{{Ref: ref}},
	}
	st, err := ckstore.Open(ctx, ckstore.Options{Path: filepath.Join(t.TempDir(), "testcrawl.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	req := &Request{Store: st, Format: ckoutput.JSON, Out: &bytes.Buffer{}}

	err = executeVerb(ctx, source, targetVerb{name: "sync"}, req, globalOptions{}, ckoutput.JSON)
	if err == nil || !strings.Contains(err.Error(), "late sync failure") {
		t.Fatalf("executeVerb err = %v, want late sync failure", err)
	}

	resolved, err := (&Request{Store: st}).ResolveShortRef(ctx, shortref.Alias(ref, shortref.MinLength))
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0] != ref {
		t.Fatalf("resolved = %#v, want %q", resolved, ref)
	}
}

func TestExecuteVerbLeavesExistingShortRefsWhenProviderReturnsNone(t *testing.T) {
	ctx := context.Background()
	const ref = "testcrawl:item/gone"
	source := &testShortRefCrawler{
		testCrawler: &testCrawler{},
	}
	st, err := ckstore.Open(ctx, ckstore.Options{Path: filepath.Join(t.TempDir(), "testcrawl.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	req := &Request{Store: st, Format: ckoutput.JSON, Out: &bytes.Buffer{}}
	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	alias := shortref.Alias(ref, shortref.MinLength)

	err = executeVerb(ctx, source, targetVerb{name: "sync"}, req, globalOptions{}, ckoutput.JSON)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := req.ResolveShortRef(ctx, alias)
	if err != nil {
		t.Fatalf("ResolveShortRef(%q): %v", alias, err)
	}
	if len(resolved) != 1 || resolved[0] != ref {
		t.Fatalf("ResolveShortRef(%q) = %#v, want %q", alias, resolved, ref)
	}
}

// TestSyncPrepareArchiveRunsBeforeWriteOpen guards the placement decision
// behind ArchivePreparer: the hook must run before openStore creates
// req.Store, not after, so a crawler that wants to park an old archive file
// never has to fight over a connection the harness already opened (that was
// the F1 defect -- see trawlers/notes/harness_park_review_test.go).
//
// sync is a mutating verb, so the harness always runs it in a re-exec'd
// child process (see runChild), never in this test's own process. A
// prepareFn closure held by the test's local source value cannot observe
// that child's call, so this test drives TestRunnerChildHelper (via
// childTestOptions) like every other sync test in this file and reads back
// proof of the call through a marker file instead of a captured variable.
func TestSyncPrepareArchiveRunsBeforeWriteOpen(t *testing.T) {
	stateRoot := t.TempDir()
	marker := filepath.Join(t.TempDir(), "prepare.marker")
	opts := childTestOptions(t, "prepare_archive_ok")
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_PREPARE_MARKER="+marker)
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	proof, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("PrepareArchive marker not written: %v", err)
	}
	got := string(proof)
	wantPath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	if !strings.Contains(got, "path="+wantPath) {
		t.Fatalf("PrepareArchive proof = %q, want path=%s", got, wantPath)
	}
	if !strings.Contains(got, "existed=false") {
		t.Fatalf("PrepareArchive proof = %q, want existed=false (archive must not exist yet when PrepareArchive runs)", got)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("archive file missing after sync: %v", err)
	}
}

// TestSyncPrepareArchiveErrorAbortsBeforeSync checks that a PrepareArchive
// failure (e.g. a newer-than-supported archive) stops the verb before
// req.Store opens and before the crawler's own Sync ever runs -- the harness
// must not attempt a write connection, let alone a sync, once the crawler
// has refused the file.
//
// As above, sync always runs in a re-exec'd child, so this drives
// TestRunnerChildHelper's "prepare_archive_error" mode rather than a local
// closure. That mode has no matching syncFn case, so if Sync ran anyway the
// child would fail with "unknown child mode" instead of the expected
// PrepareArchive error, which the assertions below would catch.
func TestSyncPrepareArchiveErrorAbortsBeforeSync(t *testing.T) {
	stateRoot := t.TempDir()
	opts := childTestOptions(t, "prepare_archive_error")
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code == 0 {
		t.Fatalf("code = 0, want a failure; stdout=%s stderr=%s", stdout, stderr)
	}
	if strings.Contains(stdout, "unknown child mode") || strings.Contains(stderr, "unknown child mode") {
		t.Fatalf("Sync ran after PrepareArchive failed, want the harness to abort first: stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout, "newer than this build") && !strings.Contains(stderr, "newer than this build") {
		t.Fatalf("PrepareArchive error was not surfaced: stdout=%s stderr=%s", stdout, stderr)
	}
}

// TestStatusRunsReadArchivePreparation checks that preparation happens before every
// store-backed verb, including status's optional read store.
func TestStatusRunsReadArchivePreparation(t *testing.T) {
	stateRoot := t.TempDir()
	prepareCalls := 0
	source := &testCrawler{
		readPrepareFn: func(context.Context, string) error {
			prepareCalls++
			return nil
		},
	}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"status", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if prepareCalls != 1 {
		t.Fatalf("PrepareReadArchive calls = %d, want 1 for a store-backed verb", prepareCalls)
	}
	logData, err := os.ReadFile(filepath.Join(stateRoot, "testcrawl", "logs", "current.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "archive_prepare_read: duration_ms=") {
		t.Fatalf("read archive preparation duration missing from run log:\n%s", logData)
	}
}

func TestReadVerbsDoNotRunWriteArchivePreparation(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
	}{
		{name: "status", args: []string{"status", "--json"}, wantCode: 0},
		{name: "search", args: []string{"search", "anything", "--json"}, wantCode: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &writeOnlyArchivePreparer{}
			code, stdout, stderr := runForTestAt(t.TempDir(), tt.args, source, runOptions{})
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d; stdout=%s stderr=%s", code, tt.wantCode, stdout, stderr)
			}
			if source.prepareCalls != 0 {
				t.Fatalf("PrepareArchive calls = %d, want 0 for a read verb", source.prepareCalls)
			}
		})
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"status", "--json"}, source, runOptions{})
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"status", "--json"}, source, runOptions{})
	if code != 0 || !statusSawNil || !statusSawLog {
		t.Fatalf("status code=%d sawNil=%t sawLog=%t stdout=%s", code, statusSawNil, statusSawLog, stdout)
	}
	code, stdout, _ = runForTestAt(stateRoot, []string{"search", "--json", "hello"}, source, runOptions{})
	var missingArchive ckoutput.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &missingArchive); err != nil {
		t.Fatalf("missing archive error = %q: %v", stdout, err)
	}
	if code != 1 || missingArchive.Error.Code != "unavailable" || missingArchive.Error.Message != "This source is not ready yet." || missingArchive.Error.Remedy != "" || strings.Contains(stdout, stateRoot) {
		t.Fatalf("missing archive code=%d error=%#v stdout=%s", code, missingArchive.Error, stdout)
	}

	createArchive(t, stateRoot)
	source.searchFn = func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		if req.Store == nil {
			t.Fatal("search Store is nil")
		}
		if _, err := req.Store.DB().ExecContext(ctx, `create table should_fail(id text)`); err == nil {
			t.Fatal("read store accepted a write")
		}
		results := []Hit{testSearchHit("testcrawl:1", "Test result", q.Text)}
		return SearchResult{Results: results, TotalMatches: len(results)}, nil
	}
	code, stdout, _ = runForTestAt(stateRoot, []string{"search", "--json", "hello"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, `"results"`) {
		t.Fatalf("search code=%d stdout=%s", code, stdout)
	}
}

func TestRunBespokeStoreNoneFreshMaintenanceVerbsDoNotCreateArchive(t *testing.T) {
	stateRoot := t.TempDir()
	archivePath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	source := &testCrawler{verbs: maintenanceFixtureVerbs(func(ctx context.Context, req *Request, name string) error {
		if req.Store != nil {
			t.Fatalf("%s Store = %#v, want nil", name, req.Store)
		}
		_, err := fmt.Fprintf(req.Out, "%s store=nil\n", name)
		return err
	})}

	opts := childTestOptions(t, "maintenance_init")
	code, stdout, stderr := runForTestAt(stateRoot, []string{"maintenance", "init", "--json"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "maintenance init store=nil") {
		t.Fatalf("maintenance init code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runForTestAt(stateRoot, []string{"maintenance", "status", "--json"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, "maintenance status store=nil") {
		t.Fatalf("maintenance status code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runForTestAt(stateRoot, []string{"maintenance", "snapshots", "--json"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, "maintenance snapshots store=nil") {
		t.Fatalf("maintenance snapshots code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive exists after StoreNone maintenance verbs: err=%v path=%s", err, archivePath)
	}
	t.Logf("archive_exists_after_store_none=false archive_path=%s", archivePath)
}

func TestRunBespokeStoreOptionalSeesNilThenOpenStore(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{
		Name:  "archive inspect",
		Help:  "Inspect the archive if present.",
		Store: StoreOptional,
		Run: func(ctx context.Context, req *Request) error {
			if req.Store == nil {
				_, err := io.WriteString(req.Out, "optional store=nil\n")
				return err
			}
			_, err := io.WriteString(req.Out, "optional store=open\n")
			return err
		},
	}}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"archive", "inspect", "--json"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, "optional store=nil") {
		t.Fatalf("optional missing archive code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	createArchive(t, stateRoot)
	code, stdout, stderr = runForTestAt(stateRoot, []string{"archive", "inspect", "--json"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, "optional store=open") {
		t.Fatalf("optional existing archive code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunBespokeStoreRequiredRequiresArchive(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{
		Name:  "archive inspect",
		Help:  "Inspect the archive.",
		Store: StoreRequired,
		Run: func(ctx context.Context, req *Request) error {
			if req.Store == nil {
				t.Fatal("StoreRequired Store is nil")
			}
			_, err := io.WriteString(req.Out, "required store=open\n")
			return err
		},
	}}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"archive", "inspect", "--json"}, source, runOptions{})
	var missingArchive ckoutput.ErrorEnvelope
	if err := json.Unmarshal([]byte(stdout), &missingArchive); err != nil {
		t.Fatalf("required missing archive error = %q: %v", stdout, err)
	}
	if code != 1 || missingArchive.Error.Code != "unavailable" || missingArchive.Error.Message != "This source is not ready yet." || missingArchive.Error.Remedy != "" || stderr != "" || strings.Contains(stdout, stateRoot) {
		t.Fatalf("required missing archive code=%d error=%#v stdout=%s stderr=%s", code, missingArchive.Error, stdout, stderr)
	}
	createArchive(t, stateRoot)
	code, stdout, stderr = runForTestAt(stateRoot, []string{"archive", "inspect", "--json"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, "required store=open") {
		t.Fatalf("required existing archive code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunRejectsBespokeStoreOptionalWithMutates(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{
		Name:    "maintenance restore",
		Help:    "Restore state.",
		Mutates: true,
		Store:   StoreOptional,
		Run: func(ctx context.Context, req *Request) error {
			return nil
		},
	}}}
	wantMessage := "invalid maintenance restore Verb declaration: StoreOptional cannot be used with Mutates"
	wantRemedy := "Set Store to StoreNone"

	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("metadata invalid StoreOptional code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = runForTestAt(stateRoot, []string{"maintenance", "restore", "--json"}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("invocation invalid StoreOptional code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunSearchJSONEnvelopeAndFlags(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	var got Query
	source := &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		got = q
		results := []Hit{testSearchHit("testcrawl:1", "Test result", q.Text)}
		return SearchResult{Results: results, TotalMatches: 12, Truncated: true}, nil
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "--json", "hello"}, source, runOptions{})
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

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--json", "--limit", "3", "--after", "2026-01-01T00:00:00Z", "--before", "2026-01-31T00:00:00Z", "--who", "Ada"}, source, runOptions{})
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

		code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--json", "--before", "2026-01-31"}, source, runOptions{})
		if code != 0 {
			t.Fatalf("date-only before search code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		wantBefore := time.Date(2026, 1, 31, 23, 59, 59, 0, fixedLocal).UTC().Format(time.RFC3339)
		if got.Before.Format(time.RFC3339) != wantBefore {
			t.Fatalf("date-only before = %s, want %s", got.Before.Format(time.RFC3339), wantBefore)
		}
	}()

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--json", "--all"}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "flag provided but not defined: -all") || stderr != "" {
		t.Fatalf("deleted all flag code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "--json", "--who", "Ada"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("filter-only search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "" || got.Who != "Ada" {
		t.Fatalf("filter-only query = %#v", got)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--json", "--who", ""}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "search --who requires an identity") || stderr != "" {
		t.Fatalf("empty who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--json", "--limit", "0"}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "--limit must be at least 1") || stderr != "" {
		t.Fatalf("bad limit code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "--json", "--", "--help"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("dash query search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Text != "--help" {
		t.Fatalf("dash query = %#v", got)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "--json"}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "search needs a query or filter") || stderr != "" {
		t.Fatalf("empty search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunRejectsUnknownFlagsBeforeOpeningArchive(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{
		Name: "messages",
		Flags: func(fs *flag.FlagSet) {
			fs.Int("limit", 20, "maximum messages")
		},
		Run: func(ctx context.Context, req *Request) error {
			t.Fatal("bespoke verb ran despite unknown flag")
			return nil
		},
	}}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "needle", "--all"}, source, runOptions{})
	combined := stdout + stderr
	if code != 2 || !strings.Contains(combined, "flag provided but not defined: -all") || strings.Contains(combined, "archive does not exist") {
		t.Fatalf("search unknown flag code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"messages", "--all"}, source, runOptions{})
	combined = stdout + stderr
	if code != 2 || !strings.Contains(combined, "flag provided but not defined: -all") || strings.Contains(combined, "archive does not exist") {
		t.Fatalf("bespoke unknown flag code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunSearchTextShowsTruncationSummary(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
		return SearchResult{
			Results:      []Hit{testSearchHit("testcrawl:1", "Test result", q.Text)},
			TotalMatches: 3,
			Truncated:    true,
		}, nil
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "hello"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("search text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		`Search "hello": showing 1 of 3, newest first.`,
		"Open: trawl test open REF",
		`More: trawl test search "hello" --limit 2`,
		"Narrow results with --who, --after, or --before.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("search text missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunSearchTruncationHintUsesManifestFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		source  Crawler
		want    string
		notWant string
	}{
		{
			name:    "who-less",
			source:  &testSearchCrawler{},
			want:    "Narrow results with --after or --before.",
			notWant: "--who",
		},
		{
			name: "who-capable",
			source: &testCrawler{searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
				return SearchResult{
					Results:      []Hit{testSearchHit("testcrawl:1", "Test result", q.Text)},
					TotalMatches: 3,
					Truncated:    true,
				}, nil
			}},
			want: "Narrow results with --who, --after, or --before.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateRoot := t.TempDir()
			createArchive(t, stateRoot)
			code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "hello"}, tc.source, runOptions{})
			if code != 0 {
				t.Fatalf("search text code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Fatalf("search text missing %q:\n%s", tc.want, stdout)
			}
			if tc.notWant != "" && strings.Contains(stdout, tc.notWant) {
				t.Fatalf("search text includes %q:\n%s", tc.notWant, stdout)
			}
		})
	}
}

func TestRunSyncJSONIncludesZeroCounts(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	opts := childTestOptions(t, "zero_sync")
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
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
	sourceRoot := ""
	source := &testCrawler{verbs: []Verb{{
		Name: "sync",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&includeArchived, "include-archived", false, "include archived items")
			fs.StringVar(&sourceRoot, "source-root", "", "source root")
		},
	}}}
	opts := childTestOptions(t, "sync_flag")
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--include-archived", "--source-root", "synthetic", "--json"}, source, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 7`) {
		t.Fatalf("sync flag code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunPrefixedBespokeSyncDoesNotNeedSyncer(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testStatusCrawler{verbs: []Verb{{
		Name:  "sync apple",
		Help:  "Preview Apple Contacts sync",
		Store: StoreNone,
		Run: func(ctx context.Context, req *Request) error {
			if len(req.Args) > 0 {
				return fmt.Errorf("args = %v", req.Args)
			}
			_, err := req.Out.Write([]byte("apple sync preview\n"))
			return err
		},
	}}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "apple", "--json"}, source, runOptions{})
	if code != 0 || stdout != "apple sync preview\n" || stderr != "" {
		t.Fatalf("sync apple code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"sync", "--json"}, source, runOptions{})
	if code != 2 || stderr != "" || !strings.Contains(stdout, "source does not support sync") {
		t.Fatalf("sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	if _, ok := manifest.Commands["sync"]; ok {
		t.Fatalf("metadata advertised generic sync: %#v", manifest.Commands["sync"])
	}
	if got := manifest.Commands["sync_apple"]; got.Title != "Preview Apple Contacts sync" || got.Mutates {
		t.Fatalf("sync_apple command = %#v", got)
	}
}

func TestRunMetadataAdvertisesSpineVerbFlags(t *testing.T) {
	stateRoot := t.TempDir()
	includeArchived := false
	sourceRoot := ""
	source := &testCrawler{verbs: []Verb{{
		Name: "sync",
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&includeArchived, "include-archived", false, "include archived items")
			fs.StringVar(&sourceRoot, "source-root", "", "source root")
		},
	}}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	syncCommand := manifest.Commands["sync"]
	if syncCommand.Title != "Sync the archive" || !syncCommand.Mutates || len(syncCommand.Flags) != 2 {
		t.Fatalf("sync command = %#v", syncCommand)
	}
	flags := map[string]control.Flag{}
	for _, flag := range syncCommand.Flags {
		flags[flag.Name] = flag
	}
	if flag := flags["include-archived"]; flag.Usage != "include archived items" || flag.Default != "false" {
		t.Fatalf("sync bool flag = %#v", flag)
	}
	if flag := flags["source-root"]; flag.Usage != "source root" || flag.Default != "" {
		t.Fatalf("sync string flag = %#v", flag)
	}
	if got, want := strings.Join(syncCommand.Argv[1:], " "), "sync --json"; got != want {
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
			return SearchResult{Results: []Hit{testSearchHit("testcrawl:1", "Test result", q.Text)}, TotalMatches: 1}, nil
		},
	}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "exact", "--json"}, source, runOptions{})
	if code != 0 || got.Text != "exact" || exact {
		t.Fatalf("bare query code=%d exact=%t query=%#v stdout=%s stderr=%s", code, exact, got, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--exact", "--json"}, source, runOptions{})
	if code != 0 || got.Text != "needle" || !exact {
		t.Fatalf("flagged query code=%d exact=%t query=%#v stdout=%s stderr=%s", code, exact, got, stdout, stderr)
	}

	exact = false
	got = Query{}
	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "--exact", "--json", "--", "--literal"}, source, runOptions{})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"who", "--exact", "--json", "--", "Ada"}, source, runOptions{})
	if code != 0 || gotPerson != "Ada" || !exact {
		t.Fatalf("who delimiter code=%d exact=%t person=%q stdout=%s stderr=%s", code, exact, gotPerson, stdout, stderr)
	}
}

func TestRunSpineContactExportStoreNoneFreshNoArchive(t *testing.T) {
	stateRoot := t.TempDir()
	archivePath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	source := &testContactCrawler{
		testCrawler: &testCrawler{verbs: []Verb{{
			Name:  "contacts_export",
			Store: StoreNone,
		}}},
		contactExportFn: func(ctx context.Context, req *Request) (*control.ContactExport, error) {
			if req.Store != nil {
				t.Fatalf("contacts_export Store = %#v, want nil", req.Store)
			}
			if req.Paths.Archive != archivePath {
				t.Fatalf("contacts_export archive path = %q, want %q", req.Paths.Archive, archivePath)
			}
			return &control.ContactExport{Contacts: []control.Contact{{
				DisplayName:  "Ada Example",
				PhoneNumbers: []string{"+15550100"},
			}}}, nil
		},
	}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("manifest json = %s err=%v", stdout, err)
	}
	if got := manifest.Commands["contacts_export"].Store; got != "none" {
		t.Fatalf("contacts_export manifest store = %q, want none", got)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"contacts", "export", "--json"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, `"contacts"`) {
		t.Fatalf("contacts_export StoreNone code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("contacts_export StoreNone created archive: err=%v path=%s", err, archivePath)
	}
	t.Logf("contacts_export_store_none_archive_exists=false archive_path=%s", archivePath)
}

func TestRunSpineSearchStoreOptionalFreshNoArchive(t *testing.T) {
	stateRoot := t.TempDir()
	archivePath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	source := &testCrawler{
		verbs: []Verb{{
			Name:  "search",
			Store: StoreOptional,
		}},
		searchFn: func(ctx context.Context, req *Request, q Query) (SearchResult, error) {
			if req.Store != nil {
				t.Fatalf("search Store = %#v, want nil", req.Store)
			}
			return SearchResult{Results: []Hit{}, TotalMatches: 0}, nil
		},
	}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "--json", "needle"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, `"results"`) {
		t.Fatalf("search StoreOptional code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("search StoreOptional created archive: err=%v path=%s", err, archivePath)
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
	wantDetail := "spine verb declarations may only set Name, Flags, and Store"
	wantRemedy := "Remove Help, Run, Mutates, Timeout, and Args from the sync Verb declaration."

	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantDetail) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("metadata invalid declaration code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"sync", "--json"}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantDetail) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("sync invalid declaration code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"status"}, source, runOptions{})
	if code != 1 || stdout != "" || !strings.Contains(stderr, wantMessage) || !strings.Contains(stderr, wantDetail) || !strings.Contains(stderr, wantRemedy) {
		t.Fatalf("status invalid declaration code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestManifestValidatesHeadlines(t *testing.T) {
	cases := []struct {
		name      string
		headlines []string
		want      string
	}{
		{name: "empty", headlines: []string{""}, want: "entries must not be empty"},
		{name: "padded", headlines: []string{" chats"}, want: "entries must already be trimmed"},
		{name: "duplicate", headlines: []string{"chats", "chats"}, want: "entries must be distinct"},
		{name: "fifth", headlines: []string{"a", "b", "c", "d", "e"}, want: "at most four entries are allowed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Manifest(&testCrawler{headlines: tc.headlines})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Manifest() error = %v, want %q", err, tc.want)
			}
		})
	}
	manifest, err := Manifest(&testCrawler{
		headlines: []string{"emails", "labels"},
		verbs:     []Verb{{Name: "renamed command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(manifest.Headlines, []string{"emails", "labels"}) {
		t.Fatalf("headlines = %#v", manifest.Headlines)
	}
}

func TestRunRejectsSpineStoreWidening(t *testing.T) {
	stateRoot := t.TempDir()
	cases := []struct {
		name        string
		source      Crawler
		args        []string
		wantMessage string
		wantRemedy  string
	}{
		{
			name: "read spine cannot declare required store",
			source: &testCrawler{verbs: []Verb{{
				Name:  "search",
				Store: StoreRequired,
			}}},
			args:        []string{"search", "needle", "--json"},
			wantMessage: "invalid search Verb declaration: StoreRequired is not valid; use StoreNone or StoreOptional",
			wantRemedy:  "Remove Store from the search Verb declaration, or set Store to StoreNone or StoreOptional.",
		},
		{
			name: "sync cannot declare store",
			source: &testCrawler{verbs: []Verb{{
				Name:  "sync",
				Store: StoreNone,
			}}},
			args:        []string{"sync", "--json"},
			wantMessage: "invalid sync Verb declaration: StoreNone is not valid; sync always writes the archive",
			wantRemedy:  "Remove Store from the sync Verb declaration; sync always writes the archive.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, tc.source, runOptions{})
			if code != 1 || stderr != "" || !strings.Contains(stdout, tc.wantMessage) || !strings.Contains(stdout, tc.wantRemedy) {
				t.Fatalf("metadata invalid Store code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}

			code, stdout, stderr = runForTestAt(stateRoot, tc.args, tc.source, runOptions{})
			if code != 1 || stderr != "" || !strings.Contains(stdout, tc.wantMessage) || !strings.Contains(stdout, tc.wantRemedy) {
				t.Fatalf("invocation invalid Store code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
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
			args:        []string{"search", "needle", "--json"},
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
			args:        []string{"sync", "--json"},
			wantMessage: "crawler flag --json collides with a runner-owned flag",
			wantRemedy:  "Remove --json from the sync Verb declaration; the runner owns that flag.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, tc.source, runOptions{})
			if code != 1 || stderr != "" || !strings.Contains(stdout, tc.wantMessage) || !strings.Contains(stdout, tc.wantRemedy) {
				t.Fatalf("metadata collision code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}

			code, stdout, stderr = runForTestAt(stateRoot, tc.args, tc.source, runOptions{})
			if code != 1 || stderr != "" || !strings.Contains(stdout, tc.wantMessage) || !strings.Contains(stdout, tc.wantRemedy) {
				t.Fatalf("invocation collision code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
		})
	}
}

func TestRunUnsupportedSpineVerbDeclarationKeepsInvocationUsageError(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testStatusCrawler{verbs: []Verb{{Name: "sync"}}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, source, runOptions{})
	if code != 2 || stderr != "" || !strings.Contains(stdout, "source does not support sync") || strings.Contains(stdout, "invalid sync Verb declaration") {
		t.Fatalf("unsupported sync invocation code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, "invalid sync Verb declaration: source does not implement Syncer") || !strings.Contains(stdout, "Implement trawlkit.Syncer or remove the sync Verb declaration.") {
		t.Fatalf("unsupported sync metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunRejectsDuplicateSpineVerbDeclaration(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{verbs: []Verb{{Name: "sync"}, {Name: "sync"}}}
	wantMessage := "invalid sync Verb declaration: declared more than once"
	wantRemedy := "Keep one sync Verb declaration and remove the duplicate."

	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
	if code != 1 || stderr != "" || !strings.Contains(stdout, wantMessage) || !strings.Contains(stdout, wantRemedy) {
		t.Fatalf("duplicate metadata code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"sync", "--json"}, source, runOptions{})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"status"}, source, runOptions{})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"who", "Ada"}, source, runOptions{})
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
	code, stdout, stderr := runSourcesForTestAt(stateRoot, []string{"shared", "status", "--json"}, []Crawler{
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
				Results:      []Hit{testSearchHit("testcrawl:1", "Ada Example", q.Text)},
				TotalMatches: 1,
			}, nil
		},
	}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "needle", "--json", "--who", "Ada"}, source, runOptions{})
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
				Results:      []Hit{testSearchHit("testcrawl:1", "Ada Example", q.Text)},
				TotalMatches: 1,
			}, nil
		},
	}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "needle", "--who", "Ada"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("search --who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `Search "needle" with Ada Example: showing 1 of 1, newest first.`) {
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"who", "Ada", "--json"}, source, runOptions{})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "needle", "--json", "--who", "Ada"}, source, runOptions{})
	if code != 4 || stderr != "" || !strings.Contains(stdout, `"code":"ambiguous_who"`) || !strings.Contains(stdout, `"candidates"`) {
		t.Fatalf("ambiguous who code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	source.whoFn = func(ctx context.Context, req *Request, person string) ([]whomatch.Candidate, error) {
		return nil, nil
	}
	code, stdout, stderr = runForTestAt(stateRoot, []string{"search", "needle", "--json", "--who", "Ada"}, source, runOptions{})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "needle", "--who", "Ada"}, source, runOptions{})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"search", "hello"}, &testCrawler{}, runOptions{})
	if code != 1 {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("text error wrote stdout: %q", stdout)
	}
	if stderr != "Error: This source is not ready yet.\n" || strings.Contains(stderr, stateRoot) {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestOpenStoreMissingArchiveKeepsPathOutOfErrorBody(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "synthetic-missing.db")
	_, err := openStore(context.Background(), Paths{Archive: archive}, storeRead)
	if err == nil {
		t.Fatal("openStore returned nil error")
	}
	var missing MissingArchiveError
	if !errors.As(err, &missing) {
		t.Fatalf("error type = %T, want MissingArchiveError", err)
	}
	body := errorBodyFor(err)
	if err.Error() != "This source is not ready yet." || body.Code != "unavailable" || body.Message != "This source is not ready yet." || body.Remedy != "" || strings.Contains(err.Error(), archive) || strings.Contains(body.Message, archive) || strings.Contains(body.Remedy, archive) {
		t.Fatalf("missing archive error=%q body=%#v", err, body)
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"metadata", "--json"}, source, runOptions{})
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
		{name: "top", args: []string{"--help"}, want: "Commands:\n"},
		{name: "verb", args: []string{"status", "--help"}, want: "test status:"},
		{name: "help command", args: []string{"help", "search"}, want: "--limit VALUE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runForTestAt(stateRoot, tc.args, source, runOptions{})
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
			assertNoInternalFlagTokens(t, stdout)
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"help", "archive", "import"}, source, runOptions{})
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
	assertNoInternalFlagTokens(t, stdout)
}

func TestRunHelpDoesNotExposeInternalStateFlags(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testOpenContactCrawler{testContactCrawler: &testContactCrawler{testCrawler: &testCrawler{}}}
	for _, args := range [][]string{
		{"--help"},
		{"help", "metadata"},
		{"help", "status"},
		{"help", "sync"},
		{"help", "search"},
		{"help", "who"},
		{"help", "open"},
		{"help", "contacts", "export"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, stdout, stderr := runForTestAt(stateRoot, args, source, runOptions{})
			if code != 0 || stderr != "" {
				t.Fatalf("help code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			assertNoInternalFlagTokens(t, stdout)
		})
	}

	code, stdout, stderr := runSourcesForTestAt(stateRoot, []string{"--help"}, []Crawler{
		&testCrawler{id: "one", surface: "one"},
		&testCrawler{id: "two", surface: "two"},
	}, runOptions{})
	if code != 0 || stderr != "" {
		t.Fatalf("root help code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertNoInternalFlagTokens(t, stdout)
}

func TestRunWorldMustChangeUsesSharedErrorBody(t *testing.T) {
	stateRoot := t.TempDir()
	source := &testCrawler{statusFn: func(ctx context.Context, req *Request) (*control.Status, error) {
		return nil, cklog.WorldMustChange{
			Message: "cannot read source",
			Remedy:  "grant Full Disk Access",
		}
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"status", "--json"}, source, runOptions{})
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

	code, stdout, stderr = runForTestAt(stateRoot, []string{"status"}, source, runOptions{})
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
		{name: "metadata", args: []string{"metadata"}, want: "Metadata\n"},
		{name: "status", args: []string{"status"}, want: "Status: ok\nRecently synced.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, _ := runForTestAt(stateRoot, tc.args, source, runOptions{})
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"search", "hello"}, source, runOptions{})
	if code != 0 || !strings.Contains(stdout, `Search "hello": showing 1 of 1, newest first.`) || strings.Contains(stdout, "&{") {
		t.Fatalf("search text code=%d stdout=%s", code, stdout)
	}

	opts := childTestOptions(t, "progress")
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync"}, source, opts)
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"archive", "import", "--dry-run", "/tmp/archive.zip"}, source, runOptions{})
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
	code, stdout, _ = runForTestAt(stateRoot, []string{"archive", "import", "/tmp/archive.zip", "--dry-run"}, source, runOptions{})
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"search", "--json", "wait"}, source, runOptions{readTimeout: 20 * time.Millisecond})
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
	code, stdout, _ := runForTestAt(stateRoot, []string{"search", "--json", "sqlite"}, source, runOptions{readTimeout: 20 * time.Millisecond})
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("progress child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	opts = childTestOptions(t, "hold")
	code, stdout, stderr = runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
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
	code, stdout, stderr = runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("rerun after killed child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunChildWireCarriesStateRootAndRunIDThroughEnv(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	opts := childTestOptions(t, "env_wire")
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_EXPECT_STATE_ROOT="+stateRoot)
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("env wire child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunWireChildRequiresStateRootAndRunIDEnv(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   string
		value string
		unset bool
		want  string
	}{
		{
			name:  "missing state root",
			env:   childStateRootEnv,
			unset: true,
			want:  "TRAWLKIT_STATE_ROOT is required",
		},
		{
			name:  "empty state root",
			env:   childStateRootEnv,
			value: "",
			want:  "TRAWLKIT_STATE_ROOT is required",
		},
		{
			name:  "missing run id",
			env:   childRunIDEnv,
			unset: true,
			want:  "TRAWLKIT_RUN_ID is required",
		},
		{
			name:  "empty run id",
			env:   childRunIDEnv,
			value: "",
			want:  "TRAWLKIT_RUN_ID is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setEnvForTest(t, childStateRootEnv, t.TempDir())
			setEnvForTest(t, childRunIDEnv, "run-1")
			if tc.unset {
				unsetEnvForTest(t, tc.env)
			} else {
				setEnvForTest(t, tc.env, tc.value)
			}

			code, frame, stderr := runWireForTest(t, []string{HiddenWireSubcommand, "--json", "testcrawl", "sync"}, &testCrawler{}, runOptions{})
			if code != 2 || stderr != "" || frame.kind != childFrameResult || frame.errorBody == nil {
				t.Fatalf("wire env failure code=%d frame=%#v stderr=%s", code, frame, stderr)
			}
			if frame.errorBody.Code != "usage" || frame.errorBody.Message != tc.want || frame.errorBody.Remedy != "invoke the parent crawler command; the runner supplies TRAWLKIT_STATE_ROOT and TRAWLKIT_RUN_ID for hidden wire child runs" {
				t.Fatalf("wire env error body = %#v, want message %q", frame.errorBody, tc.want)
			}
			t.Logf("hidden wire env failure proof: env=%s code=%d message=%q remedy=%q", tc.env, code, frame.errorBody.Message, frame.errorBody.Remedy)
		})
	}
}

func TestRunChildWireOverwritesStaleParentStateRootEnv(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	setEnvForTest(t, childStateRootEnv, filepath.Join(t.TempDir(), "stale"))
	opts := childTestOptions(t, "env_wire")
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_EXPECT_STATE_ROOT="+stateRoot)
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("stale env wire child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	t.Log("stale TRAWLKIT_STATE_ROOT overwritten by parent state root: true")
}

// countingWatchdogTimer is a watchdogTimer that never fires. It counts how many
// times the child watchdog is reset. The log-frame test injects it so the proof
// rests on frame delivery, not on a real timer racing wall-clock scheduling.
type countingWatchdogTimer struct {
	resets int
}

func (t *countingWatchdogTimer) tick() <-chan time.Time { return nil }
func (t *countingWatchdogTimer) reset(time.Duration)    { t.resets++ }
func (t *countingWatchdogTimer) stop()                  {}

func TestRunChildWireLogFramesResetWatchdog(t *testing.T) {
	const logHeartbeats = 5
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	opts := childTestOptions(t, "log_heartbeat")
	// The child streams logHeartbeats log frames and then a result frame. Each
	// frame resets the watchdog. Drive the watchdog with a fake timer that never
	// fires so the test proves the reset happens on every frame, whatever the
	// wall-clock gaps between frames turn out to be under parallel load.
	watchdog := &countingWatchdogTimer{}
	opts.newWatchdogTimer = func(time.Duration) watchdogTimer { return watchdog }
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
	if code != 0 || !strings.Contains(stdout, `"added": 1`) {
		t.Fatalf("log heartbeat child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// One reset per log frame proves every log frame reset the watchdog. The
	// result frame resets it once more, so the count is at least logHeartbeats.
	if watchdog.resets < logHeartbeats {
		t.Fatalf("watchdog reset %d times, want at least %d (one per log frame)", watchdog.resets, logHeartbeats)
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"archive", "import", "--json", "/tmp/archive.zip"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "bespoke:/tmp/archive.zip") {
		t.Fatalf("bespoke child code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	opts = childTestOptions(t, "bespoke")
	code, stdout, stderr = runForTestAt(stateRoot, []string{"archive", "import", "--json", "--", "/tmp/archive.zip", "--literal"}, source, opts)
	if code != 0 || !strings.Contains(stdout, "bespoke:/tmp/archive.zip --literal") {
		t.Fatalf("bespoke child with -- code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestRunMutatingBespokeStoreRequiredMatchesDefault(t *testing.T) {
	results := map[string]struct {
		stdout string
		stderr string
	}{}
	for _, tc := range []struct {
		name  string
		store StoreAccess
		env   string
	}{
		{name: "default", store: StoreDefault},
		{name: "required", store: StoreRequired, env: "required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateRoot := t.TempDir()
			archivePath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
			source := &testCrawler{verbs: []Verb{{
				Name:    "archive import",
				Help:    "Import an archive.",
				Args:    []string{"PATH"},
				Mutates: true,
				Store:   tc.store,
			}}}
			opts := childTestOptions(t, "bespoke")
			if tc.env != "" {
				opts.childEnv = append(opts.childEnv, "TRAWLKIT_CHILD_BESPOKE_STORE="+tc.env)
			}
			code, stdout, stderr := runForTestAt(stateRoot, []string{"archive", "import", "--json", "/tmp/archive.zip"}, source, opts)
			if code != 0 || !strings.Contains(stdout, "bespoke:/tmp/archive.zip") {
				t.Fatalf("%s code=%d stdout=%s stderr=%s", tc.name, code, stdout, stderr)
			}
			if _, err := os.Stat(archivePath); err != nil {
				t.Fatalf("%s archive was not created at %s: %v", tc.name, archivePath, err)
			}
			results[tc.name] = struct {
				stdout string
				stderr string
			}{stdout: stdout, stderr: stderr}
		})
	}
	if results["default"] != results["required"] {
		t.Fatalf("StoreRequired mutating output differs from StoreDefault: default=%#v required=%#v", results["default"], results["required"])
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"archive", "import", "--json", "/tmp/archive.zip"}, source, opts)
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
	code, stdout, stderr := runForTestAt(stateRoot, []string{"archive", "import", "--json", "/tmp/archive.zip"}, source, opts)
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
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_TREE_MARKER="+marker)
	code, stdout, stderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
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
	cmd.Env = append(os.Environ(), "TRAWLKIT_LOCK_PARENT=1")
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
	code, retryStdout, retryStderr := runForTestAt(stateRoot, []string{"sync", "--json"}, &testCrawler{}, opts)
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

func TestRunStoreNoneMutatingBespokeVerbReexecsAndHoldsRunLock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("parent-death process semantics differ on Windows")
	}
	stateRoot := t.TempDir()
	archivePath := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	marker := filepath.Join(t.TempDir(), "store-none-lock-holder-pid")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=TestRunnerStoreNoneLockParentHelper", "--", stateRoot, marker) // #nosec G204 -- test helper path and marker are controlled by the test.
	cmd.Env = append(os.Environ(), "TRAWLKIT_STORE_NONE_LOCK_PARENT=1")
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	childPID := waitForPIDMarker(t, marker, done, func() string { return stderr.String() })
	defer func() {
		killProcessAndWaitForLock(t, childPID, filepath.Join(stateRoot, "testcrawl"))
		select {
		case <-done:
		case <-time.After(time.Second):
			_ = cmd.Process.Kill()
			t.Fatalf("store none lock parent did not exit stdout=%s stderr=%s", stdout.String(), stderr.String())
		}
	}()

	source := &testCrawler{verbs: maintenanceFixtureVerbs(func(ctx context.Context, req *Request, name string) error {
		if req.Store != nil {
			t.Fatalf("%s Store = %#v, want nil", name, req.Store)
		}
		_, err := fmt.Fprintf(req.Out, "%s store=nil\n", name)
		return err
	})}
	opts := childTestOptions(t, "maintenance_init")
	code, retryStdout, retryStderr := runForTestAt(stateRoot, []string{"maintenance", "init", "--json"}, source, opts)
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
		t.Fatalf("second StoreNone mutating run while child holds lock code=%d stdout=%s stderr=%s", code, retryStdout, retryStderr)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("StoreNone mutating verb created archive: err=%v path=%s", err, archivePath)
	}
}

func TestRunChildWireForwardsLogLinesVerbatim(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	line := "2026-07-07 10:20:30 crawler WARN child_warn: message=kept"
	opts := childTestOptions(t, "odd_log")
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_ODD_LOG_LINE="+line)
	code, stdout, stderr := runForTestAt(stateRoot, []string{"-v", "sync", "--json"}, &testCrawler{}, opts)
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
			code, stdout, stderr := runForTestAt(stateRoot, []string{tc.verbose, "sync", "--json"}, &testCrawler{}, opts)
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
	cmd.Env = append(os.Environ(), "TRAWLKIT_FRAME_THEN_HANG=1")
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
	result := waitForChild(context.Background(), cmd, stdout, stderr.String, 30*time.Millisecond, 20*time.Millisecond, nil, 0, io.Discard, nil)
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
	if os.Getenv("TRAWLKIT_RUNNER_CHILD") != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	includeArchived := false
	sourceRoot := ""
	declaredBespokeStateRoot := ""
	declaredSyncStateRoot := ""
	verbs := []Verb{
		{
			Name:    "archive import",
			Help:    "Import an archive.",
			Args:    []string{"PATH"},
			Mutates: true,
			Store:   childBespokeStoreAccess(),
			Flags: func(fs *flag.FlagSet) {
				fs.StringVar(&declaredBespokeStateRoot, "state-root", "", "crawler-owned state root")
			},
			Run: func(ctx context.Context, req *Request) error {
				switch os.Getenv("TRAWLKIT_CHILD_MODE") {
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
				case "declared_state_root_bespoke":
					if err := writeDeclaredStateRootProof(req, "bespoke", declaredBespokeStateRoot); err != nil {
						return err
					}
					req.Progress(Progress{Phase: "import", Done: 1, Total: 1, Message: "imported archive"})
					_, err := req.Out.Write([]byte("bespoke:" + strings.Join(req.Args, " ") + "\n"))
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
				fs.StringVar(&sourceRoot, "source-root", "", "source root")
				fs.StringVar(&declaredSyncStateRoot, "state-root", "", "crawler-owned state root")
			},
		},
	}
	verbs = append(verbs, maintenanceFixtureVerbs(func(ctx context.Context, req *Request, name string) error {
		switch os.Getenv("TRAWLKIT_CHILD_MODE") {
		case "maintenance_init":
			if name != "maintenance init" {
				return fmt.Errorf("wrong maintenance child verb %s", name)
			}
			if req.Store != nil {
				return errors.New("maintenance init Store is not nil")
			}
			_, err := io.WriteString(req.Out, "maintenance init store=nil\n")
			return err
		case "store_none_lock_hold":
			if name != "maintenance init" {
				return fmt.Errorf("wrong lock child verb %s", name)
			}
			if req.Store != nil {
				return errors.New("store none lock holder Store is not nil")
			}
			marker := os.Getenv("TRAWLKIT_LOCK_MARKER")
			if marker == "" {
				return errors.New("missing lock marker")
			}
			if err := os.WriteFile(marker, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
				return err
			}
			select {}
		default:
			return errors.New("wrong child mode for maintenance verb")
		}
	})...)
	source := &testCrawler{
		verbs: verbs,
		syncFn: func(ctx context.Context, req *Request) (*SyncReport, error) {
			switch os.Getenv("TRAWLKIT_CHILD_MODE") {
			case "zero_sync", "prepare_archive_ok":
				return &SyncReport{}, nil
			case "progress":
				req.Progress(Progress{Phase: "sync", Done: 1, Total: 1, Message: "synced one item"})
				return &SyncReport{Added: 1}, nil
			case "sync_flag":
				if !includeArchived {
					return nil, errors.New("sync flag was not parsed")
				}
				if sourceRoot != "synthetic" {
					return nil, errors.New("sync string flag was not parsed")
				}
				req.Progress(Progress{Phase: "sync", Done: 1, Total: 1, Message: "synced archived items"})
				return &SyncReport{Added: 7}, nil
			case "declared_state_root_sync":
				if err := writeDeclaredStateRootProof(req, "sync", declaredSyncStateRoot); err != nil {
					return nil, err
				}
				req.Progress(Progress{Phase: "sync", Done: 1, Total: 1, Message: "synced declared state root"})
				return &SyncReport{Added: 13}, nil
			case "env_wire":
				expectedRoot := os.Getenv("TRAWLKIT_EXPECT_STATE_ROOT")
				if expectedRoot == "" {
					return nil, errors.New("missing expected state root")
				}
				if got := os.Getenv(childStateRootEnv); got != expectedRoot {
					return nil, fmt.Errorf("%s = %q, want %q", childStateRootEnv, got, expectedRoot)
				}
				resolvedRoot := filepath.Dir(filepath.Dir(req.Paths.Archive))
				if resolvedRoot != expectedRoot {
					return nil, fmt.Errorf("resolved state root = %q, want %q", resolvedRoot, expectedRoot)
				}
				if req.Log == nil {
					return nil, errors.New("missing child run log")
				}
				if got := os.Getenv(childRunIDEnv); got == "" || req.Log.RunID() != got {
					return nil, fmt.Errorf("child run id = %q, env %s = %q", req.Log.RunID(), childRunIDEnv, got)
				}
				return &SyncReport{Added: 1}, nil
			case "log_heartbeat":
				for i := 0; i < 5; i++ {
					if err := req.Log.Info("log_heartbeat", "iteration="+strconv.Itoa(i+1)); err != nil {
						return nil, err
					}
					if err := ctx.Err(); err != nil {
						return nil, err
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
				line := os.Getenv("TRAWLKIT_ODD_LOG_LINE")
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
				marker := os.Getenv("TRAWLKIT_TREE_MARKER")
				if marker == "" {
					return nil, errors.New("missing tree marker")
				}
				cmd := exec.Command(os.Args[0], "-test.run=TestRunnerGrandchildHelper", "--", marker) // #nosec G204 -- test helper path and marker are controlled by the test.
				cmd.Env = append(os.Environ(), "TRAWLKIT_RUNNER_GRANDCHILD=1")
				cmd.Stdout = io.Discard
				cmd.Stderr = io.Discard
				if err := cmd.Start(); err != nil {
					return nil, err
				}
				req.Progress(Progress{Phase: "tree", Done: 1, Total: 1, Message: "grandchild started"})
				select {}
			case "lock_hold":
				marker := os.Getenv("TRAWLKIT_LOCK_MARKER")
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
		prepareFn: func(ctx context.Context, path string) error {
			switch os.Getenv("TRAWLKIT_CHILD_MODE") {
			case "prepare_archive_ok":
				marker := os.Getenv("TRAWLKIT_PREPARE_MARKER")
				if marker == "" {
					return errors.New("missing prepare marker")
				}
				existed := false
				if _, err := os.Stat(path); err == nil {
					existed = true
				}
				return os.WriteFile(marker, []byte(fmt.Sprintf("path=%s existed=%v\n", path, existed)), 0o600)
			case "prepare_archive_error":
				return errors.New("archive schema is newer than this build supports")
			default:
				return nil
			}
		},
	}
	code := runner{opts: defaultRunOptions()}.run(args, []Crawler{source})
	os.Exit(code)
}

func TestRunnerLockParentHelper(t *testing.T) {
	if os.Getenv("TRAWLKIT_LOCK_PARENT") != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	if len(args) != 2 {
		os.Exit(2)
	}
	opts := childTestOptions(t, "lock_hold")
	opts.watchdog = time.Hour
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_LOCK_MARKER="+args[1])
	opts.stateRoot = args[0]
	code := runner{opts: opts}.run([]string{"sync", "--json"}, []Crawler{&testCrawler{}})
	os.Exit(code)
}

func TestRunnerStoreNoneLockParentHelper(t *testing.T) {
	if os.Getenv("TRAWLKIT_STORE_NONE_LOCK_PARENT") != "1" {
		return
	}
	args := argsAfterDoubleDash(os.Args)
	if len(args) != 2 {
		os.Exit(2)
	}
	opts := childTestOptions(t, "store_none_lock_hold")
	opts.watchdog = time.Hour
	opts.childEnv = append(opts.childEnv, "TRAWLKIT_LOCK_MARKER="+args[1])
	opts.stateRoot = args[0]
	source := &testCrawler{verbs: maintenanceFixtureVerbs(func(ctx context.Context, req *Request, name string) error {
		return errors.New("store none lock parent should re-exec before running handler")
	})}
	code := runner{opts: opts}.run([]string{"maintenance", "init", "--json"}, []Crawler{source})
	os.Exit(code)
}

func TestRunnerFrameThenHangHelper(t *testing.T) {
	if os.Getenv("TRAWLKIT_FRAME_THEN_HANG") != "1" {
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
	if os.Getenv("TRAWLKIT_RUNNER_GRANDCHILD") != "1" {
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

func runForTestAt(stateRoot string, argv []string, source Crawler, opts runOptions) (int, string, string) {
	return runSourcesForTestAt(stateRoot, argv, []Crawler{source}, opts)
}

func runSourcesForTestAt(stateRoot string, argv []string, sources []Crawler, opts runOptions) (int, string, string) {
	opts.stateRoot = stateRoot
	return runSourcesForTest(argv, sources, opts)
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
	createArchiveAt(t, filepath.Join(stateRoot, "testcrawl", "testcrawl.db"))
}

func createArchiveAt(t *testing.T, path string) {
	t.Helper()
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

func maintenanceFixtureVerbs(run func(context.Context, *Request, string) error) []Verb {
	return []Verb{
		{
			Name:    "maintenance init",
			Help:    "Initialise maintenance state.",
			Mutates: true,
			Store:   StoreNone,
			Run: func(ctx context.Context, req *Request) error {
				return run(ctx, req, "maintenance init")
			},
		},
		{
			Name:  "maintenance status",
			Help:  "Show maintenance status.",
			Store: StoreNone,
			Run: func(ctx context.Context, req *Request) error {
				return run(ctx, req, "maintenance status")
			},
		},
		{
			Name:  "maintenance snapshots",
			Help:  "List maintenance snapshots.",
			Store: StoreNone,
			Run: func(ctx context.Context, req *Request) error {
				return run(ctx, req, "maintenance snapshots")
			},
		},
	}
}

func childBespokeStoreAccess() StoreAccess {
	switch os.Getenv("TRAWLKIT_CHILD_BESPOKE_STORE") {
	case "none":
		return StoreNone
	case "optional":
		return StoreOptional
	case "required":
		return StoreRequired
	default:
		return StoreDefault
	}
}

func writeDeclaredStateRootProof(req *Request, mode, declaredStateRoot string) error {
	expectedRoot := os.Getenv("TRAWLKIT_EXPECT_STATE_ROOT")
	if expectedRoot == "" {
		return errors.New("missing expected state root")
	}
	expectedDeclared := os.Getenv("TRAWLKIT_EXPECT_DECLARED_STATE_ROOT")
	if expectedDeclared == "" {
		return errors.New("missing expected declared state root")
	}
	if declaredStateRoot != expectedDeclared {
		return fmt.Errorf("declared state-root = %q, want %q", declaredStateRoot, expectedDeclared)
	}
	if got := os.Getenv(childStateRootEnv); got != expectedRoot {
		return fmt.Errorf("%s = %q, want %q", childStateRootEnv, got, expectedRoot)
	}
	resolvedRoot := filepath.Dir(filepath.Dir(req.Paths.Archive))
	if resolvedRoot != expectedRoot {
		return fmt.Errorf("resolved state root = %q, want %q", resolvedRoot, expectedRoot)
	}
	if req.Log == nil {
		return errors.New("missing child run log")
	}
	if got := os.Getenv(childRunIDEnv); got == "" || req.Log.RunID() != got {
		return fmt.Errorf("child run id = %q, env %s = %q", req.Log.RunID(), childRunIDEnv, got)
	}
	proofPath := os.Getenv("TRAWLKIT_DECLARED_STATE_ROOT_PROOF")
	if proofPath == "" {
		return errors.New("missing declared state root proof path")
	}
	proof := strings.Join([]string{
		"mode=" + mode,
		"declared_state_root=" + declaredStateRoot,
		"wire_state_root_matches_parent=true",
		"request_state_root_matches_parent=true",
		"child_run_id_matches_log=true",
	}, "\n") + "\n"
	return os.WriteFile(proofPath, []byte(proof), 0o600)
}

func childTestOptions(t *testing.T, mode string) runOptions {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "TRAWLKIT_RUNNER_CHILD=1", "TRAWLKIT_CHILD_MODE="+mode)
	return runOptions{
		executable:      exe,
		childPrefixArgs: []string{"-test.run=TestRunnerChildHelper", "--"},
		childEnv:        env,
		watchdog:        80 * time.Millisecond,
		killGrace:       20 * time.Millisecond,
	}
}

func runWireForTest(t *testing.T, argv []string, source Crawler, opts runOptions) (int, childFrame, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts.stdout = &stdout
	opts.stderr = &stderr
	if opts.signalContext == nil {
		opts.signalContext = func(ctx context.Context) (context.Context, context.CancelFunc) {
			return ctx, func() {}
		}
	}
	code := runner{opts: opts}.run(argv, []Crawler{source})
	frame, err := readChildFrame(bufio.NewReader(bytes.NewReader(stdout.Bytes())))
	if err != nil {
		t.Fatalf("wire stdout frame err=%v stdout=%q stderr=%s", err, stdout.String(), stderr.String())
	}
	return code, frame, stderr.String()
}

func argsAfterDoubleDash(args []string) []string {
	for i, arg := range args {
		if arg == "--" {
			return args[i+1:]
		}
	}
	return nil
}

func setEnvForTest(t *testing.T, key, value string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func unsetEnvForTest(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		}
	})
}

func commandHasFlag(command control.Command, name string) bool {
	for _, flag := range command.Flags {
		if flag.Name == name {
			return true
		}
	}
	return false
}

func readProofFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertProofLine(t *testing.T, proof, want string) {
	t.Helper()
	for _, line := range strings.Split(proof, "\n") {
		if line == want {
			return
		}
	}
	t.Fatalf("proof missing %q:\n%s", want, proof)
}

func assertNoInternalFlagTokens(t *testing.T, text string) {
	t.Helper()
	for _, token := range []string{"--state-root", "--trawlkit-run-id"} {
		if strings.Contains(text, token) {
			t.Fatalf("output contains internal flag token %s:\n%s", token, text)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
