package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/clawdex/internal/contactexport"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/crawlkit/conformance"
)

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
	out = run("person", "add", "Ada Lovelace", "--email", "ada@example.com", "--phone", "+1 555 0100", "--tag", "math")
	if !strings.Contains(out, "Ada Lovelace") {
		t.Fatalf("add out = %s", out)
	}
	out = run("person", "list", "--plain")
	if !strings.Contains(out, "Ada Lovelace") {
		t.Fatalf("list out = %s", out)
	}
	out = run("person", "show", "ada@example.com")
	if !strings.Contains(out, "email: ada@example.com") {
		t.Fatalf("show out = %s", out)
	}
	avatarPath := filepath.Join(t.TempDir(), "avatar.png")
	writeCLITestPNG(t, avatarPath)
	out = run("person", "avatar", "set", "ada@example.com", avatarPath)
	if !strings.Contains(out, `"path": "avatars/avatar.png"`) {
		t.Fatalf("avatar set out = %s", out)
	}
	out = run("person", "avatar", "show", "ada@example.com", "--path")
	if !strings.Contains(out, "avatars/avatar.png") {
		t.Fatalf("avatar show path = %s", out)
	}
	out = run("person", "avatar", "show", "ada@example.com")
	if !strings.Contains(out, `"mime": "image/png"`) {
		t.Fatalf("avatar show = %s", out)
	}
	out = run("--dry-run", "person", "avatar", "set", "ada@example.com", avatarPath)
	if !strings.Contains(out, "would_set_avatar") {
		t.Fatalf("avatar dry set out = %s", out)
	}
	out = run("note", "add", "ada", "--kind", "dm", "--source", "manual", "--text", "Analytical engine")
	if !strings.Contains(out, "dm\tmanual") {
		t.Fatalf("note out = %s", out)
	}
	out = run("note", "list", "ada")
	if !strings.Contains(out, "Analytical engine") {
		t.Fatalf("notes out = %s", out)
	}
	out = run("timeline", "ada")
	if !strings.Contains(out, "Analytical engine") {
		t.Fatalf("timeline out = %s", out)
	}
	out = run("search", "engine", "--plain")
	if !strings.Contains(out, "note") {
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
	out = run("person", "avatar", "clear", "ada@example.com")
	if !strings.Contains(out, "Ada Lovelace") {
		t.Fatalf("avatar clear out = %s", out)
	}
	out = run("--dry-run", "person", "avatar", "clear", "ada@example.com")
	if !strings.Contains(out, "would_clear_avatar") {
		t.Fatalf("avatar dry clear out = %s", out)
	}
	var noAvatarOut, noAvatarErr bytes.Buffer
	if err := Execute([]string{"--config", cfg, "person", "avatar", "show", "ada@example.com"}, &noAvatarOut, &noAvatarErr); err == nil {
		t.Fatal("expected no avatar error")
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
	if !strings.Contains(out, "Doctor checks:") || !strings.Contains(out, "Contacts repo: ok") {
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

func TestReadCommandRebuildsStaleIndexAndLogsOnce(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "person", "add", "Ada Indexed", "--email", "ada@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}

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

	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "person", "list", "--plain"}, &out, &errOut); err != nil {
		t.Fatalf("person list: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if got := errOut.String(); got != "index rebuilt: 2 people\n" {
		t.Fatalf("stderr = %q", got)
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
		"WHO                 MATCH",
		"Alice Example",
		"close",
		"telecrawl,",
		"wacrawl",
		"15550100",
		"telegram:alice",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("who output missing %q:\n%s", want, out.String())
		}
	}
	for _, line := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if len([]rune(line)) > 72 {
			t.Fatalf("line exceeds COLUMNS=72 (%d): %q\n%s", len([]rune(line)), line, out.String())
		}
	}
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
	Who          string   `json:"who"`
	Identifiers  []string `json:"identifiers"`
	Sources      []string `json:"sources"`
	LastSeen     string   `json:"last_seen"`
	MatchQuality string   `json:"match_quality"`
	Identity     string   `json:"identity"`
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

func stringIn(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writeCLITestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
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
	if !strings.Contains(out.String(), "create\tAda Apple") {
		t.Fatalf("apple import out = %s", out.String())
	}
	fakeGog := writeFakeGog(t, `[{"resourceName":"people/g1","name":"Grace Google","email":"grace@example.com"}]`)
	t.Setenv("PATH", filepath.Dir(fakeGog)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "google", "--account", "me@example.com"}, &out, &errOut); err != nil {
		t.Fatalf("google import: %v %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "create\tGrace Google") {
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
	if !strings.Contains(out.String(), "create\tDiscord Friend") {
		t.Fatalf("discrawl import out = %s", out.String())
	}
	fakeBirdclaw := writeFakeSQLite(t, `[{"conversation_id":"1-2","profile_id":"2","handle":"bird","display_name":"Bird Person","messages":5}]`)
	t.Setenv("PATH", filepath.Dir(fakeBirdclaw)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out.Reset()
	if err := Execute([]string{"--config", cfg, "import", "birdclaw", "--db", filepath.Join(t.TempDir(), "birdclaw.sqlite"), "--min-messages", "4"}, &out, &errOut); err != nil {
		t.Fatalf("birdclaw import: %v %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "create\tBird Person") {
		t.Fatalf("birdclaw import out = %s", out.String())
	}
}

func TestExecuteGitStatusAndDryRun(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "person", "add", "Dry Run"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "would_create: Dry Run") {
		t.Fatalf("dry run = %s", out.String())
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
	if !strings.Contains(out.String(), "stage\tAda Source") {
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
	if !strings.Contains(out.String(), "stage\tAda Path") {
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
	if !strings.Contains(out.String(), "stage\tTelegram Source") || !strings.Contains(out.String(), "stage\tWhatsApp Source") {
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
	if !strings.Contains(out.String(), "stage\tWhatsApp Source") {
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
	if !strings.Contains(out.String(), "stage\tAda Source") || !strings.Contains(out.String(), "skipped 1 contacts without identifiers") {
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
	var result importContactsResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("noop import output is not an import result: %s", out.String())
	}
	if len(result.Changes) != 0 {
		t.Fatalf("noop import changes = %#v", result.Changes)
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
	manifest := `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Fake Crawler","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export","--json",";echo shell-expanded"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--json\" ] && [ \"$2\" = \"metadata\" ]; then\n" +
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
	if !strings.Contains(out.String(), "stage\tAda Argv") {
		t.Fatalf("import contacts out = %s", out.String())
	}
}

func TestExecuteImportContactsRejectsMutatingCommand(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	fake := writeFakeContactCrawlerManifest(t, "telecrawl", `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","--json","contacts","export"],"json":true,"mutates":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`, `{"contacts":[]}`)
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
			manifest: `{"schema_version":"not-crawlkit","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","--json","contacts","export"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "missing command",
			manifest: `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "not json",
			manifest: `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export"]}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "json command missing json flag",
			manifest: `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","contacts","export"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
		},
		{
			name:     "empty argv",
			manifest: `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":[],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`,
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
	fake := writeFakeContactCrawlerManifest(t, "telecrawl", `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Telegram Crawl","binary":{"name":"telecrawl"},"commands":{"contacts_export":{"argv":["telecrawl","contacts","export","--json"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`, `{"contacts":[{"display_name":"Ada Contacts","phone_numbers":["123"]}]}`)
	out.Reset()
	errOut.Reset()
	if err := Execute([]string{"--config", cfg, "--dry-run", "import", "contacts", "--from", fake}, &out, &errOut); err != nil {
		t.Fatalf("import contacts: %v stderr=%s stdout=%s", err, errOut.String(), out.String())
	}
	if !strings.Contains(out.String(), "stage\tAda Contacts") {
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
	manifest := `{"schema_version":"crawlkit.control.v1","id":"telecrawl","display_name":"Fake Crawler","binary":{"name":"telecrawl"},"commands":{"contact-export":{"argv":["telecrawl","--json","contacts","export"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"--json\" ] && [ \"$2\" = \"metadata\" ]; then\n" +
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
		Accounts:     map[string][]string{"telegram": {"ada"}},
		Handles:      map[string][]string{"github": {"ada-gh"}},
	}}})
	if len(contacts) != 1 {
		t.Fatalf("contacts = %#v", contacts)
	}
	got := contacts[0]
	if got.Source != "telecrawl" || got.Name != "Ada" || len(got.Phones) != 2 || len(got.Emails) != 1 {
		t.Fatalf("mapped contact = %#v", got)
	}
	if !got.Phones[0].Primary || got.Phones[1].Primary {
		t.Fatalf("primary phones = %#v", got.Phones)
	}
	if got.Phones[1].Value != "456" || got.Phones[1].Source != "telecrawl" {
		t.Fatalf("second phone = %#v", got.Phones[1])
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
	must("person", "add", "Ada JSON", "--email", "json@example.com")
	must("person", "add", "Empty Email")
	if got := must("--json", "person", "show", "json@example.com"); !strings.Contains(got, `"name": "Ada JSON"`) {
		t.Fatalf("json show = %s", got)
	}
	if got := must("--json", "person", "list", "--query", "Ada"); !strings.Contains(got, `"Ada JSON"`) {
		t.Fatalf("json list = %s", got)
	}
	if got := must("--plain", "person", "show", "json@example.com"); !strings.Contains(got, "Ada JSON") {
		t.Fatalf("plain show = %s", got)
	}
	if got := must("--plain", "person", "list", "--query", "NoMatch"); got != "" {
		t.Fatalf("empty list = %s", got)
	}
	if got := must("person", "list", "--query", "Empty"); !strings.Contains(got, "Empty Email") {
		t.Fatalf("no-email list = %s", got)
	}
	must("note", "add", "json@example.com", "--kind", "call", "--source", "manual", "--text", "Call body", "--occurred-at", "2026-05-08 10:00")
	if got := must("--json", "note", "list", "json@example.com"); !strings.Contains(got, `"kind": "call"`) {
		t.Fatalf("json notes = %s", got)
	}
	if got := must("export", "vcard", "--person", "json@example.com", "-o", "-"); !strings.Contains(got, "BEGIN:VCARD") {
		t.Fatalf("stdout vcard = %s", got)
	}
	input := filepath.Join(t.TempDir(), "apple.ndjson")
	if err := os.WriteFile(input, []byte("{\"identifier\":\"a1\",\"full_name\":\"Dry Apple\",\"emails\":[\"dry@example.com\"]}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := must("--dry-run", "import", "apple", "--input", input); !strings.Contains(got, "create\tDry Apple") {
		t.Fatalf("dry import = %s", got)
	}
}

func TestPrintHelpersCoverPlainJSONAndWriteErrors(t *testing.T) {
	var out bytes.Buffer
	person := model.Person{
		ID:     "person_1",
		Name:   "Print Person",
		Path:   "/tmp/person.md",
		Emails: []model.ContactValue{{Value: "print@example.com"}},
	}
	note := model.Note{
		ID:         "note_1",
		Kind:       "note",
		Source:     "manual",
		OccurredAt: time.Date(2026, 5, 8, 10, 0, 0, 0, time.UTC),
		Body:       "line one\nline two",
	}
	hit := model.SearchHit{Kind: "note", ID: "note_1", Name: "Print Person", Snippet: "line", Path: "/tmp/note.md"}

	r := &Runtime{stdout: &out, root: &CLI{}}
	if err := r.printPeople([]model.Person{person}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "print@example.com") {
		t.Fatalf("people out = %s", out.String())
	}
	out.Reset()
	if err := r.printTimeline([]model.Note{note}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\nline two") {
		t.Fatalf("timeline did not flatten body = %s", out.String())
	}
	out.Reset()
	if err := r.printHits([]model.SearchHit{hit}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "line") {
		t.Fatalf("hits out = %s", out.String())
	}
	out.Reset()
	r.root.Plain = true
	if err := r.printPeople([]model.Person{{ID: "person_2", Name: "No Email", Path: "/tmp/no.md"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.printHits([]model.SearchHit{hit}); err != nil {
		t.Fatal(err)
	}
	r.root.JSON = true
	if err := r.printTimeline([]model.Note{note}); err != nil {
		t.Fatal(err)
	}

	r.stdout = errWriter{}
	r.root.JSON = false
	if err := r.printPeople([]model.Person{person}); err == nil {
		t.Fatal("expected printPeople write error")
	}
	if err := r.printTimeline([]model.Note{note}); err == nil {
		t.Fatal("expected printTimeline write error")
	}
	if err := r.printHits([]model.SearchHit{hit}); err == nil {
		t.Fatal("expected printHits write error")
	}
	if err := r.printPerson(person); err == nil {
		t.Fatal("expected printPerson write error")
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
	for _, args := range [][]string{
		{"--config", cfg, "init", data, "--remote", remote},
		{"--config", cfg, "person", "add", "Ada Remote"},
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
	for _, args := range [][]string{
		{"--config", cfg, "init", data, "--remote", ""},
		{"--config", cfg, "person", "add", "Ada Origin"},
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

func TestExecuteEditorExportPersonAndRepair(t *testing.T) {
	cfg, data := testPaths(t)
	var out, errOut bytes.Buffer
	if err := Execute([]string{"--config", cfg, "init", data, "--remote", ""}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if err := Execute([]string{"--config", cfg, "person", "add", "Ada Edit", "--email", "edit@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(t.TempDir(), "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s' \"$1\" > \""+filepath.Join(t.TempDir(), "edited")+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", editor)
	if err := Execute([]string{"--config", cfg, "person", "edit", "edit@example.com"}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
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
		{"--config", cfg, "note", "add", "nobody", "--kind", "note", "--source", "manual"},
		{"--config", cfg, "note", "add", "nobody", "--kind", "note", "--source", "manual", "--text", "x", "--occurred-at", "bad"},
		{"--config", cfg, "export", "vcard", "-o", filepath.Join(t.TempDir(), "x.vcf")},
		{"--config", cfg, "person", "show", "missing"},
		{"--config", cfg, "person", "avatar", "clear", "missing"},
		{"--config", cfg, "--dry-run", "person", "avatar", "set", "nobody", filepath.Join(t.TempDir(), "missing.png")},
	} {
		out.Reset()
		errOut.Reset()
		if err := Execute(args, &out, &errOut); err == nil {
			t.Fatalf("expected error for %v", args)
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
		{"--config", cfg, "--repo", filepath.Join(dir, "missing"), "person", "add", "No Repo"},
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
	if !strings.Contains(out.String(), "Contacts repo: missing") || !strings.Contains(out.String(), "run clawdex init") {
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
		{"--config", cfg, "note", "list", "missing"},
		{"--config", cfg, "timeline", "missing"},
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

func fakeContactCrawlerManifest(name string) string {
	return `{"schema_version":"crawlkit.control.v1","id":"` + name + `","display_name":"Fake Crawler","binary":{"name":"` + name + `"},"commands":{"contact-export":{"argv":["` + name + `","contacts","export","--json"],"json":true}},"privacy":{"contains_private_messages":true,"exports_secrets":false}}`
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
		"if [ \"$1\" = \"--json\" ] && [ \"$2\" = \"metadata\" ]; then\n" +
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
