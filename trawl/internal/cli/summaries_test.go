package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestSummariesListUsesIndexAndMarkdownOnly(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	t.Setenv("COLUMNS", "80")
	writeDerivedFile(t, home, "INDEX.md", `---
written_by: ai
---

# Derived documents

- purchases/possessions.md - durable goods you own, model-extracted from Gmail evidence.
- purchases/possessions.tsv - the same possession rows, tab-separated.
- purchases/subscriptions.md - what you pay for: one row per real service with plan history and evidence refs.
- purchases/spending.md - one-off spend events from Gmail evidence.
`)
	writeDerivedFile(t, home, "purchases/possessions.md", summaryFixture("Possessions"))
	writeDerivedFile(t, home, "purchases/possessions.tsv", "name\tamount\n")
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))
	writeDerivedFile(t, home, "purchases/spending.md", summaryFixture("Spending"))
	writeDerivedFile(t, home, "purchases/run-log.md", summaryFixture("Run log"))

	stdout, stderr, code := runCLI(t, "summaries")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"Read: trawl summaries NAME",
		"name",
		"summary",
		"possessions",
		"durable goods you own",
		"subscriptions",
		"what you pay for",
		"spending",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"INDEX.md", "possessions.tsv", "run-log"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("stdout included %q:\n%s", unwanted, stdout)
		}
	}
	assertLinesFit(t, stdout, 80)
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesWithoutIndexReportsNoSummaries(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))

	stdout, stderr, code := runCLI(t, "summaries")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "No summaries exist yet") {
		t.Fatalf("stderr = %s", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
}

func TestSummariesJSONListIsOneDocument(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- purchases/subscriptions.md - what you pay for.\n")
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))

	stdout, stderr, code := runCLI(t, "--json", "summaries")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	doc := decodeSingleJSONDocument(t, stdout)
	summaries, ok := doc["summaries"].([]any)
	if !ok || len(summaries) != 1 {
		t.Fatalf("summaries = %#v", doc["summaries"])
	}
	item := summaries[0].(map[string]any)
	if item["name"] != "subscriptions" || item["summary"] != "what you pay for." || item["path"] != "purchases/subscriptions.md" {
		t.Fatalf("summary item = %#v", item)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesReadByNameStripsFrontmatterInHumanMode(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- purchases/subscriptions.md - what you pay for.\n")
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))

	stdout, stderr, code := runCLI(t, "summaries", "subscriptions")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "written_by") || strings.HasPrefix(stdout, "---") {
		t.Fatalf("stdout still has frontmatter:\n%s", stdout)
	}
	if !strings.HasPrefix(stdout, "# Subscriptions\n") {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesReadJSONKeepsRawContentInOneDocument(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- purchases/subscriptions.md - what you pay for.\n")
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))

	stdout, stderr, code := runCLI(t, "--json", "summaries", "subscriptions")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	doc := decodeSingleJSONDocument(t, stdout)
	if doc["name"] != "subscriptions" || doc["path"] != "purchases/subscriptions.md" {
		t.Fatalf("doc = %#v", doc)
	}
	content, _ := doc["content"].(string)
	if !strings.Contains(content, "written_by: ai") || !strings.Contains(content, "# Subscriptions") {
		t.Fatalf("content = %q", content)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesReportsAmbiguousName(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- first/subscriptions.md - one.\n- second/subscriptions.md - two.\n")
	writeDerivedFile(t, home, "first/subscriptions.md", summaryFixture("Subscriptions"))
	writeDerivedFile(t, home, "second/subscriptions.md", summaryFixture("Subscriptions"))

	stdout, stderr, code := runCLI(t, "summaries", "subscriptions")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
	for _, want := range []string{
		`Summary "subscriptions" is ambiguous`,
		"first/subscriptions.md",
		"second/subscriptions.md",
		"Remedy: run trawl summaries",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestSummariesReportsUnknownNameAsJSONError(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- purchases/subscriptions.md - what you pay for.\n")
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))

	stdout, stderr, code := runCLI(t, "--json", "summaries", "missing")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	doc := decodeSingleJSONDocument(t, stdout)
	errDoc := doc["error"].(map[string]any)
	if errDoc["code"] != "unknown_summary" || errDoc["remedy"] != "run trawl summaries" {
		t.Fatalf("error = %#v", errDoc)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesIndexedTsvNeverResolvesAsName(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- purchases/subscriptions.md - what you pay for.\n- purchases/possessions.tsv - the same rows, tab-separated.\n")
	writeDerivedFile(t, home, "purchases/subscriptions.md", summaryFixture("Subscriptions"))
	writeDerivedFile(t, home, "purchases/possessions.tsv", "name\tamount\n")

	stdout, stderr, code := runCLI(t, "summaries", "possessions")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `Summary "possessions" was not found`) {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesDanglingIndexEntryIsAContractError(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeDerivedFile(t, home, "INDEX.md", "- purchases/ghost.md - a document that does not exist.\n")

	stdout, stderr, code := runCLI(t, "--json", "summaries", "ghost")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	doc := decodeSingleJSONDocument(t, stdout)
	errDoc := doc["error"].(map[string]any)
	if errDoc["code"] != "read_summary_failed" {
		t.Fatalf("error = %#v", errDoc)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesReportsMissingDerivedRootAsJSONError(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)

	stdout, stderr, code := runCLI(t, "--json", "summaries")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	doc := decodeSingleJSONDocument(t, stdout)
	errDoc := doc["error"].(map[string]any)
	if errDoc["code"] != "no_summaries" || !strings.Contains(errDoc["message"].(string), "~/.opentrawl/trawl/derived") {
		t.Fatalf("error = %#v", errDoc)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSummariesHelpIsDiscoverable(t *testing.T) {
	stdout, stderr, code := runCLI(t, "--help")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "trawl summaries") || !strings.Contains(stdout, "precomputed answers: subscriptions, possessions, spending") {
		t.Fatalf("main help missing summaries example:\n%s", stdout)
	}
	stdout, stderr, code = runCLI(t, "summaries", "--help")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "precomputed documents over the archives") ||
		!strings.Contains(stdout, "rows carry refs you can") ||
		!strings.Contains(stdout, "open with trawl open") {
		t.Fatalf("summaries help missing explanation:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func writeDerivedFile(t *testing.T, home, rel, content string) {
	t.Helper()
	path := filepath.Join(home, ".opentrawl", "trawl", "derived", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func summaryFixture(title string) string {
	return `---
written_by: ai
---

# ` + title + `

Example body.
`
}

func decodeSingleJSONDocument(t *testing.T, text string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(text))
	var doc map[string]any
	if err := decoder.Decode(&doc); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, text)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("stdout had more than one JSON document: %v\n%s", err, text)
	}
	return doc
}

func assertLinesFit(t *testing.T, text string, width int) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if got := runewidth.StringWidth(line); got > width {
			t.Fatalf("line width = %d, want <= %d:\n%s", got, width, line)
		}
	}
}
