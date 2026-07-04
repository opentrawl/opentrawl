package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/control"
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
	defer st.Close()
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
	if strings.Contains(jsonOut.String(), shortRef) {
		t.Fatalf("search JSON leaked short ref %q:\n%s", shortRef, jsonOut.String())
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
	conformance.AssertHumanOutput(t, string(runOutput(t, context.Background(), []string{"search", "project", "--limit", "2", "--archive", dbPath})))
	conformance.AssertHumanOutput(t, string(runOutput(t, context.Background(), []string{"status", "--archive", dbPath})))
	conformance.AssertHumanOutput(t, string(runOutput(t, context.Background(), []string{"doctor", "--archive", dbPath})))
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
	var manifest metadataEnvelope
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

func TestHelpDocumentsWhoAndSearchResolution(t *testing.T) {
	installFakeGog(t)
	top := string(runOutput(t, context.Background(), []string{"--help"}))
	if !strings.Contains(top, "gogcrawl who NAME [--json]") {
		t.Fatalf("top help missing who:\n%s", top)
	}
	if !strings.Contains(top, "Diagnostics: run with -v, or read ~/.gogcrawl/logs/gogcrawl.log") {
		t.Fatalf("top help missing diagnostics:\n%s", top)
	}
	search := string(runOutput(t, context.Background(), []string{"help", "search"}))
	for _, want := range []string{
		"gogcrawl search [QUERY]",
		"QUERY is optional when --who, --after or --before is present.",
		"Resolve a name, or filter by an exact email, phone or handle.",
		"Diagnostics: run with -v, or read ~/.gogcrawl/logs/gogcrawl.log",
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
	for _, want := range []string{"who", "identifiers", "last_seen", "messages", "alice@example.com"} {
		if !strings.Contains(human, want) {
			t.Fatalf("human who missing %q:\n%s", want, human)
		}
	}
	conformance.AssertHumanOutput(t, human)
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
	if unknown["error"]["code"] != "unknown_who" || len(unknown["error"]["did_you_mean"].([]any)) != 0 || unknown["error"]["hint"] == "" {
		t.Fatalf("unknown envelope = %#v", unknown)
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
	marker := "… 6,214 more characters; the full body is in the archive"

	text := string(runOutput(t, context.Background(), []string{"open", archive.RefPrefix + "m1", "--archive", dbPath}))
	if !strings.Contains(text, marker) {
		t.Fatalf("open text missing truncation marker:\n%s", text)
	}
	if strings.Contains(text, "THIS_IS_ELIDED") {
		t.Fatalf("open text leaked elided body:\n%s", text)
	}

	rawJSON := string(runOutput(t, context.Background(), []string{"open", archive.RefPrefix + "m1", "--json", "--archive", dbPath}))
	if !strings.Contains(rawJSON, `"body_truncated": true`) {
		t.Fatalf("open JSON missing body_truncated:\n%s", rawJSON)
	}
	var opened archive.OpenResult
	if err := json.Unmarshal([]byte(rawJSON), &opened); err != nil {
		t.Fatalf("decode open JSON: %v\n%s", err, rawJSON)
	}
	if !opened.BodyTruncated || !strings.Contains(opened.Body, marker) {
		t.Fatalf("open JSON truncation = %#v", opened)
	}
	if strings.Contains(opened.Body, "THIS_IS_ELIDED") {
		t.Fatalf("open JSON leaked elided body:\n%s", opened.Body)
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
	defer db.Close()
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

func seedArchive(t *testing.T, messages []archive.Message) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "gogcrawl.db")
	st, err := archive.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
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

func requireNoQuotedPrintableText(t *testing.T, value string) {
	t.Helper()
	for _, raw := range []string{"=E2=80=8B", "=C3=BC", "=3D", "web-vie="} {
		if strings.Contains(value, raw) {
			t.Fatalf("output contains quoted-printable text %q:\n%s", raw, value)
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
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".gogcrawl", "logs", name))
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
