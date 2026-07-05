package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/control"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	_ "modernc.org/sqlite"
)

func TestSyncBackupIngestAndShardIdempotence(t *testing.T) {
	fake := installFakeGog(t)
	workDir := filepath.Join("/private/tmp", "gogcrawl.test", strconv.Itoa(os.Getpid()))
	if err := os.RemoveAll(workDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })
	dbPath := filepath.Join(workDir, "gogcrawl.db")
	repoPath := filepath.Join(workDir, "backup")
	var firstStderr bytes.Buffer
	err := Run(context.Background(), []string{"-vv", "sync", "--query", "from:me", "--max", "25", "--json", "--archive", dbPath, "--backup-repo", repoPath}, &bytes.Buffer{}, &firstStderr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(firstStderr.String(), `"type":"progress"`) {
		t.Fatalf("sync did not emit crawlkit progress to stderr:\n%s", firstStderr.String())
	}
	logText := readTestLog(t, "gogcrawl.log")
	for _, want := range []string{
		"shard_done: shard=",
		"kind=messages",
		"shard_phase: shard=",
		"decrypt_ms=",
		"parse_ms=",
		"index_ms=",
		"subprocess_exec: argv=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("gogcrawl log missing %q:\n%s", want, logText)
		}
	}
	if got := countLogLines(t, fake.log, "backup gmail push"); got != 1 {
		t.Fatalf("backup push calls = %d, want 1", got)
	}
	if got := countLogLines(t, fake.log, "backup cat"); got != 2 {
		t.Fatalf("backup cat calls = %d, want 2", got)
	}
	st, err := archive.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 3 {
		t.Fatalf("messages after sync = %d, want 3", status.Messages)
	}
	open, err := st.OpenMessage(context.Background(), archive.RefPrefix+"m3")
	if err != nil {
		t.Fatal(err)
	}
	if open.Headers.ToAddress == "" || open.Headers.CcAddress == "" {
		t.Fatalf("headers = %#v", open.Headers)
	}
	search, err := st.Search(context.Background(), archive.SearchOptions{Query: "project", Who: "me", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 3 || !search.Truncated || len(search.Results) != 1 || search.Results[0].Who != "me" || search.Results[0].ShortRef == "" {
		t.Fatalf("owner short-ref search = %#v", search)
	}
	shortRef := search.Results[0].ShortRef
	openedByShortRef, err := st.OpenMessage(context.Background(), shortRef)
	if err != nil {
		t.Fatal(err)
	}
	if openedByShortRef.Ref != search.Results[0].Ref {
		t.Fatalf("short ref opened %q, want %q", openedByShortRef.Ref, search.Results[0].Ref)
	}
	var jsonOut bytes.Buffer
	if err := json.NewEncoder(&jsonOut).Encode(search); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"short_ref":"`+shortRef+`"`) {
		t.Fatalf("search JSON missing short ref %q:\n%s", shortRef, jsonOut.String())
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
	statusOut := runStatus(t, context.Background(), dbPath)
	if statusOut.LastRun == nil || statusOut.LastRun.Command != "sync" || statusOut.LastRun.Outcome != "success" {
		t.Fatalf("status log tail = %#v", statusOut.LastRun)
	}
	searchJSON := runOutput(t, context.Background(), []string{"search", "project", "--limit", "2", "--json", "--archive", dbPath})
	conformance.AssertSearchEnvelope(t, searchJSON)
	searchHuman := string(runOutput(t, context.Background(), []string{"search", "project", "--limit", "2", "--archive", dbPath}))
	assertSearchShortRefParity(t, searchJSON, searchHuman)
	conformance.AssertHumanOutput(t, searchHuman)
	assertNoMachineTimestamps(t, searchHuman)
	conformance.AssertHumanOutput(t, string(runOutput(t, context.Background(), []string{"status", "--archive", dbPath})))
	conformance.AssertHumanOutput(t, string(runOutput(t, context.Background(), []string{"doctor", "--archive", dbPath})))
}

func TestVerboseDoesNotLeakDebugToStderr(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()

	verboseDBPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	verboseRepoPath := filepath.Join(t.TempDir(), "backup")
	var verboseStderr bytes.Buffer
	err := Run(ctx, []string{"-v", "sync", "--query", "from:me", "--max", "25", "--archive", verboseDBPath, "--backup-repo", verboseRepoPath}, &bytes.Buffer{}, &verboseStderr)
	if err != nil {
		t.Fatalf("sync -v failed: %v\nstderr=%s", err, verboseStderr.String())
	}
	verboseText := verboseStderr.String()
	if !strings.Contains(verboseText, "INFO") || !strings.Contains(verboseText, "shard_done") {
		t.Fatalf("sync -v did not stream non-debug log lines:\n%s", verboseText)
	}
	for _, notWant := range []string{"DEBUG", "subprocess_exec", "shard_phase"} {
		if strings.Contains(verboseText, notWant) {
			t.Fatalf("sync -v stderr leaked %q:\n%s", notWant, verboseText)
		}
	}

	debugDBPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	debugRepoPath := filepath.Join(t.TempDir(), "backup")
	var debugStderr bytes.Buffer
	err = Run(ctx, []string{"-vv", "sync", "--query", "from:me", "--max", "25", "--archive", debugDBPath, "--backup-repo", debugRepoPath}, &bytes.Buffer{}, &debugStderr)
	if err != nil {
		t.Fatalf("sync -vv failed: %v\nstderr=%s", err, debugStderr.String())
	}
	debugText := debugStderr.String()
	for _, want := range []string{"DEBUG", "subprocess_exec: argv=", "shard_phase:"} {
		if !strings.Contains(debugText, want) {
			t.Fatalf("sync -vv stderr missing %q:\n%s", want, debugText)
		}
	}
}

func TestSearchTextWrapsLongQueryAndSnippetRows(t *testing.T) {
	t.Setenv("COLUMNS", "80")
	value := searchOutput{SearchResult: archive.SearchResult{
		Query:        strings.Repeat("q", 200),
		TotalMatches: 1,
		Results: []archive.SearchHit{{
			Time:     "2012-12-30T22:08:54+01:00",
			Who:      "Twitter",
			ShortRef: "abc12",
			Snippet:  "Do you know Томояяош's cнıʟD, RiFF RaFF and PBS on Twitter? " + strings.Repeat("synthetic old body ", 8),
		}},
	}}
	var buf bytes.Buffer
	if err := printSearchText(&buf, value); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	assertLinesWithinDisplayWidth(t, got, 80)
	if !strings.Contains(got, "…") {
		t.Fatalf("long query was not display-truncated:\n%s", got)
	}
	// Tripwire (TRAWL-91): search rows render through WriteList with a
	// labelled ref column, so short-ref codes never appear unexplained.
	if !searchHeaderPattern.MatchString(got) {
		t.Fatalf("search output missing labelled date/who/ref/text header:\n%s", got)
	}
	if !strings.Contains(got, "abc12") {
		t.Fatalf("search output missing short ref:\n%s", got)
	}
	if !strings.Contains(got, "Open: gogcrawl open REF") {
		t.Fatalf("search output missing Open hint:\n%s", got)
	}
	assertNoMachineTimestamps(t, got)
}

// Tripwires for the TRAWL-91 defect classes: unlabelled search columns and
// raw RFC3339 timestamps in human output.
var (
	searchHeaderPattern     = regexp.MustCompile(`(?m)^date\s+who\s+ref\s+text$`)
	machineTimestampPattern = regexp.MustCompile(`\d{2}T\d{2}:\d{2}:\d{2}`)
)

func assertNoMachineTimestamps(t *testing.T, humanOutput string) {
	t.Helper()
	if match := machineTimestampPattern.FindString(humanOutput); match != "" {
		t.Fatalf("human output leaked an RFC3339 timestamp %q:\n%s", match, humanOutput)
	}
}

func TestSearchTextEmptyPrintsOnlyEmptySentence(t *testing.T) {
	var buf bytes.Buffer
	if err := printSearchText(&buf, searchOutput{SearchResult: archive.SearchResult{Query: "boat"}}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); got != "No matches for \"boat\".\n" {
		t.Fatalf("empty search output = %q", got)
	}
}

func TestHumanModeErrorsOnEnvelopeWithoutRenderer(t *testing.T) {
	var buf bytes.Buffer
	r := &runtime{stdout: &buf}
	err := r.print(struct{ Unrendered bool }{})
	if err == nil || !strings.Contains(err.Error(), "no human renderer") {
		t.Fatalf("err = %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("human mode wrote output for an unrendered envelope: %q", buf.String())
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

func TestStatusAuthOmitsExpiryWhenCheckSucceeds(t *testing.T) {
	installFakeGog(t)
	t.Setenv("GOG_FAKE_AUTH_EXPIRES", "2000-01-02T03:04:05Z")
	var out map[string]any
	runJSON(t, context.Background(), []string{"status", "--json", "--archive", filepath.Join(t.TempDir(), "missing.db")}, &out)
	auth, ok := out["auth"].(map[string]any)
	if !ok {
		t.Fatalf("auth = %#v", out["auth"])
	}
	if auth["authorized"] != true {
		t.Fatalf("auth = %#v", auth)
	}
	if _, ok := auth["expires"]; ok {
		t.Fatalf("auth exposed stale expiry: %#v", auth)
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

func TestDoctorReportsInvalidAuthWithRemedy(t *testing.T) {
	installFakeGog(t)
	t.Setenv("GOG_FAKE_AUTH_VALID", "false")
	t.Setenv("GOG_FAKE_AUTH_EXPIRES", "2000-01-02T03:04:05Z")
	var out doctorOutput
	runJSON(t, context.Background(), []string{"doctor", "--json", "--archive", filepath.Join(t.TempDir(), "missing.db")}, &out)
	found := false
	for _, check := range out.Checks {
		if check.ID == "gog_auth" && check.State == "fail" && check.Remedy == "run gog login <email>" {
			found = true
		}
	}
	if !found {
		t.Fatalf("doctor checks = %#v", out.Checks)
	}
	status := runStatus(t, context.Background(), filepath.Join(t.TempDir(), "missing.db"))
	if status.Auth.Authorized {
		t.Fatalf("status auth = %#v", status.Auth)
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
	var manifest control.Manifest
	runJSON(t, context.Background(), []string{"metadata", "--json"}, &manifest)
	if manifest.ContractVersion != 1 || manifest.ID != "gogcrawl" {
		t.Fatalf("manifest = %#v", manifest)
	}
	for _, capability := range []string{"contacts_export", "short_refs", "who", "verbose_logs"} {
		if !contains(manifest.Capabilities, capability) {
			t.Fatalf("capabilities = %#v", manifest.Capabilities)
		}
	}
	if _, ok := manifest.Commands["who"]; !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
}

// TestMetadataHumanOutputHasNoCapabilityTokens pins TRAWL-125: the
// capability list exists for trawl's machine discovery, not for a human
// reader (rules.md §2.3). --json keeps it; the human card must not.
func TestMetadataHumanOutputHasNoCapabilityTokens(t *testing.T) {
	human := string(runOutput(t, context.Background(), []string{"metadata"}))
	for _, token := range []string{"Capabilities", "capabilities", "short_refs", "verbose_logs", "contacts_export"} {
		if strings.Contains(human, token) {
			t.Fatalf("metadata human output still contains %q:\n%s", token, human)
		}
	}
}

func TestHelpDocumentsWhoAndSearchResolution(t *testing.T) {
	installFakeGog(t)
	top := string(runOutput(t, context.Background(), []string{"--help"}))
	for _, want := range []string{
		"Read your archive:",
		"Keep it fresh:",
		"Health:",
		"Resolve a person across names and addresses.",
		"Run 'gogcrawl COMMAND --help' for flags and details.",
		"Diagnostics: run with -v, or read ~/.opentrawl/gogcrawl/logs/gogcrawl.log",
	} {
		if !strings.Contains(top, want) {
			t.Fatalf("top help missing %q:\n%s", want, top)
		}
	}
	search := string(runOutput(t, context.Background(), []string{"help", "search"}))
	for _, want := range []string{
		"gogcrawl search [QUERY]",
		"QUERY is optional when --who, --after or --before is present.",
		"Resolve a name, or filter by an exact email, phone or handle.",
		"Diagnostics: run with -v, or read ~/.opentrawl/gogcrawl/logs/gogcrawl.log",
	} {
		if !strings.Contains(search, want) {
			t.Fatalf("search help missing %q:\n%s", want, search)
		}
	}
	who := string(runOutput(t, context.Background(), []string{"help", "who"}))
	if !strings.Contains(who, "Resolve a Gmail participant name or identifier.") {
		t.Fatalf("who help = %s", who)
	}
}

func TestSearchRejectsEmptyWho(t *testing.T) {
	installFakeGog(t)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"search", "--who", " \t ", "project", "--archive", filepath.Join(t.TempDir(), "missing.db")}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "search --who requires an identity") {
		t.Fatalf("err = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
}

func TestSearchRejectsLimitBelowOne(t *testing.T) {
	installFakeGog(t)
	stdout, stderr, err := runExpectError(t, []string{"search", "needle", "--limit", "0", "--archive", filepath.Join(t.TempDir(), "missing.db")})
	if !strings.Contains(err.Error(), "--limit must be at least 1") {
		t.Fatalf("err = %v stdout=%s stderr=%s", err, stdout, stderr)
	}
}

func TestSearchRejectsAllWithLimit(t *testing.T) {
	installFakeGog(t)
	stdout, stderr, err := runExpectError(t, []string{"search", "needle", "--all", "--limit", "5", "--archive", filepath.Join(t.TempDir(), "missing.db")})
	if !strings.Contains(err.Error(), "use either --all or --limit") {
		t.Fatalf("err = %v stdout=%s stderr=%s", err, stdout, stderr)
	}
}

func TestSearchAllReturnsEverythingUntruncated(t *testing.T) {
	installFakeGog(t)
	// Above the ShortRefs chunk size so --all exercises the batched
	// short-ref lookup instead of one over-long SQL IN clause.
	const count = 1000
	dbPath := seedLimitArchive(t, count)

	var result archive.SearchResult
	runJSON(t, context.Background(), []string{"search", "needle", "--all", "--json", "--archive", dbPath}, &result)
	if len(result.Results) != count || result.TotalMatches != count || result.Truncated {
		t.Fatalf("search --all = len %d total %d truncated %t, want %d/%d/false", len(result.Results), result.TotalMatches, result.Truncated, count, count)
	}

	human := string(runOutput(t, context.Background(), []string{"search", "needle", "--all", "--archive", dbPath}))
	if strings.Contains(human, "More: ") {
		t.Fatalf("search --all reported hidden rows:\n%s", human)
	}
}

func TestSearchLimitAboveOldCapIsHonored(t *testing.T) {
	installFakeGog(t)
	const limit = 205
	dbPath := seedLimitArchive(t, limit)

	var result archive.SearchResult
	runJSON(t, context.Background(), []string{"search", "needle", "--limit", strconv.Itoa(limit), "--json", "--archive", dbPath}, &result)
	if len(result.Results) != limit || result.TotalMatches != limit || result.Truncated {
		t.Fatalf("search JSON limit = len %d total %d truncated %t, want %d/%d/false", len(result.Results), result.TotalMatches, result.Truncated, limit, limit)
	}

	human := string(runOutput(t, context.Background(), []string{"search", "needle", "--limit", strconv.Itoa(limit), "--archive", dbPath}))
	if got := strings.Count(human, "needle limit message "); got != limit {
		t.Fatalf("search human rows = %d, want %d\n%s", got, limit, human)
	}
	if strings.Contains(human, "More: ") {
		t.Fatalf("search human reported hidden rows:\n%s", human)
	}
}

func TestWhoCommandReturnsContractShapeAndHumanTable(t *testing.T) {
	installFakeGog(t)
	dbPath := seedArchive(t, []archive.Message{
		{ID: "m1", Time: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC), FromName: "Alice Example", FromAddress: "alice@example.com", Subject: "Project", Body: "Project body."},
		{ID: "m2", Time: time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC), FromName: "Alice A.", FromAddress: "alice@example.com", Subject: "Project", Body: "Another project body."},
	})
	var raw map[string]any
	runJSON(t, context.Background(), []string{"who", "alce", "--json", "--archive", dbPath}, &raw)
	if len(raw) != 2 || raw["query"] != "alce" {
		t.Fatalf("who JSON = %#v", raw)
	}
	candidates, ok := raw["candidates"].([]any)
	if !ok || len(candidates) != 1 {
		t.Fatalf("candidates = %#v", raw["candidates"])
	}
	candidate, ok := candidates[0].(map[string]any)
	if !ok {
		t.Fatalf("candidate = %#v", candidates[0])
	}
	for _, key := range []string{"who", "identifiers", "last_seen", "messages"} {
		if _, ok := candidate[key]; !ok {
			t.Fatalf("candidate missing %q: %#v", key, candidate)
		}
	}
	for key := range candidate {
		if key != "who" && key != "identifiers" && key != "last_seen" && key != "messages" {
			t.Fatalf("candidate has extra key %q: %#v", key, candidate)
		}
	}
	human := string(runOutput(t, context.Background(), []string{"who", "ali", "--archive", dbPath}))
	for _, want := range []string{"who", "identifiers", "last seen", "messages", "alice@example.com"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human who missing %q:\n%s", want, human)
		}
	}
	conformance.AssertHumanOutput(t, human)
	assertNoMachineTimestamps(t, human)
}

func TestSearchWhoResolutionOneManyZeroAndIdentifier(t *testing.T) {
	installFakeGog(t)
	dbPath := seedArchive(t, []archive.Message{
		{ID: "alice-new", Time: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), FromName: "Alice Example", FromAddress: "alice@example.com", Subject: "Project <alpha>", Body: "Needle & body."},
		{ID: "alice-old", Time: time.Date(2026, 7, 2, 11, 0, 0, 0, time.UTC), FromName: "Alice Example", FromAddress: "alice@example.com", Subject: "Project", Body: "Older needle body."},
		{ID: "casey-one", Time: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC), FromName: "Casey One", FromAddress: "casey.one@example.com", Subject: "Needle", Body: "First."},
		{ID: "casey-two", Time: time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC), FromName: "Casey Two", FromAddress: "casey.two@example.com", Subject: "Needle", Body: "Second."},
	})
	var resolved archive.SearchResult
	runJSON(t, context.Background(), []string{"search", "needle", "--who", "alice", "--json", "--archive", dbPath}, &resolved)
	if resolved.WhoResolved == nil || resolved.WhoResolved.Who != "Alice Example" || len(resolved.WhoResolved.Identifiers) != 1 {
		t.Fatalf("resolved search = %#v", resolved)
	}
	human := string(runOutput(t, context.Background(), []string{"search", "needle", "--who", "alice", "--archive", dbPath}))
	if !strings.Contains(human, "alice → Alice Example") {
		t.Fatalf("human search = %s", human)
	}

	var direct archive.SearchResult
	runJSON(t, context.Background(), []string{"search", "--who", "alice@example.com", "--limit", "1", "--json", "--archive", dbPath}, &direct)
	if direct.Query != "" || direct.WhoResolved != nil || direct.TotalMatches != 2 || !direct.Truncated || len(direct.Results) != 1 || direct.Results[0].Ref != archive.RefPrefix+"alice-new" {
		t.Fatalf("direct filter-only search = %#v", direct)
	}
	if raw := string(runOutput(t, context.Background(), []string{"search", "alpha", "--who", "alice@example.com", "--json", "--archive", dbPath})); strings.Contains(raw, `\u003c`) || strings.Contains(raw, `\u003e`) || strings.Contains(raw, `\u0026`) {
		t.Fatalf("search JSON escaped HTML characters:\n%s", raw)
	}

	stdout, stderr, err := runExpectError(t, []string{"search", "needle", "--who", "casey", "--json", "--archive", dbPath})
	if code := ExitCode(err); code != 4 {
		t.Fatalf("ambiguous exit = %d err=%v stdout=%s stderr=%s", code, err, stdout, stderr)
	}
	var ambiguous map[string]map[string]any
	if err := json.Unmarshal([]byte(stdout), &ambiguous); err != nil {
		t.Fatalf("ambiguous JSON: %v\n%s", err, stdout)
	}
	if ambiguous["error"]["code"] != "ambiguous_who" || len(ambiguous["error"]["candidates"].([]any)) != 2 {
		t.Fatalf("ambiguous envelope = %#v", ambiguous)
	}
	if !strings.Contains(err.Error(), "Retry with one identifier: gogcrawl search --who") {
		t.Fatalf("ambiguous human error = %v", err)
	}

	stdout, stderr, err = runExpectError(t, []string{"search", "needle", "--who", "nobody", "--json", "--archive", dbPath})
	if code := ExitCode(err); code != 5 {
		t.Fatalf("unknown exit = %d err=%v stdout=%s stderr=%s", code, err, stdout, stderr)
	}
	var unknown map[string]map[string]any
	if err := json.Unmarshal([]byte(stdout), &unknown); err != nil {
		t.Fatalf("unknown JSON: %v\n%s", err, stdout)
	}
	if unknown["error"]["code"] != "unknown_who" || unknown["error"]["hint"] == "" {
		t.Fatalf("unknown envelope = %#v", unknown)
	}
	if _, ok := unknown["error"]["did_you_mean"]; ok {
		t.Fatalf("unknown envelope emitted empty did_you_mean = %#v", unknown)
	}
}

func TestSearchRequiresQueryOnlyWithoutFilters(t *testing.T) {
	installFakeGog(t)
	stdout, stderr, err := runExpectError(t, []string{"search", "--archive", filepath.Join(t.TempDir(), "missing.db")})
	if err == nil || !strings.Contains(err.Error(), "search query is required unless --who, --after or --before is present") {
		t.Fatalf("err = %v stdout=%s stderr=%s", err, stdout, stderr)
	}
}

func TestStatusJSONUsesFreshnessLastSyncOnly(t *testing.T) {
	installFakeGog(t)
	dbPath := seedArchive(t, []archive.Message{
		{ID: "m1", Time: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC), FromName: "Alice Example", FromAddress: "alice@example.com", Subject: "Project", Body: "Project body."},
	})
	raw := string(runOutput(t, context.Background(), []string{"status", "--json", "--archive", dbPath}))
	if strings.Contains(raw, "last_sync_at") {
		t.Fatalf("status JSON kept last_sync_at:\n%s", raw)
	}
	if !strings.Contains(raw, `"freshness"`) || !strings.Contains(raw, `"last_sync"`) {
		t.Fatalf("status JSON missing freshness.last_sync:\n%s", raw)
	}
}

func TestOpenTruncatesOversizedBodyInTextAndJSON(t *testing.T) {
	installFakeGog(t)
	body := strings.Repeat("A", maxOpenBodyRunes) + "THIS_IS_ELIDED" + strings.Repeat("B", 6200)
	dbPath := seedArchive(t, []archive.Message{
		{ID: "m1", Time: time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC), FromName: "Alice Example", FromAddress: "alice@example.com", Subject: "Terms", Body: body},
	})
	// The hint sits on its own line, separated from the body by a blank
	// line, and never crammed into the body data (rules §2.4).
	hint := "\n\n… 6,214 more characters. Open the full message in Gmail.\n"

	text := string(runOutput(t, context.Background(), []string{"open", archive.RefPrefix + "m1", "--archive", dbPath}))
	if !strings.Contains(text, hint) {
		t.Fatalf("open text missing separated truncation hint:\n%s", text)
	}
	if strings.Contains(text, "THIS_IS_ELIDED") {
		t.Fatalf("open text leaked elided body:\n%s", text)
	}
	// Tripwire (TRAWL-91): human open shows the short alias, never the
	// full machine ref — full refs stay in JSON (docs/rendering.md).
	if strings.Contains(text, archive.RefPrefix) {
		t.Fatalf("open human output leaked a full machine ref:\n%s", text)
	}

	rawJSON := string(runOutput(t, context.Background(), []string{"open", archive.RefPrefix + "m1", "--json", "--archive", dbPath}))
	if !strings.Contains(rawJSON, `"body_truncated": true`) || !strings.Contains(rawJSON, `"body_elided_chars": 6214`) {
		t.Fatalf("open JSON missing explicit truncation fields:\n%s", rawJSON)
	}
	var opened archive.OpenResult
	if err := json.Unmarshal([]byte(rawJSON), &opened); err != nil {
		t.Fatalf("decode open JSON: %v\n%s", err, rawJSON)
	}
	// The JSON body is only mail content: the kept prefix, no hint text,
	// no elided remainder.
	if opened.Body != strings.Repeat("A", maxOpenBodyRunes) {
		t.Fatalf("open JSON body is not clean truncated content:\n%q", opened.Body)
	}
	if !opened.BodyTruncated || opened.BodyElidedChars != 6214 {
		t.Fatalf("open JSON truncation = %#v", opened)
	}
	if strings.Contains(opened.Body, "…") || strings.Contains(opened.Body, "Gmail") {
		t.Fatalf("open JSON leaked hint text into body:\n%q", opened.Body)
	}
}

func TestOpenRendersQuotedPrintableBodyDecoded(t *testing.T) {
	installFakeGog(t)
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	st, err := archive.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	raw := strings.Join([]string{
		"From: Alice Example <alice@example.com>",
		"To: Bob Example <bob@example.com>",
		"Subject: Quoted printable",
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Hidden=E2=80=8B mark, M=C3=BCnchen, literal equals =3D yes,",
		"web-vie=",
		"w ready.",
		"",
	}, "\r\n")
	row := `{"id":"mqp","threadId":"tqp","historyId":"hqp","internalDate":1783000000123,"labelIds":["INBOX"],"sizeEstimate":100,"raw":"` +
		base64.RawURLEncoding.EncodeToString([]byte(raw)) + "\"}\n"
	shard := archive.BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "qp-hash", Kind: archive.BackupShardMessages}
	if _, err := st.IngestBackupShard(ctx, shard, []byte(row)); err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	want := "Hidden\u200b mark, München, literal equals = yes, web-view ready."
	text := string(runOutput(t, ctx, []string{"open", archive.RefPrefix + "mqp", "--archive", dbPath}))
	if !strings.Contains(text, want) {
		t.Fatalf("open text missing decoded body:\n%s", text)
	}
	requireNoQuotedPrintableText(t, text)

	var opened archive.OpenResult
	runJSON(t, ctx, []string{"open", archive.RefPrefix + "mqp", "--json", "--archive", dbPath}, &opened)
	if opened.Body != want {
		t.Fatalf("open JSON body = %q, want %q", opened.Body, want)
	}
	requireNoQuotedPrintableText(t, opened.Body)
}

func TestOpenShortRefErrorsUseContractCodes(t *testing.T) {
	installFakeGog(t)
	dbPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	st, err := archive.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.InsertMessages(context.Background(), []archive.Message{{ID: "m1", Subject: "Project", Body: "Needle"}})
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	_, err = st.RebuildShortRefs(context.Background())
	if err != nil {
		_ = st.Close()
		t.Fatal(err)
	}
	_ = st.Close()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.ExecContext(context.Background(), `
insert into short_refs(alias, full_ref)
values ('22222', ?), ('22222', ?)
`, archive.RefPrefix+"m1", archive.RefPrefix+"missing"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ref  string
		code string
	}{
		{name: "unknown", ref: "33333", code: "unknown_short_ref"},
		{name: "ambiguous", ref: "22222", code: "ambiguous_short_ref"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			var stdout bytes.Buffer
			err := Run(context.Background(), []string{"open", tc.ref, "--json", "--archive", dbPath}, &stdout, &bytes.Buffer{})
			if err == nil {
				t.Fatal("open succeeded")
			}
			if jsonErr := json.Unmarshal(stdout.Bytes(), &out); jsonErr != nil {
				t.Fatalf("decode error JSON: %v\n%s", jsonErr, stdout.String())
			}
			if out.Error.Code != tc.code {
				t.Fatalf("code = %q, want %q; stdout=%s", out.Error.Code, tc.code, stdout.String())
			}
		})
	}
}

func TestJSONUsageErrorIsSingleRenderedDocument(t *testing.T) {
	stdout, stderr, err := runExpectError(t, []string{"search", "a", "--limit", "0", "--json"})
	if !ckoutput.IsRendered(err) {
		t.Fatalf("error was not marked rendered: %v", err)
	}
	if stderr != "" {
		t.Fatalf("JSON error wrote stderr: %s", stderr)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"error"`
	}
	assertSingleJSONDocument(t, stdout, &payload)
	if payload.Error.Code != "usage" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error payload = %#v", payload)
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
	data := runOutput(t, ctx, args)
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("decode JSON for %v: %v\n%s", args, err, string(data))
	}
}

func runOutput(t *testing.T, ctx context.Context, args []string) []byte {
	t.Helper()
	ensureTestHome(t)
	var stdout, stderr bytes.Buffer
	if err := Run(ctx, args, &stdout, &stderr); err != nil {
		t.Fatalf("Run(%v) failed: %v\nstdout=%s\nstderr=%s", args, err, stdout.String(), stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...)
}

func runExpectError(t *testing.T, args []string) (string, string, error) {
	t.Helper()
	ensureTestHome(t)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), args, &stdout, &stderr)
	if err == nil {
		t.Fatalf("Run(%v) succeeded; stdout=%s stderr=%s", args, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String(), err
}

func assertSingleJSONDocument(t *testing.T, data string, out any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(data))
	if err := dec.Decode(out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, data)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON output had trailing data: %v\n%s", err, data)
	}
}

func assertSearchShortRefParity(t *testing.T, jsonOutput []byte, humanOutput string) {
	t.Helper()
	var search archive.SearchResult
	if err := json.Unmarshal(jsonOutput, &search); err != nil {
		t.Fatalf("decode search JSON: %v\n%s", err, string(jsonOutput))
	}
	if len(search.Results) == 0 {
		t.Fatal("search JSON had no results")
	}
	for _, hit := range search.Results {
		if !validShortRef(hit.ShortRef) {
			t.Fatalf("short_ref = %q, want lowercase short-ref alphabet and length >= 5\n%s", hit.ShortRef, string(jsonOutput))
		}
		if !humanSearchHasAliasForTime(humanOutput, hit.Time, hit.ShortRef) {
			t.Fatalf("human search did not display short_ref %q for %s:\n%s", hit.ShortRef, hit.Time, humanOutput)
		}
	}
}

func validShortRef(value string) bool {
	if len(value) < 5 {
		return false
	}
	const alphabet = "23456789abcdefghjkmnpqrstuvwxyz"
	for _, ch := range value {
		if !strings.ContainsRune(alphabet, ch) {
			return false
		}
	}
	return true
}

func humanSearchHasAliasForTime(humanOutput, when, alias string) bool {
	localTime := render.ShortLocalTime(parseRFC3339(when))
	for _, line := range strings.Split(humanOutput, "\n") {
		if strings.Contains(line, localTime) && strings.Contains(line, alias) {
			return true
		}
	}
	return false
}

func seedArchive(t *testing.T, messages []archive.Message) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	st, err := archive.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.InsertMessages(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	if err := st.MarkSyncStarted(context.Background(), when); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSyncCompleted(context.Background(), when); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func seedLimitArchive(t *testing.T, count int) string {
	t.Helper()
	messages := make([]archive.Message, 0, count)
	base := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	for i := 1; i <= count; i++ {
		id := strconv.Itoa(i)
		messages = append(messages, archive.Message{
			ID:          "limit-" + id,
			ThreadID:    "thread-" + id,
			Time:        base.Add(time.Duration(i) * time.Minute),
			FromName:    "Alice Example",
			FromAddress: "alice@example.com",
			Subject:     "Needle",
			Body:        "needle limit message " + id,
		})
	}
	return seedArchive(t, messages)
}

func requireNoQuotedPrintableText(t *testing.T, value string) {
	t.Helper()
	for _, raw := range []string{"=E2=80=8B", "=C3=BC", "=3D", "web-vie="} {
		if strings.Contains(value, raw) {
			t.Fatalf("output contains quoted-printable text %q:\n%s", raw, value)
		}
	}
}

func assertLinesWithinDisplayWidth(t *testing.T, got string, width int) {
	t.Helper()
	for lineNo, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if lineWidth := render.DisplayWidth(line); lineWidth > width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", lineNo+1, lineWidth, width, got)
		}
	}
}

type fakeGogInstall struct {
	dir string
	log string
}

func installFakeGog(t *testing.T) fakeGogInstall {
	t.Helper()
	ensureTestHome(t)
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

func ensureTestHome(t *testing.T) {
	t.Helper()
	if os.Getenv("GOGCRAWL_TEST_HOME") != "" {
		return
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GOGCRAWL_TEST_HOME", home)
}

func clearLog(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestLog(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".opentrawl", "gogcrawl", "logs", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
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
