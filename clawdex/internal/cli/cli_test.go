package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/clawdex/internal/contactexport"
	"github.com/openclaw/clawdex/internal/index"
	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/render"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "clawdex-test-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestExecuteEndToEndLocalCommands(t *testing.T) {
	cfg, data := testPaths(t)
	run := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		full := append([]string{"--config", cfg}, args...)
		if err := Execute(full, &out, &errOut); err != nil {
			t.Fatalf("Execute(%v): %v stderr=%s stdout=%s", full, err, errOut.String(), out.String())
		}
		return out.String()
	}
	out := run("init", data, "--remote", "")
	if !strings.Contains(out, "repo_path:") {
		t.Fatalf("init out = %s", out)
	}
	seedTestPerson(t, cfg, "Ada Lovelace", []string{"ada@example.com"}, []string{"+1 555 0100"}, []string{"math"})
	out = run("person", "list")
	if !strings.Contains(out, "Ada Lovelace") {
		t.Fatalf("list out = %s", out)
	}
	out = run("person", "show", "ada@example.com")
	if !strings.Contains(out, "Email: ada@example.com") {
		t.Fatalf("show out = %s", out)
	}
	seedTestNote(t, cfg, "ada", "dm", "Analytical engine")
	out = run("search", "engine")
	if !strings.Contains(out, "Analytical engine") {
		t.Fatalf("search out = %s", out)
	}
	vcardPath := filepath.Join(t.TempDir(), "contacts.vcf")
	out = run("export", "vcard", "--all", "--include-avatars", "-o", vcardPath)
	if !strings.Contains(out, "exported: 1") {
		t.Fatalf("export out = %s", out)
	}
	if data, err := os.ReadFile(vcardPath); err != nil || !strings.Contains(string(data), "BEGIN:VCARD") {
		t.Fatalf("vcard data=%q err=%v", data, err)
	}
	out = run("sync", "apple")
	if !strings.Contains(out, "remote writes not implemented") {
		t.Fatalf("sync out = %s", out)
	}
	out = run("sync", "google", "--account", "me@example.com")
	if !strings.Contains(out, "me@example.com") {
		t.Fatalf("sync google out = %s", out)
	}
	out = run("doctor")
	conformance.AssertHumanOutput(t, out)
	if !strings.Contains(out, "Doctor checks:") || !strings.Contains(out, "contacts repo: ok") {
		t.Fatalf("doctor out = %s", out)
	}
	out = run("git", "commit", "-m", "test: contacts")
	if !strings.Contains(out, "committed: true") {
		t.Fatalf("git commit out = %s", out)
	}
	out = run("git", "commit", "-m", "test: no changes")
	if !strings.Contains(out, "committed: false") {
		t.Fatalf("git commit clean out = %s", out)
	}
}

func TestVerboseLogsWriteFileAndStreamOnRequest(t *testing.T) {
	cfg, _ := testPaths(t)
	beforeMetadataLines := countClawdexLogLines(t, "metadata start:")
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "metadata"}, &out, &errOut); err != nil {
		t.Fatalf("metadata: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("metadata without -v wrote stderr:\n%s", errOut.String())
	}
	if _, err := os.Stat(clawdexLogPath()); err != nil {
		t.Fatalf("log file missing at %s: %v", clawdexLogPath(), err)
	}
	if got := countClawdexLogLines(t, "metadata start:") - beforeMetadataLines; got != 1 {
		t.Fatalf("metadata start log lines added = %d, want 1\nlog:\n%s", got, readClawdexLog(t))
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "metadata", "-v"}, &out, &errOut); err != nil {
		t.Fatalf("metadata -v: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(errOut.String(), "metadata start:") || !strings.Contains(errOut.String(), "metadata finish: outcome=success") {
		t.Fatalf("-v stderr missing log lines:\n%s", errOut.String())
	}
	if strings.Contains(errOut.String(), "DEBUG") {
		t.Fatalf("-v streamed debug line:\n%s", errOut.String())
	}
}

func TestVerboseLogsStatusAndSyncDebugTiming(t *testing.T) {
	cfg, _ := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "metadata"}, &out, &errOut); err != nil {
		t.Fatalf("metadata: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "status", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("status json: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	var status statusOutput
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		t.Fatalf("status json = %s err=%v", out.String(), err)
	}
	if status.LastRun == nil || status.LastRun.Command != "metadata" || status.LastRun.Outcome != "success" {
		t.Fatalf("status last_run = %#v", status.LastRun)
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"-vv", "--config", cfg, "sync", "apple"}, &out, &errOut); err != nil {
		t.Fatalf("sync -vv: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	logText := readClawdexLog(t)
	for _, want := range []string{
		"sync_done: source=apple",
		"dry_run=true",
		"sync_phase: source=apple",
		"preview_ms=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("clawdex log missing %q:\n%s", want, logText)
		}
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("-vv stderr missing %q:\n%s", want, errOut.String())
		}
	}
}

func TestHelpEndsWithDiagnosticsLine(t *testing.T) {
	for _, args := range [][]string{
		{"--help"},
		{"status", "--help"},
		{"sync", "apple", "--help"},
		{"person", "list", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := Execute(args, &out, &errOut); err != nil {
				t.Fatalf("Execute(%v): %v stderr=%s stdout=%s", args, err, errOut.String(), out.String())
			}
			if errOut.Len() != 0 {
				t.Fatalf("help wrote stderr:\n%s", errOut.String())
			}
			diagnostics := diagnosticsLine()
			if !strings.Contains(out.String(), diagnostics) {
				t.Fatalf("help missing diagnostics line:\n%s", out.String())
			}
			if !strings.HasSuffix(strings.TrimSpace(out.String()), diagnostics) {
				t.Fatalf("help does not end with diagnostics line:\n%s", out.String())
			}
		})
	}
}

func TestReadCommandRebuildsStaleIndexAndLogsOnce(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	seedTestPerson(t, cfg, "Ada Indexed", []string{"ada@example.com"}, nil, nil)

	personPath := filepath.Join(data, "people", "mohamed-prefix", "person.md")
	personMarkdown := `---
id: person_mohamed_prefix
name: Mohamed Prefix
emails:
    - value: mohamed@example.com
created_at: 2026-05-08T09:00:00Z
updated_at: 2026-05-08T09:00:00Z
---
# Mohamed Prefix
`
	if err := os.MkdirAll(filepath.Dir(personPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(personPath, []byte(personMarkdown), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(data, "index", "phones.json")
	if err := os.WriteFile(legacyPath, []byte(`{"15550100":"person_old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	before := readPersonFilesForTest(t, filepath.Join(data, "people"))
	beforeLogLines := countClawdexLogLines(t, "index_rebuilt: people=2")

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "person", "list"}, &out, &errOut); err != nil {
		t.Fatalf("person list: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if got := errOut.String(); got != "" {
		t.Fatalf("stderr = %q", got)
	}
	if got := countClawdexLogLines(t, "index_rebuilt: people=2") - beforeLogLines; got != 1 {
		t.Fatalf("index_rebuilt log lines added = %d, want 1\nlog:\n%s", got, readClawdexLog(t))
	}
	if !strings.Contains(out.String(), "Mohamed Prefix") {
		t.Fatalf("person list = %s", out.String())
	}
	if _, err := os.Stat(filepath.Join(data, "index", "index.db")); err != nil {
		t.Fatalf("index.db missing: %v", err)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy json index still exists: %v", err)
	}
	after := readPersonFilesForTest(t, filepath.Join(data, "people"))
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("person markdown changed after read\nbefore=%#v\nafter=%#v", before, after)
	}

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "person", "show", "mo"}, &out, &errOut); err != nil {
		t.Fatalf("person show: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected second rebuild log: %q", errOut.String())
	}
	if got := countClawdexLogLines(t, "index_rebuilt: people=2") - beforeLogLines; got != 1 {
		t.Fatalf("index_rebuilt log lines added after second read = %d, want 1\nlog:\n%s", got, readClawdexLog(t))
	}
	if !strings.Contains(out.String(), "Mohamed Prefix") {
		t.Fatalf("person show = %s", out.String())
	}
}

func TestExecuteWhoJSONMatchesContractAndTrawlInvocation(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	writeWhoFixturePerson(t, data)

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "who", "ali", "--json"}, &out, &errOut); err != nil {
		t.Fatalf("who json: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	envelope := decodeWhoEnvelopeForTest(t, out.Bytes())
	if envelope.Query != "ali" || len(envelope.Candidates) != 1 {
		t.Fatalf("envelope = %#v", envelope)
	}
	candidate := envelope.Candidates[0]
	if candidate.Who != "Alice Example" || candidate.Identity != "Alice Example" || candidate.MatchQuality != "prefix" {
		t.Fatalf("candidate identity = %#v", candidate)
	}
	for _, want := range []string{"alice@example.com", "15550100", "telegram:alice_handle"} {
		if !stringIn(candidate.Identifiers, want) {
			t.Fatalf("identifiers missing %q: %#v", want, candidate.Identifiers)
		}
	}
	for _, identifier := range candidate.Identifiers {
		if strings.Contains(strings.ToLower(identifier), "baker") {
			t.Fatalf("address leaked into identifiers: %#v", candidate.Identifiers)
		}
	}
	if len(candidate.Addresses) != 1 || candidate.Addresses[0].Value != "221B Baker Street\nLondon" || candidate.Addresses[0].Label != "home" {
		t.Fatalf("addresses = %#v", candidate.Addresses)
	}
	if !reflect.DeepEqual(candidate.Sources, []string{"telecrawl", "wacrawl"}) {
		t.Fatalf("sources = %#v", candidate.Sources)
	}
	if candidate.LastSeen != "2026-07-02T11:00:00Z" {
		t.Fatalf("last_seen = %q", candidate.LastSeen)
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestExecuteWhoHumanUsesWidthFittedTable(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	writeWhoFixturePerson(t, data)
	t.Setenv("COLUMNS", "72")

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "who", "Alixe"}, &out, &errOut); err != nil {
		t.Fatalf("who human: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	conformance.AssertHumanOutput(t, out.String())
	for _, want := range []string{
		"who",
		"last seen",
		"identifiers",
		"Show one: trawl contacts person show NAME",
		"Alice Example",
		"wacrawl",
		"15550100",
		"telegram:alice",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("who output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "Baker Street") {
		t.Fatalf("who human output listed address:\n%s", out.String())
	}
	assertOutputDisplayWidth(t, out.String(), 72)
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestExecuteWhoHumanTruncatesWideRuneNameToDisplayWidth(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	writeWideRuneWhoFixturePerson(t, data)
	t.Setenv("COLUMNS", "72")

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "who", "hiro@example.com"}, &out, &errOut); err != nil {
		t.Fatalf("who human: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	conformance.AssertHumanOutput(t, out.String())
	if !strings.Contains(out.String(), "田中太郎") {
		t.Fatalf("who output missing wide-rune name:\n%s", out.String())
	}
	assertOutputDisplayWidth(t, out.String(), 72)
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestExecuteWhoJSONEmptyCandidatesExitZero(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	writeWhoFixturePerson(t, data)

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--json", "who", "missing"}, &out, &errOut); err != nil {
		t.Fatalf("who miss should exit zero: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	envelope := decodeWhoEnvelopeForTest(t, out.Bytes())
	if envelope.Query != "missing" || len(envelope.Candidates) != 0 || envelope.Candidates == nil {
		t.Fatalf("empty envelope = %#v", envelope)
	}
}

type whoEnvelopeForTest struct {
	Query      string                `json:"query"`
	Candidates []whoCandidateForTest `json:"candidates"`
}

type whoCandidateForTest struct {
	Who          string               `json:"who"`
	Identifiers  []string             `json:"identifiers"`
	Addresses    []model.ContactValue `json:"addresses"`
	Sources      []string             `json:"sources"`
	LastSeen     string               `json:"last_seen"`
	MatchQuality string               `json:"match_quality"`
	Identity     string               `json:"identity"`
}

func decodeWhoEnvelopeForTest(t *testing.T, data []byte) whoEnvelopeForTest {
	t.Helper()
	var envelope whoEnvelopeForTest
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("who json = %s err=%v", data, err)
	}
	if envelope.Candidates == nil {
		t.Fatalf("who json missing candidates array: %s", data)
	}
	return envelope
}

func writeWhoFixturePerson(t *testing.T, data string) {
	t.Helper()
	personPath := filepath.Join(data, "people", "alice-example", "person.md")
	if err := os.MkdirAll(filepath.Dir(personPath), 0o755); err != nil {
		t.Fatal(err)
	}
	person := `---
id: person_alice_example
name: Alice Example
tags:
  - Ally
emails:
  - value: alice@example.com
phones:
  - value: "+1 555 0100"
addresses:
  - value: |-
      221B Baker Street
      London
    label: home
    source: apple
    primary: true
accounts:
  telegram:
    - alice_handle
sources:
  telecrawl:
    names:
      - Alice Telegram
    phones:
      - "+1 555 0100"
    accounts:
      telegram:
        - alice_handle
    last_seen_at: 2026-07-02T11:00:00Z
  wacrawl:
    names:
      - Alice WhatsApp
    emails:
      - alice@example.com
    last_seen_at: 2026-07-01T11:00:00Z
created_at: 2026-07-02T09:00:00Z
updated_at: 2026-07-02T09:00:00Z
---
# Alice Example
`
	if err := os.WriteFile(personPath, []byte(person), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeWideRuneWhoFixturePerson(t *testing.T, data string) {
	t.Helper()
	personPath := filepath.Join(data, "people", "wide-rune-person", "person.md")
	if err := os.MkdirAll(filepath.Dir(personPath), 0o755); err != nil {
		t.Fatal(err)
	}
	person := `---
id: person_wide_rune
name: 田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎
emails:
  - value: hiro@example.com
created_at: 2026-07-02T09:00:00Z
updated_at: 2026-07-02T09:00:00Z
---
# 田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎
`
	if err := os.WriteFile(personPath, []byte(person), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertOutputDisplayWidth(t *testing.T, output string, width int) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if got := render.DisplayWidth(line); got > width {
			t.Fatalf("line exceeds COLUMNS=%d display width (%d): %q\n%s", width, got, line, output)
		}
	}
}

func stringIn(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func readPersonFilesForTest(t *testing.T, peopleDir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(peopleDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) != "person.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(peopleDir, path)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExecuteConfigJSONAndUsage(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "--json", "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatalf("init: %v %s", err, errOut.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", out.String(), err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "config", "set", "git.branch", "main"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "config", "set", "google.default_account", "me@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "me@example.com") {
		t.Fatalf("dry config = %s", out.String())
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "--json", "config", "show"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"branch": "main"`) {
		t.Fatalf("config = %s", out.String())
	}
	if err := Execute([]string{"--config", cfg, "config", "set", "nope", "x"}, &out, &errOut); err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected usage err, got %v", err)
	}
	if err := Execute([]string{"--bogus"}, &out, &errOut); err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected parse usage err, got %v", err)
	}
	badCfg := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(badCfg, []byte("["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"--config", badCfg, "config"}, &out, &errOut); err == nil {
		t.Fatal("expected config parse error")
	}
	if ExitCode(nil) != 0 {
		t.Fatal("nil exit code")
	}
}

func TestExecuteImportAppleFromFileAndGoogleViaFakeGog(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "apple.ndjson")
	if err := os.WriteFile(input, []byte("{\"identifier\":\"a1\",\"full_name\":\"Ada Apple\",\"emails\":[\"apple@example.com\"]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "apple", "--input", input}, &out, &errOut); err != nil {
		t.Fatalf("apple import: %v %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "Ada Apple") {
		t.Fatalf("apple import out = %s", out.String())
	}
	fakeGog := writeFakeGog(t, `[{"resourceName":"people/g1","name":"Grace Google","email":"grace@example.com"}]`)
	t.Setenv("PATH", filepath.Dir(fakeGog)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "google", "--account", "me@example.com"}, &out, &errOut); err != nil {
		t.Fatalf("google import: %v %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "Grace Google") {
		t.Fatalf("google import out = %s", out.String())
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "person", "show", "grace@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Grace Google") {
		t.Fatalf("show = %s", out.String())
	}
	fakeSQLite := writeFakeSQLite(t, `[{"channel_id":"dm1","name":"Discord Friend","messages":5,"counterpart_id":"user1"}]`)
	t.Setenv("PATH", filepath.Dir(fakeSQLite)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "discrawl", "--db", filepath.Join(t.TempDir(), "discrawl.db"), "--min-messages", "4"}, &out, &errOut); err != nil {
		t.Fatalf("discrawl import: %v %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "Discord Friend") {
		t.Fatalf("discrawl import out = %s", out.String())
	}
	fakeBirdclaw := writeFakeSQLite(t, `[{"conversation_id":"1-2","profile_id":"2","handle":"bird","display_name":"Bird Person","messages":5}]`)
	t.Setenv("PATH", filepath.Dir(fakeBirdclaw)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "birdclaw", "--db", filepath.Join(t.TempDir(), "birdclaw.sqlite"), "--min-messages", "4"}, &out, &errOut); err != nil {
		t.Fatalf("birdclaw import: %v %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "Bird Person") {
		t.Fatalf("birdclaw import out = %s", out.String())
	}
}

func TestExecuteContactsExportJSONIncludesAddresses(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "apple.ndjson")
	if err := os.WriteFile(input, []byte("{\"identifier\":\"a1\",\"full_name\":\"Ada Apple\",\"emails\":[\"apple@example.com\"],\"addresses\":[{\"value\":\"1 Main Street\\nApt 2\",\"label\":\"home\"}]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "apple", "--input", input}, &out, &errOut); err != nil {
		t.Fatalf("apple import: %v %s", err, errOut.String())
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "--json", "contacts", "export"}, &out, &errOut); err != nil {
		t.Fatalf("contacts export: %v %s", err, errOut.String())
	}
	var export contactexport.ContactExport
	if err := json.Unmarshal(out.Bytes(), &export); err != nil {
		t.Fatal(err)
	}
	if len(export.Contacts) != 1 || len(export.Contacts[0].Addresses) != 1 || export.Contacts[0].Addresses[0] != "1 Main Street\nApt 2" {
		t.Fatalf("export = %#v", export)
	}
}

func TestExecuteContactsExportHumanUsesLabelledWidthFittedTable(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(t.TempDir(), "apple.ndjson")
	contacts := strings.Join([]string{
		`{"identifier":"a1","full_name":"Alexandria Example With A Very Long Display Name For Wrapping","emails":["alexandria@example.com"],"phones":["+1 555 0100"],"addresses":[{"value":"1 Long Address Line With Enough Words To Wrap Around The Table Column\nSuite 200","label":"home"}]}`,
		`{"identifier":"b1","full_name":"Blake No Address","emails":["blake@example.com"]}`,
		`{"identifier":"c1","full_name":"田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎田中太郎","emails":["hiro@example.com"],"addresses":[{"value":"東京都千代田区丸の内一丁目東京都千代田区丸の内一丁目東京都千代田区丸の内一丁目","label":"home"}]}`,
	}, "\n") + "\n"
	if err := os.WriteFile(input, []byte(contacts), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "import", "apple", "--input", input}, &out, &errOut); err != nil {
		t.Fatalf("apple import: %v %s", err, errOut.String())
	}
	t.Setenv("COLUMNS", "72")

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "contacts", "export"}, &out, &errOut); err != nil {
		t.Fatalf("contacts export: %v %s", err, errOut.String())
	}
	conformance.AssertHumanOutput(t, out.String())
	for _, want := range []string{"who", "identifiers", "addresses", "2 identifiers", "1 address", "0 addresses", "3 contacts", "田中太郎"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("contacts export output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "\t") {
		t.Fatalf("contacts export output still uses tab-separated rows:\n%s", out.String())
	}
	assertOutputDisplayWidth(t, out.String(), 72)
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %s", errOut.String())
	}
}

func TestExecuteGitStatusAndDryRun(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "git"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No commits yet") {
		t.Fatalf("git status = %s", out.String())
	}
	for _, args := range [][]string{
		{"--config", cfg, "git", "push"},
		{"--config", cfg, "git", "pull"},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err == nil || !strings.Contains(err.Error(), "git remote is not configured") {
			t.Fatalf("%v: err=%v stdout=%s stderr=%s", args, err, out.String(), errOut.String())
		}
	}
}

func TestExecuteImportDiscrawlErrors(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fakeSQLite := filepath.Join(t.TempDir(), "sqlite3")
	if err := os.WriteFile(fakeSQLite, []byte("#!/bin/sh\necho locked >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fakeSQLite)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Execute([]string{"--config", cfg, "import", "discrawl", "--db", filepath.Join(t.TempDir(), "discrawl.db")}, &out, &errOut); err == nil {
		t.Fatal("expected discrawl import error")
	}
}

func TestExecuteImportContactsFromCrawler(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[{"display_name":"Ada Source","phone_numbers":[" +1 555 0100 "]}]}`)
	t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", "telecrawl"}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Ada Source") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestExecuteImportContactsFromCrawlerPath(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[{"display_name":"Ada Path","phone_numbers":["123"]}]}`)
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Ada Path") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestExecuteImportContactsFromAll(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	telecrawl := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[{"display_name":"Telegram Source","phone_numbers":["+1 555 0100"]}]}`)
	wacrawl := writeFakeContactCrawler(t, "wacrawl", `{"contacts":[{"display_name":"WhatsApp Source","phone_numbers":["+1 555 0101"]}]}`)
	t.Setenv("PATH", filepath.Dir(telecrawl)+string(os.PathListSeparator)+filepath.Dir(wacrawl)+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from-all"}, &out, &errOut); err != nil {
		t.Fatalf("import contacts --from-all: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Telegram Source") || !strings.Contains(out.String(), "WhatsApp Source") {
		t.Fatalf("from-all output = %s", out.String())
	}
}

func TestExecuteImportContactsFromAllReportsSuccessesAndFailures(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeFakeContactCrawlerScript(t, dir, "telecrawl", fakeContactCrawlerManifest("telecrawl"), "echo telecrawl failed >&2\nexit 9\n")
	writeFakeContactCrawlerScript(t, dir, "wacrawl", fakeContactCrawlerManifest("wacrawl"), "cat <<'JSON'\n{\"contacts\":[{\"display_name\":\"WhatsApp Source\",\"phone_numbers\":[\"+1 555 0101\"]}]}\nJSON\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/bin")
	out.Reset()
	errOut.Reset()
	err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from-all"}, &out, &errOut)
	if err == nil {
		t.Fatal("expected from-all aggregate error")
	}
	if !strings.Contains(err.Error(), "1 crawler import failed") {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(out.String(), "WhatsApp Source") {
		t.Fatalf("from-all stdout = %s", out.String())
	}
	if !strings.Contains(errOut.String(), "telecrawl failed") {
		t.Fatalf("from-all stderr = %s", errOut.String())
	}
}

func TestExecuteImportContactsReportsSkippedIdentifierlessContacts(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[{"display_name":"Ada Source","phone_numbers":["+1 555 0100"]},{"display_name":"No IDs"}]}`)
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Ada Source") || !strings.Contains(out.String(), "Skipped 1 contact with no email, phone or handle.") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestExecuteImportContactsNoopJSONHasEmptyChanges(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[{"display_name":"Ada Source","phone_numbers":["+1 555 0100"]}]}`)
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("first import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--json", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("second import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	var result importChangesEnvelope
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("noop import output is not an import result: %s", out.String())
	}
	if len(result.Changes) != 0 {
		t.Fatalf("noop import changes = %#v", result.Changes)
	}
}

func TestExecuteImportContactsAcceptsTelecrawlContractMetadata(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawlerManifest(t, "telecrawl", telecrawlContractMetadataJSON, `{"contacts":[{"display_name":"Ada Telegram","phone_numbers":["+15550100"]}]}`)
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Ada Telegram") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestExecuteImportContactsDoesNotShellExpandManifestArgv(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "telecrawl")
	manifest := `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Fake Crawler","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export","--json",";echo shell-expanded"],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"metadata\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"cat <<'JSON'\n" + manifest + "\nJSON\n" +
		"exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"contacts\" ] && [ \"$2\" = \"export\" ] && [ \"$3\" = \"--json\" ]; then\n" +
		"cat <<'JSON'\n{\"contacts\":[{\"display_name\":\"Ada Argv\",\"phone_numbers\":[\"123\"]}]}\nJSON\n" +
		"exit 0\n" +
		"fi\n" +
		"echo unexpected args: \"$@\" >&2\nexit 2\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Ada Argv") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestExecuteImportContactsRejectsMutatingCommand(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawlerManifest(t, "telecrawl", `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","--json","contacts","export"],"json":true,"mutates":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`, `{"contacts":[]}`)
	t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", "telecrawl"}, &out, &errOut); err == nil {
		t.Fatal("expected mutating command error")
	}
}

func TestExecuteImportContactsRejectsBadManifests(t *testing.T) {
	for _, tc := range []struct {
		name     string
		manifest string
	}{
		{
			name:     "wrong schema",
			manifest: `{"schema_version":1,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","--json","contacts","export"],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "wrong contract",
			manifest: `{"schema_version":2,"contract_version":2,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "missing capability",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":[],"binary":{"name":"telecrawl"},"commands":{},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "not json",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export"]}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "missing mutates",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export","--json"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "null mutates",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export","--json"],"json":true,"mutates":null}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "null json",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export","--json"],"json":null,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "json command missing json flag",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export"],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "empty argv",
			manifest: `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":[],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, data := testPaths(t)
			var out, errOut bytes.Buffer
			if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
				t.Fatal(err)
			}
			fake := writeFakeContactCrawlerManifest(t, "telecrawl", tc.manifest, `{"contacts":[]}`)
			t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
			out.Reset()
			errOut.Reset()
			if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", "telecrawl"}, &out, &errOut); err == nil {
				t.Fatal("expected bad manifest error")
			}
		})
	}
}

func TestExecuteImportContactsRejectsBadPayload(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawler(t, "telecrawl", `{"contacts":[]} private junk`)
	t.Setenv("PATH", filepath.Dir(fake)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", "telecrawl"}, &out, &errOut); err == nil {
		t.Fatal("expected bad payload error")
	}
}

func TestExecuteImportContactsAcceptsContactsExportCommandKey(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawlerManifest(t, "telecrawl", `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Telegram Crawl","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contacts_export":{"argv":["telecrawl","contacts","export","--json"],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`, `{"contacts":[{"display_name":"Ada Contacts","phone_numbers":["123"]}]}`)
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "Ada Contacts") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestReadCrawlerManifestErrors(t *testing.T) {
	dir := t.TempDir()
	failing := filepath.Join(dir, "failing")
	if err := os.WriteFile(failing, []byte("#!/bin/sh\necho metadata failed >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrawlerManifest(t.Context(), failing); err == nil {
		t.Fatal("expected metadata command error")
	}

	badJSON := filepath.Join(dir, "badjson")
	if err := os.WriteFile(badJSON, []byte("#!/bin/sh\necho not-json\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readCrawlerManifest(t.Context(), badJSON); err == nil {
		t.Fatal("expected metadata decode error")
	}
}

func TestReadCrawlerContactsReportsExportFailure(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "telecrawl")
	manifest := `{"schema_version":2,"contract_version":1,"id":"telegram","display_name":"Fake Crawler","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","--json","contacts","export"],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"metadata\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"cat <<'JSON'\n" + manifest + "\nJSON\n" +
		"exit 0\n" +
		"fi\n" +
		"echo export failed >&2\nexit 9\n"
	if err := os.WriteFile(fake, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readCrawlerContacts(t.Context(), fake); err == nil {
		t.Fatal("expected export command error")
	}
}

func TestContactExportArgv(t *testing.T) {
	got := contactExportArgv("/tmp/telecrawl")
	if !reflect.DeepEqual(got, []string{"/tmp/telecrawl", "contacts", "export", "--json"}) {
		t.Fatalf("argv = %#v", got)
	}
}

func TestSourceContactsFromExportMapsPhones(t *testing.T) {
	contacts := sourceContactsFromExport("telecrawl", contactexport.ContactExport{Contacts: []contactexport.Contact{{
		DisplayName:  "Ada",
		PhoneNumbers: []string{"123", "456"},
		Emails:       []string{"ada@example.com"},
		Addresses:    []string{"1 Main Street\nLondon"},
		Accounts:     map[string][]string{"telegram": {"ada"}},
		Handles:      map[string][]string{"github": {"ada-gh"}},
	}}})
	if len(contacts) != 1 {
		t.Fatalf("contacts = %#v", contacts)
	}
	got := contacts[0]
	if got.Source != "telecrawl" || got.Name != "Ada" || len(got.Phones) != 2 || len(got.Emails) != 1 || len(got.Addresses) != 1 {
		t.Fatalf("mapped contact = %#v", got)
	}
	if !got.Phones[0].Primary || got.Phones[1].Primary {
		t.Fatalf("primary phones = %#v", got.Phones)
	}
	if got.Phones[1].Value != "456" || got.Phones[1].Source != "telecrawl" {
		t.Fatalf("second phone = %#v", got.Phones[1])
	}
	if got.Addresses[0].Value != "1 Main Street\nLondon" || got.Addresses[0].Label != "other" || got.Addresses[0].Source != "telecrawl" || !got.Addresses[0].Primary {
		t.Fatalf("addresses = %#v", got.Addresses)
	}
	if got.Accounts["telegram"][0] != "ada" || got.Accounts["github"][0] != "ada-gh" {
		t.Fatalf("accounts = %#v", got.Accounts)
	}
}

func TestExecuteImportBirdclawErrors(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fakeSQLite := filepath.Join(t.TempDir(), "sqlite3")
	if err := os.WriteFile(fakeSQLite, []byte("#!/bin/sh\necho locked >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(fakeSQLite)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Execute([]string{"--config", cfg, "import", "birdclaw", "--db", filepath.Join(t.TempDir(), "birdclaw.sqlite")}, &out, &errOut); err == nil {
		t.Fatal("expected birdclaw import error")
	}
}

func TestExecuteJSONPlainAndStdoutBranches(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	must := func(args ...string) string {
		t.Helper()
		out.Reset()
		errOut.Reset()
		if err := Execute(append([]string{"--config", cfg}, args...), &out, &errOut); err != nil {
			t.Fatalf("%v: %v stderr=%s", args, err, errOut.String())
		}
		return out.String()
	}
	must("init", data, "--remote", "")
	seedTestPerson(t, cfg, "Ada JSON", []string{"json@example.com"}, nil, nil)
	seedTestPerson(t, cfg, "Empty Email", nil, nil, nil)
	if got := must("--json", "person", "show", "json@example.com"); !strings.Contains(got, `"name": "Ada JSON"`) {
		t.Fatalf("json show = %s", got)
	}
	if got := must("--json", "person", "list", "--query", "Ada"); !strings.Contains(got, `"Ada JSON"`) {
		t.Fatalf("json list = %s", got)
	}
	if got := must("person", "show", "json@example.com"); !strings.Contains(got, "Ada JSON") {
		t.Fatalf("human show = %s", got)
	}
	if got := must("person", "list", "--query", "NoMatch"); !strings.Contains(got, `No people match "NoMatch".`) {
		t.Fatalf("empty list = %s", got)
	}
	if got := must("person", "list", "--query", "Empty"); !strings.Contains(got, "Empty Email") {
		t.Fatalf("no-email list = %s", got)
	}
	if got := must("export", "vcard", "--person", "json@example.com", "-o", "-"); !strings.Contains(got, "BEGIN:VCARD") {
		t.Fatalf("stdout vcard = %s", got)
	}
	input := filepath.Join(t.TempDir(), "apple.ndjson")
	if err := os.WriteFile(input, []byte("{\"identifier\":\"a1\",\"full_name\":\"Dry Apple\",\"emails\":[\"dry@example.com\"]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := must("--dry-run", "import", "apple", "--input", input); !strings.Contains(got, "Dry Apple") {
		t.Fatalf("dry import = %s", got)
	}
}

func TestPrintRenderersAndWriteErrors(t *testing.T) {
	var out bytes.Buffer
	person := model.Person{
		ID:     "person_1",
		Name:   "Print Person",
		Path:   "/tmp/person.md",
		Emails: []model.ContactValue{{Value: "print@example.com"}},
	}
	hit := model.SearchHit{Kind: "note", ID: "note_1", Name: "Print Person", Snippet: "line", Path: "/tmp/note.md"}
	people := peopleEnvelope{People: []model.Person{person}, Total: 1}
	search := searchEnvelope{Query: "line", Results: []model.SearchHit{hit}, TotalMatches: 1}

	r := &Runtime{stdout: &out, root: &CLI{}}
	if err := r.print(people); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "print@example.com") {
		t.Fatalf("people out = %s", out.String())
	}
	out.Reset()
	if err := r.print(search); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "line") || !strings.Contains(out.String(), `Search "line"`) {
		t.Fatalf("search out = %s", out.String())
	}
	out.Reset()
	if err := r.print(person); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Print Person") {
		t.Fatalf("person out = %s", out.String())
	}

	if err := r.print(struct{ Unrenderable bool }{}); err == nil || !strings.Contains(err.Error(), "no human renderer") {
		t.Fatalf("expected no-human-renderer error, got %v", err)
	}

	r.stdout = errWriter{}
	if err := r.print(people); err == nil {
		t.Fatal("expected people write error")
	}
	if err := r.print(search); err == nil {
		t.Fatal("expected search write error")
	}
	if err := r.print(person); err == nil {
		t.Fatal("expected person write error")
	}
}

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestExecuteGitPushPullWithLocalRemote(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	data := filepath.Join(dir, "contacts")
	remote := filepath.Join(dir, "remote.git")
	if err := os.Mkdir(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	runShell(t, remote, "git", "init", "--bare")
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", remote}, &out, &errOut); err != nil {
		t.Fatalf("init: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	seedTestPerson(t, cfg, "Ada Remote", nil, nil, nil)
	for _, args := range [][]string{
		{"--config", cfg, "git", "commit", "-m", "test: remote"},
		{"--config", cfg, "git", "push"},
		{"--config", cfg, "git", "pull"},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v stderr=%s stdout=%s", args, err, errOut.String(), out.String())
		}
	}
}

func TestExecuteGitPushPullWithExistingOrigin(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	data := filepath.Join(dir, "contacts")
	remote := filepath.Join(dir, "remote.git")
	if err := os.Mkdir(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	runShell(t, remote, "git", "init", "--bare")
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatalf("init: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	seedTestPerson(t, cfg, "Ada Origin", nil, nil, nil)
	for _, args := range [][]string{
		{"--config", cfg, "git", "commit", "-m", "test: origin"},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v stderr=%s stdout=%s", args, err, errOut.String(), out.String())
		}
	}
	runShell(t, data, "git", "remote", "add", "origin", remote)
	for _, args := range [][]string{
		{"--config", cfg, "git", "push"},
		{"--config", cfg, "git", "pull"},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err != nil {
			t.Fatalf("%v: %v stderr=%s stdout=%s", args, err, errOut.String(), out.String())
		}
	}
}

func TestExecuteExportPersonAndRepair(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	seedTestPerson(t, cfg, "Ada Edit", []string{"edit@example.com"}, nil, nil)
	vcardPath := filepath.Join(t.TempDir(), "one.vcf")
	if err := Execute([]string{"--config", cfg, "export", "vcard", "--person", "edit@example.com", "-o", vcardPath}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	personPath := filepath.Join(data, "people", "ada-edit", "person.md")
	if err := os.WriteFile(personPath, []byte("---\nid: person_x\nname: Ada Edit\ntags: [broken\n---\n# Ada Edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "doctor", "--repair"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	conformance.AssertHumanOutput(t, out.String())
	if !strings.Contains(out.String(), "Markdown repair: ok - 1 person markdown file would be repaired") {
		t.Fatalf("repair dry-run = %s", out.String())
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "doctor", "--repair"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	conformance.AssertHumanOutput(t, out.String())
	if !strings.Contains(out.String(), "Markdown repair: ok - 1 person markdown file repaired") {
		t.Fatalf("repair = %s", out.String())
	}
	if err := os.WriteFile(personPath, []byte("---\nid: person_x\nname: Ada Edit\navatar:\n  path: avatars/missing.png\n---\n# Ada Edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "doctor", "--repair"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	conformance.AssertHumanOutput(t, out.String())
	if !strings.Contains(out.String(), "Avatar metadata: ok - 1 avatar metadata entry would be repaired") {
		t.Fatalf("avatar repair dry-run = %s", out.String())
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "doctor", "--repair"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	conformance.AssertHumanOutput(t, out.String())
	if !strings.Contains(out.String(), "Avatar metadata: ok - 1 avatar metadata entry repaired") {
		t.Fatalf("avatar repair = %s", out.String())
	}
}

func TestExecuteUsageGuards(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--config", cfg, "export", "vcard", "-o", filepath.Join(t.TempDir(), "x.vcf")},
		{"--config", cfg, "person", "show", "missing"},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
}

// Runtime errors route through the one crawlkit output envelope in --json
// mode (TRAWL-83): the machine error doc lands on stdout, nothing on stderr,
// and the exit code is preserved.
func TestExecuteJSONErrorEnvelope(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}

	decode := func(t *testing.T, b []byte) struct {
		Error struct{ Code, Message, Remedy string } `json:"error"`
	} {
		t.Helper()
		var env struct {
			Error struct{ Code, Message, Remedy string } `json:"error"`
		}
		if err := json.Unmarshal(b, &env); err != nil {
			t.Fatalf("decode envelope: %v (%s)", err, b)
		}
		return env
	}

	// Runtime usage error: code "usage", remedy carries the next step, exit 2.
	out.Reset()
	errOut.Reset()
	err := Execute([]string{"--config", cfg, "--json", "who", ""}, &out, &errOut)
	if ExitCode(err) != 2 {
		t.Fatalf("who empty exit = %d (%v)", ExitCode(err), err)
	}
	if errOut.Len() != 0 {
		t.Fatalf("rendered error should not touch stderr: %s", errOut.String())
	}
	env := decode(t, out.Bytes())
	if env.Error.Code != "usage" || env.Error.Remedy != "Run 'trawl contacts --help'." || env.Error.Message == "" {
		t.Fatalf("usage envelope = %#v", env.Error)
	}

	// Generic runtime error: default "command_failed", human-clear message, exit 1.
	out.Reset()
	errOut.Reset()
	err = Execute([]string{"--config", cfg, "--json", "person", "show", "nobody"}, &out, &errOut)
	if ExitCode(err) != 1 {
		t.Fatalf("person show missing exit = %d (%v)", ExitCode(err), err)
	}
	env = decode(t, out.Bytes())
	if env.Error.Code != "command_failed" || !strings.Contains(env.Error.Message, "no person matched") {
		t.Fatalf("command_failed envelope = %#v", env.Error)
	}

	// Kong parse errors print before Run with no framework seam, so they stay
	// plain text on stderr even under --json (documented limitation): no JSON
	// envelope on stdout.
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--json", "bogus"}, &out, &errOut); ExitCode(err) != 2 {
		t.Fatalf("bogus exit = %d (%v)", ExitCode(err), err)
	}
	if out.Len() != 0 {
		t.Fatalf("kong parse error must not emit a JSON envelope: %s", out.String())
	}
}

// The manual contact-management verbs were deleted under TRAWL-118 (Q4).
// They must fail as standard unknown commands (usage error, exit 2).
func TestDeletedManualVerbsAreUnknown(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"person", "add", "Ada"},
		{"person", "edit", "ada"},
		{"person", "avatar", "show", "ada"},
		{"note", "add", "ada", "--kind", "dm", "--source", "manual", "--text", "x"},
		{"note", "list", "ada"},
		{"timeline", "ada"},
	} {
		out.Reset()
		errOut.Reset()
		err := Execute(append([]string{"--config", cfg}, args...), &out, &errOut)
		if err == nil {
			t.Fatalf("deleted verb %v succeeded: stdout=%s", args, out.String())
		}
		if ExitCode(err) != 2 {
			t.Fatalf("deleted verb %v exit = %d (err=%v), want 2", args, ExitCode(err), err)
		}
	}
}

func TestSmallCLIHelpers(t *testing.T) {
	if got := firstNonEmpty("", "  ", "x"); got != "x" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Fatalf("firstNonEmpty empty = %q", got)
	}
}

func TestExecuteErrorBranchesAndNoConfigInit(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.toml")
	data := filepath.Join(dir, "contacts")
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", "", "--no-config"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatalf("config unexpectedly exists: %v", err)
	}
	for _, args := range [][]string{
		{"--config", cfg, "--repo", filepath.Join(dir, "missing"), "person", "list"},
		{"--config", cfg, "--repo", filepath.Join(dir, "missing"), "export", "vcard", "--all", "-o", "-"},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--repo", filepath.Join(dir, "missing"), "doctor"}, &out, &errOut); err != nil {
		t.Fatalf("doctor should diagnose a missing repo: %v stdout=%s stderr=%s", err, out.String(), errOut.String())
	}
	conformance.AssertHumanOutput(t, out.String())
	if !strings.Contains(out.String(), "contacts repo: missing") || !strings.Contains(out.String(), "run trawl contacts init") {
		t.Fatalf("doctor missing repo out = %s", out.String())
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"--config", cfg, "config", "set", "repo_path", data}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"--config", cfg, "config", "set", "git.remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--config", cfg, "search", ""},
		{"--config", cfg, "export", "vcard", "--person", "missing", "-o", "-"},
		{"--config", cfg, "export", "vcard", "--all", "-o", filepath.Join(dir, "nope", "x.vcf")},
		{"--config", cfg, "import", "apple", "--input", filepath.Join(dir, "missing.ndjson")},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err == nil {
			t.Fatalf("expected error for %v", args)
		}
	}
	fakeGog := writeFakeGogExit(t)
	t.Setenv("PATH", filepath.Dir(fakeGog)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := Execute([]string{"--config", cfg, "import", "google"}, &out, &errOut); err == nil {
		t.Fatal("expected fake gog failure")
	}
}

func testPaths(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "config.toml"), filepath.Join(dir, "contacts")
}

// The manual person/note verbs are gone (TRAWL-118); tests seed contact data
// through the store, the same layer imports write through.
func seedTestPerson(t *testing.T, cfgPath, name string, emails, phones, tags []string) model.Person {
	t.Helper()
	p, err := testStore(t, cfgPath).AddPerson(name, emails, phones, tags, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func seedTestNote(t *testing.T, cfgPath, personQuery, kind, text string) {
	t.Helper()
	note := markdown.NewNote("", kind, "manual", text, time.Time{}, time.Now(), nil)
	if _, err := testStore(t, cfgPath).AddNote(personQuery, note); err != nil {
		t.Fatal(err)
	}
}

func testStore(t *testing.T, cfgPath string) index.Store {
	t.Helper()
	cfg, err := repo.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return index.New(repo.Open(cfg.RepoPath, cfg))
}

func clawdexLogPath() string {
	return filepath.Join(os.Getenv("HOME"), ".opentrawl", "contacts", "logs", "contacts.log")
}

func readClawdexLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(clawdexLogPath())
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func countClawdexLogLines(t *testing.T, containsText string) int {
	t.Helper()
	count := 0
	for _, line := range strings.Split(readClawdexLog(t), "\n") {
		if strings.Contains(line, containsText) {
			count++
		}
	}
	return count
}

func writeFakeGog(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	name := "gog"
	if runtime.GOOS == "windows" {
		name = "gog.bat"
	}
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\nprintf '%s\\n' '" + output + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeGogExit(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gog")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho bad >&2\nexit 4\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeSQLite(t *testing.T, output string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "sqlite3")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat <<'JSON'\n"+output+"\nJSON\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFakeContactCrawler(t *testing.T, name, contacts string) string {
	t.Helper()
	return writeFakeContactCrawlerManifest(t, name, fakeContactCrawlerManifest(name), contacts)
}

const telecrawlContractMetadataJSON = `{
	  "schema_version": 2,
	  "contract_version": 1,
	  "id": "telegram",
	  "display_name": "Telegram",
	  "version": "0.3.1",
  "capabilities": [
    "metadata",
    "doctor",
    "status",
    "sync",
    "search",
    "open",
    "who",
    "short_refs",
    "contacts_export",
	    "backup"
	  ],
	  "commands": {
	    "contact-export": {
	      "argv": ["telecrawl", "contacts", "export", "--json"],
	      "json": true,
	      "mutates": false
	    }
	  },
	  "privacy": {"contains_private_messages": true, "exports_secrets": false}
	}`

func fakeContactCrawlerManifest(name string) string {
	return `{"schema_version":2,"contract_version":1,"id":"` + name + `","display_name":"Fake Crawler","version":"0.0.0","capabilities":["contacts_export"],"binary":{"name":"` + name + `"},"commands":{"contact-export":{"argv":["` + name + `","contacts","export","--json"],"json":true,"mutates":false}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`
}

func writeFakeContactCrawlerManifest(t *testing.T, name, manifest, contacts string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	writeFakeContactCrawlerScript(t, dir, name, manifest, "cat <<'JSON'\n"+contacts+"\nJSON\n")
	return path
}

func writeFakeContactCrawlerScript(t *testing.T, dir, name, manifest, exportScript string) {
	t.Helper()
	path := filepath.Join(dir, name)
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"metadata\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"cat <<'JSON'\n" + manifest + "\nJSON\n" +
		"exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = \"contacts\" ] && [ \"$2\" = \"export\" ] && [ \"$3\" = \"--json\" ]; then\n" +
		exportScript +
		"exit $?\n" +
		"fi\n" +
		"echo unexpected args: \"$@\" >&2\nexit 2\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func runShell(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

// TRAWL-94 regression: list verbs speak human on zero results, JSON arrays
// are never null, --limit is honored as given, and metadata renders human
// text without --json.
func TestListVerbsEmptyStatesLimitsAndJSONArrays(t *testing.T) {
	cfg, data := testPaths(t)
	run := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		full := append([]string{"--config", cfg}, args...)
		if err := Execute(full, &out, &errOut); err != nil {
			t.Fatalf("Execute(%v): %v stderr=%s stdout=%s", full, err, errOut.String(), out.String())
		}
		return out.String()
	}
	run("init", data, "--remote", "")
	seedTestPerson(t, cfg, "Ada Lovelace", []string{"ada@example.com"}, nil, nil)
	seedTestPerson(t, cfg, "Grace Hopper", []string{"grace@example.com"}, nil, nil)
	seedTestNote(t, cfg, "ada", "dm", "Analytical engine")

	// Empty states: a human sentence, never silence.
	for _, probe := range []struct {
		args []string
		want string
	}{
		{[]string{"search", "zz-no-match"}, `No matches for "zz-no-match".`},
		{[]string{"person", "list", "--query", "zz-no-match"}, `No people match "zz-no-match".`},
	} {
		out := run(probe.args...)
		conformance.AssertHumanOutput(t, out)
		if !strings.Contains(out, probe.want) {
			t.Fatalf("%v output missing %q:\n%s", probe.args, probe.want, out)
		}
	}

	// JSON zero results: arrays present, never null.
	for _, probe := range []struct {
		args  []string
		array string
	}{
		{[]string{"--json", "search", "zz-no-match"}, `"results": []`},
		{[]string{"--json", "person", "list", "--query", "zz-no-match"}, `"people": []`},
	} {
		out := run(probe.args...)
		if !strings.Contains(out, probe.array) {
			t.Fatalf("%v output missing %q:\n%s", probe.args, probe.array, out)
		}
		if strings.Contains(out, ": null") {
			t.Fatalf("%v output has null array:\n%s", probe.args, out)
		}
	}

	// --limit honored as given, truncation declared.
	var people peopleEnvelope
	if err := json.Unmarshal([]byte(run("--json", "person", "list", "--limit", "1")), &people); err != nil {
		t.Fatal(err)
	}
	if len(people.People) != 1 || people.Total != 2 || !people.Truncated {
		t.Fatalf("person list --limit 1 envelope = %#v", people)
	}
	out := run("person", "list", "--limit", "1")
	if !strings.Contains(out, "showing 1 of 2") ||
		!strings.Contains(out, "More: trawl contacts person list --limit 2") ||
		!strings.Contains(out, "All: trawl contacts person list --all") {
		t.Fatalf("person list --limit 1 human = %s", out)
	}

	// --all returns everything, no hidden cap and nothing truncated.
	var everyone peopleEnvelope
	if err := json.Unmarshal([]byte(run("--json", "person", "list", "--all")), &everyone); err != nil {
		t.Fatal(err)
	}
	if len(everyone.People) != 2 || everyone.Total != 2 || everyone.Truncated {
		t.Fatalf("person list --all envelope = %#v", everyone)
	}
	var search searchEnvelope
	if err := json.Unmarshal([]byte(run("--json", "search", "example.com", "--limit", "1")), &search); err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.TotalMatches < 2 || !search.Truncated {
		t.Fatalf("search --limit 1 envelope = %#v", search)
	}

	// Limit below 1 is a usage error, and a usage error is user feedback:
	// the log run finishes as rejected, never as a recorded crawler error.
	rejectedBefore := countClawdexLogLines(t, "outcome=rejected")
	errorsBefore := countClawdexLogLines(t, "search_failed")
	var out2, errOut2 bytes.Buffer
	err := Execute([]string{"--config", cfg, "search", "ada", "--limit", "0"}, &out2, &errOut2)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("search --limit 0 = %v", err)
	}
	if got := countClawdexLogLines(t, "outcome=rejected") - rejectedBefore; got != 1 {
		t.Fatalf("rejected log lines added = %d, want 1\nlog:\n%s", got, readClawdexLog(t))
	}
	if got := countClawdexLogLines(t, "search_failed") - errorsBefore; got != 0 {
		t.Fatalf("usage error was logged as crawler error:\n%s", readClawdexLog(t))
	}

	// metadata speaks human without --json.
	metadata := run("metadata")
	conformance.AssertHumanOutput(t, metadata)
	// Capabilities are machine vocabulary for trawl's discovery probe,
	// not something a human reader can use (rules.md §2.3, TRAWL-125).
	if strings.Contains(metadata, "capabilities") {
		t.Fatalf("metadata human output still prints the capability list:\n%s", metadata)
	}
}
