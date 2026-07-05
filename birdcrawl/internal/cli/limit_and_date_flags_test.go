package cli

// Regression tests for TRAWL-131: the one --limit contract (crawlkit/flags)
// on every limit-taking read verb, the fleet date grammar on --after/--before,
// version --help, and the manifest's metadata command.

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/control"
)

var limitVerbs = [][]string{
	{"search", "needle"},
	{"tweets"},
	{"bookmarks"},
	{"likes"},
	{"mentions"},
}

func runArgs(t *testing.T, dbPath string, args ...string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), append([]string{"--db", dbPath}, args...), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func TestLimitBelowOneIsUsageErrorOnEveryVerb(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	for _, verb := range limitVerbs {
		for _, bad := range []string{"0", "-3"} {
			_, _, err := runArgs(t, dbPath, append(append([]string{}, verb...), "--limit", bad)...)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("%s --limit %s: err = %v code = %d, want usage error exit 2", verb[0], bad, err, ExitCode(err))
			}
			if !strings.Contains(err.Error(), "--limit must be at least 1") {
				t.Fatalf("%s --limit %s error = %q", verb[0], bad, err.Error())
			}
		}
	}
}

func TestAllWithLimitIsUsageErrorOnEveryVerb(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	for _, verb := range limitVerbs {
		_, _, err := runArgs(t, dbPath, append(append([]string{}, verb...), "--all", "--limit", "5")...)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("%s --all --limit 5: err = %v code = %d, want usage error exit 2", verb[0], err, ExitCode(err))
		}
		if !strings.Contains(err.Error(), "use either --all or --limit") {
			t.Fatalf("%s --all --limit 5 error = %q", verb[0], err.Error())
		}
	}
}

func TestSearchAllReturnsEverythingUntruncated(t *testing.T) {
	dbPath := seedManyTweets(t, 205)
	stdout, stderr, err := runArgs(t, dbPath, "--json", "search", "needle", "--all")
	if err != nil {
		t.Fatalf("search --all error: %v stderr=%s", err, stderr)
	}
	var envelope searchEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != 205 || envelope.TotalMatches != 205 || envelope.Truncated {
		t.Fatalf("search --all = len %d total %d truncated %v, want 205/205/false", len(envelope.Results), envelope.TotalMatches, envelope.Truncated)
	}
	human, _, err := runArgs(t, dbPath, "search", "needle", "--all")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human, "More:") || strings.Contains(human, "All:") {
		t.Fatalf("search --all still advertises truncation hints:\n%s", human)
	}
}

func TestBrowseAllReturnsEverythingAndTruncationAdvertisesAll(t *testing.T) {
	dbPath, _ := seedCLITestArchive(t)
	stdout, stderr, err := runArgs(t, dbPath, "--json", "bookmarks", "--all")
	if err != nil {
		t.Fatalf("bookmarks --all error: %v stderr=%s", err, stderr)
	}
	var envelope listEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != 2 || envelope.Total != 2 || envelope.Truncated {
		t.Fatalf("bookmarks --all = len %d total %d truncated %v, want 2/2/false", len(envelope.Results), envelope.Total, envelope.Truncated)
	}
	truncated, _, err := runArgs(t, dbPath, "bookmarks", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(truncated, "All: birdcrawl bookmarks --all") {
		t.Fatalf("truncated bookmarks output missing --all hint:\n%s", truncated)
	}
	search, _, err := runArgs(t, dbPath, "search", "boat", "--limit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(search, "All: birdcrawl search \"boat\" --all") {
		t.Fatalf("truncated search output missing --all hint:\n%s", search)
	}
}

func TestDateOnlyBoundsAcceptedOnSearchAndBrowse(t *testing.T) {
	// Seeded tweets all sit at 2026-07-04 UTC; the date bounds are months
	// away on either side so local-time parsing cannot flip the result.
	dbPath, _ := seedCLITestArchive(t)
	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"search after date-only", []string{"--json", "search", "boat", "--after", "2026-05-01"}, 6},
		{"search after RFC3339", []string{"--json", "search", "boat", "--after", "2026-05-01T00:00:00Z"}, 6},
		{"search before date-only excludes", []string{"--json", "search", "boat", "--before", "2026-05-01"}, 0},
		{"search after future date-only excludes", []string{"--json", "search", "boat", "--after", "2026-09-01"}, 0},
	} {
		stdout, stderr, err := runArgs(t, dbPath, tc.args...)
		if err != nil {
			t.Fatalf("%s error: %v stderr=%s", tc.name, err, stderr)
		}
		var envelope searchEnvelope
		if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.TotalMatches != tc.want {
			t.Fatalf("%s total = %d, want %d", tc.name, envelope.TotalMatches, tc.want)
		}
	}
	stdout, stderr, err := runArgs(t, dbPath, "--json", "tweets", "--after", "2026-05-01")
	if err != nil {
		t.Fatalf("tweets --after date-only error: %v stderr=%s", err, stderr)
	}
	var envelope listEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Total != 2 {
		t.Fatalf("tweets --after date-only total = %d, want 2", envelope.Total)
	}
}

func TestBadDateIsUsageErrorWithHumanMessage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	for _, tc := range []struct {
		args []string
		flag string
	}{
		{[]string{"search", "boat", "--after", "2026-13-01"}, "--after"},
		{[]string{"tweets", "--after", "2026-13-01"}, "--after"},
		{[]string{"bookmarks", "--before", "05/01/2026"}, "--before"},
		{[]string{"likes", "--before", "yesterday"}, "--before"},
		{[]string{"mentions", "--after", "last week"}, "--after"},
	} {
		_, _, err := runArgs(t, dbPath, tc.args...)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("%v: err = %v code = %d, want usage error exit 2", tc.args, err, ExitCode(err))
		}
		if !strings.Contains(err.Error(), tc.flag+" must be RFC3339 or YYYY-MM-DD") {
			t.Fatalf("%v error = %q, want the date grammar named", tc.args, err.Error())
		}
		if strings.Contains(err.Error(), "parsing time") {
			t.Fatalf("%v error leaks the raw Go parse error: %q", tc.args, err.Error())
		}
	}
	_, _, err := runArgs(t, dbPath, "search", "boat", "--after", "")
	if err == nil || ExitCode(err) != 2 || !strings.Contains(err.Error(), "--after requires a time") {
		t.Fatalf(`search --after "": err = %v code = %d, want usage error exit 2`, err, ExitCode(err))
	}
}

func TestVersionHelpPrintsHelpNotVersion(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	stdout, _, err := runArgs(t, dbPath, "version", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "usage: birdcrawl version") {
		t.Fatalf("version --help = %q, want usage text", stdout)
	}
	stdout, _, err = runArgs(t, dbPath, "version")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout) != version {
		t.Fatalf("version = %q, want %q", stdout, version)
	}
}

func TestManifestDeclaresMetadataCommand(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	stdout, stderr, err := runArgs(t, dbPath, "--json", "metadata")
	if err != nil {
		t.Fatalf("metadata error: %v stderr=%s", err, stderr)
	}
	var manifest control.Manifest
	if err := json.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatal(err)
	}
	command, ok := manifest.Commands["metadata"]
	if !ok {
		t.Fatalf("manifest commands missing metadata: %v", manifest.Commands)
	}
	if strings.Join(command.Argv, " ") != "birdcrawl metadata --json" {
		t.Fatalf("metadata argv = %v", command.Argv)
	}
}

// TestMetadataHumanOutputHasNoCapabilityTokens pins TRAWL-125: the
// capability list exists for trawl's machine discovery, not for a human
// reader (rules.md §2.3). --json keeps it; the human card must not.
func TestMetadataHumanOutputHasNoCapabilityTokens(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "missing.db")
	stdout, stderr, err := runArgs(t, dbPath, "metadata")
	if err != nil {
		t.Fatalf("metadata error: %v stderr=%s", err, stderr)
	}
	for _, token := range []string{"capabilities", "short_refs", "verbose_logs", "contacts_export"} {
		if strings.Contains(stdout, token) {
			t.Fatalf("metadata human output still contains %q:\n%s", token, stdout)
		}
	}
}
