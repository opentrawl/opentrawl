package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func searchResultsJSON(query string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"query":%q,"results":[`, query)
	for i := 1; i <= count; i++ {
		if i > 1 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"ref":"imessage:msg/%d","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"match %d"}`, i, i)
	}
	fmt.Fprintf(&b, `],"total_matches":%d,"truncated":false}`, count)
	return b.String()
}

func childStderrLines(source string, count int) string {
	var b strings.Builder
	for i := 1; i <= count; i++ {
		fmt.Fprintf(&b, "%s child-line %03d\n", source, i)
	}
	return b.String()
}

func TestSearchMergesSortsAndTruncates(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"imessage:msg/8842","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"…the boat trip is on Saturday…"},
				{"ref":"imessage:msg/8841","time":"not-a-time","who":"","where":"","snippet":"unparsed time stays visible"}
			],"total_matches":3,"truncated":true}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"telegram:msg/1930","time":"2026-05-12T10:00:00Z","who":"Bob","snippet":"…book the boat before June…"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "imessage,telegram", "--limit", "2")
	if code != 0 {
		t.Fatalf("search code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		`Search "boat trip": showing 2 of 4, newest first.`,
		"Open: trawl open REF",
		`More: trawl search "boat trip" --source imessage,telegram --limit 4`,
		"date              source    who    where   ref                text",
		shortLocalTestTime(t, "2026-05-14T09:12:00Z") + "  Messages  Alice  Family  imessage:msg/8842  …the boat trip is on Saturday…",
		shortLocalTestTime(t, "2026-05-12T10:00:00Z") + "  Telegram  Bob    -       telegram:msg/1930  …book the boat before June…",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONHonorsLimitAboveOldCap(t *testing.T) {
	limit := 205
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchLimit: strconv.Itoa(limit),
		search:      searchResultsJSON("boat trip", limit),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip", "--limit", strconv.Itoa(limit))
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope federatedSearchEnvelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	if got := len(envelope.Results); got != limit {
		t.Fatalf("results = %d, want %d", got, limit)
	}
	if envelope.TotalMatches != limit {
		t.Fatalf("total_matches = %d, want %d", envelope.TotalMatches, limit)
	}
	if envelope.Truncated {
		t.Fatalf("truncated = true, want false")
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchHumanHonorsLimitAboveOldCap(t *testing.T) {
	limit := 205
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchLimit: strconv.Itoa(limit),
		search:      searchResultsJSON("boat trip", limit),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--limit", strconv.Itoa(limit))
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got := strings.Count(stdout, "imessage:msg/"); got != limit {
		t.Fatalf("rendered rows = %d, want %d\n%s", got, limit, stdout)
	}
	if strings.Contains(stdout, "More: ") {
		t.Fatalf("stdout offered more rows when none are hidden:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchRejectsNonPositiveLimit(t *testing.T) {
	for _, args := range [][]string{
		{"search", "boat trip", "--limit", "0"},
		{"search", "boat trip", "--limit=-1"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Setenv("PATH", writeFakeCrawlers(t))
			t.Setenv("HOME", t.TempDir())

			var stdout, stderr strings.Builder
			err := Execute(args, &stdout, &stderr)
			if code := ExitCode(err); code != 2 {
				t.Fatalf("code = %d, want 2 stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			var usage usageErr
			if !errors.As(err, &usage) {
				t.Fatalf("err = %T %[1]v, want usageErr", err)
			}
			if !strings.Contains(err.Error(), "search --limit must be at least 1") {
				t.Fatalf("missing limit error: %v", err)
			}
		})
	}
}

func TestSearchHumanOutputUsesShortRefsWhenEveryDisplayedRowCanResolve(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
			],"total_matches":1,"truncated":false}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"telegram","display_name":"Telegram"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"telegram:msg/1930","alias":"q8n4c","time":"2026-05-12T10:00:00Z","who":"Bob","snippet":"Older match"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "search", "boat trip")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"t7k3f", "q8n4c"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing short ref %q:\n%s", want, stdout)
		}
	}
	for _, unwanted := range []string{"imessage:msg/8842", "telegram:msg/1930"} {
		if strings.Contains(stdout, unwanted) {
			t.Fatalf("stdout leaked full ref %q:\n%s", unwanted, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

// TestSearchHumanOutputDegradesRefsPerRow is the tripwire for the
// all-or-nothing degrade defect (TRAWL-2 mechanism note): one source
// without short refs must not drag every other row down to long
// machine refs.
func TestSearchHumanOutputDegradesRefsPerRow(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
			],"total_matches":1,"truncated":false}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			search: `{"query":"boat trip","results":[
				{"ref":"telegram:msg/1930","time":"2026-05-12T10:00:00Z","who":"Bob","snippet":"Older match"}
			],"total_matches":1,"truncated":false}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "search", "boat trip")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "t7k3f") {
		t.Fatalf("stdout dropped the short ref for the capable source:\n%s", stdout)
	}
	if !strings.Contains(stdout, "telegram:msg/1930") {
		t.Fatalf("stdout missing full ref for the source without short refs:\n%s", stdout)
	}
	if strings.Contains(stdout, "imessage:msg/8842") {
		t.Fatalf("stdout leaked the full ref for a row that has a short ref:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

// TestSearchJSONCarriesShortRefs pins short_ref emission in the
// federated envelope (TRAWL-2): agents must see the same alias humans
// copy into trawl open.
func TestSearchJSONCarriesShortRefs(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"imessage","display_name":"Messages"}`,
		search: `{"query":"boat trip","results":[
			{"ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
		],"total_matches":1,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := `{"query":"boat trip","results":[{"source":"imessage","ref":"imessage:msg/8842","short_ref":"t7k3f","time":"2026-05-14T09:12:00Z","who":"Alice","where":"","snippet":"Example match"}],"total_matches":1,"truncated":false}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONIncludesFederatedEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		searchLimit: "1",
		search: `{"query":"boat trip","results":[
			{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"Example match"},
			{"ref":"imessage:msg/2","time":"2026-05-13T09:12:00Z","who":"Bob","where":"Family","snippet":"Older match"}
		],"total_matches":2,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip", "--limit", "1")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := `{"query":"boat trip","results":[{"source":"imessage","ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"Example match"}],"total_matches":2,"truncated":true}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONIncludesSourceTruncation(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		search: `{"query":"boat trip","results":[
			{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"Example match"}
		],"total_matches":5,"truncated":true}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := `{"query":"boat trip","results":[{"source":"imessage","ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"Family","snippet":"Example match"}],"total_matches":5,"truncated":true}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchVerboseLogsSourceOutcomeAndPropagates(t *testing.T) {
	invocations := filepath.Join(t.TempDir(), "fake.log")
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "gogcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"gogcrawl","display_name":"Gmail","paths":{"default_logs":"~/.gogcrawl/logs"}}`,
		search: `{"query":"boat trip","results":[
			{"ref":"gogcrawl:mail/m1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}
		],"total_matches":1,"truncated":false}`,
	})
	home := t.TempDir()
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", home)
	t.Setenv("TRAWL_FAKE_LOG", invocations)

	stdout, stderr, code := runCLI(t, "-vv", "search", "boat trip", "--source", "gogcrawl", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"source_start: source=gogcrawl verb=search",
		"source_exec: source=gogcrawl",
		"source_done: source=gogcrawl verb=search",
		"outcome=ok",
		"results=1",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	logPath := filepath.Join(home, ".trawl", "logs", "trawl.log")
	logTextBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logTextBytes)
	if !strings.Contains(logText, "source_done: source=gogcrawl verb=search") || !strings.Contains(logText, "outcome=ok") {
		t.Fatalf("log missing source outcome:\n%s", logText)
	}
	invocationBytes, err := os.ReadFile(invocations)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(invocationBytes), "search boat trip --json --limit 1 -vv") {
		t.Fatalf("verbose flag was not propagated:\n%s", string(invocationBytes))
	}
}

func TestSearchVerbosePrefixesChildStderrLines(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:         "gogcrawl",
		metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"gogcrawl","display_name":"Gmail"}`,
		search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
		searchStderr: "first child line\nsecond child line\nfinal child line",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "-v", "search", "boat trip", "--source", "gogcrawl", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"source=gogcrawl first child line\n",
		"source=gogcrawl second child line\n",
		"source=gogcrawl final child line",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr)
		}
	}
	for _, line := range strings.Split(stderr, "\n") {
		if strings.Contains(line, "child line") && !strings.HasPrefix(line, "source=gogcrawl ") {
			t.Fatalf("child stderr line was not prefixed: %q\n%s", line, stderr)
		}
	}
}

func TestSearchDoesNotForwardChildStderrWithoutVerbose(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:         "gogcrawl",
		metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"gogcrawl","display_name":"Gmail"}`,
		search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
		searchStderr: "hidden child line\n",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "gogcrawl", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchVerbosePrefixesConcurrentChildStderrLines(t *testing.T) {
	const lineCount = 100
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:         "imsgcrawl",
			metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"imessage","display_name":"Messages"}`,
			search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
			searchStderr: childStderrLines("imessage", lineCount),
		},
		fakeCrawler{
			name:         "telecrawl",
			metadata:     `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","verbose_logs"],"id":"telegram","display_name":"Telegram"}`,
			search:       `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
			searchStderr: childStderrLines("telegram", lineCount),
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "-v", "search", "boat trip", "--source", "imessage,telegram", "--limit", "1")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	seen := map[string]int{}
	for _, line := range strings.Split(stderr, "\n") {
		if !strings.Contains(line, "child-line") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "source=imessage imessage child-line "):
			seen["imessage"]++
		case strings.HasPrefix(line, "source=telegram telegram child-line "):
			seen["telegram"]++
		default:
			t.Fatalf("child stderr line was sheared or unattributed: %q\n%s", line, stderr)
		}
		if strings.Contains(line, "source=imessage") && strings.Contains(line, "source=telegram") {
			t.Fatalf("child stderr line contains multiple sources: %q\n%s", line, stderr)
		}
	}
	for _, source := range []string{"imessage", "telegram"} {
		if seen[source] != lineCount {
			t.Fatalf("%s child stderr lines = %d, want %d\n%s", source, seen[source], lineCount, stderr)
		}
	}
}

func TestSearchJSONEmptyResultsEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		search:   `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchHelpDocumentsWho(t *testing.T) {
	stdout, stderr, code := runCLI(t, "search", "--help")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"--who=person",
		"Resolve a person or sender, then filter by the exact",
		"match",
		`trawl search invoice --who alex`,
		`trawl search --who "Vendor Support" --after 2026-01-01`,
		"Diagnostics: run with -v, or read ~/.trawl/logs/trawl.log",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONNoCrawlersEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 0 {
		t.Fatalf("search --json code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	want := `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchPartialAndTotalFailures(t *testing.T) {
	tests := []struct {
		name       string
		crawlers   []fakeCrawler
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name: "partial failure",
			crawlers: []fakeCrawler{
				{
					name:     "imsgcrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
					search:   `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
				},
				{
					name:     "telecrawl",
					metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
					search:   `{"query":"boat trip"}`,
				},
			},
			wantCode:   3,
			wantStdout: "note: 1 of 2 sources unavailable — results are partial (see stderr)",
			wantStderr: "telegram search failed: the crawler returned an error.\n  Remedy: run: trawl doctor telegram",
		},
		{
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
				search:   `not-json`,
			}},
			wantCode:   1,
			wantStderr: "telegram search failed: the crawler returned an error.\n  Remedy: run: trawl doctor telegram",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawlers...)
			t.Setenv("PATH", binDir)
			t.Setenv("HOME", t.TempDir())

			stdout, stderr, code := runCLI(t, "search", "boat trip")
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d stdout=%s stderr=%s", code, tt.wantCode, stdout, stderr)
			}
			if tt.wantStdout != "" && !strings.Contains(stdout, tt.wantStdout) {
				t.Fatalf("stdout missing %q:\n%s", tt.wantStdout, stdout)
			}
			if tt.wantStderr != "" && !strings.Contains(stderr, tt.wantStderr) {
				t.Fatalf("stderr missing %q:\n%s", tt.wantStderr, stderr)
			}
		})
	}
}

func TestSearchJSONIncludesFailedSources(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
		},
		fakeCrawler{
			name:     "telecrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			search:   `not-json`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip")
	if code != 3 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := `{"query":"boat trip","failed_sources":[{"source":"telegram","reason":"error"}],"results":[{"source":"imessage","ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"","snippet":"Example match"}],"total_matches":1,"truncated":false}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if !strings.Contains(stderr, "telegram search failed: the crawler returned an error.\n  Remedy: run: trawl doctor telegram") {
		t.Fatalf("stderr missing failure:\n%s", stderr)
	}
}

// TestSearchTimeoutIsLoudAndDistinctFromError is the regression for
// TRAWL-58: a source that blows the per-source deadline under fan-out
// (the photoscrawl-timed-out-during-federation case) must surface as a
// timeout — never a silent drop, and never conflated with a plain
// crawler error. A live subprocess is held past a short real deadline.
func TestSearchTimeoutIsLoudAndDistinctFromError(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"grill","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
		},
		fakeCrawler{
			name:        "photoscrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"photos","display_name":"Photos"}`,
			searchSleep: "3",
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLITimeout(t, 100*time.Millisecond, "search", "grill")
	if code != 3 {
		t.Fatalf("partial timeout exit = %d, want 3 stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "photos search failed: timed out after 100ms.\n  Remedy:") {
		t.Fatalf("stderr missing loud timeout line:\n%s", stderr)
	}
	if !strings.Contains(stdout, "note: 1 of 2 sources unavailable — results are partial (see stderr)") {
		t.Fatalf("stdout missing partial note:\n%s", stdout)
	}
	if !strings.Contains(stdout, "imessage:msg/1") {
		t.Fatalf("surviving source dropped from results:\n%s", stdout)
	}
}

func TestSearchTimeoutJSONCarriesReason(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"grill","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1}`,
		},
		fakeCrawler{
			name:        "photoscrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"photos","display_name":"Photos"}`,
			searchSleep: "3",
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLITimeout(t, 100*time.Millisecond, "--json", "search", "grill")
	if code != 3 {
		t.Fatalf("timeout exit = %d, want 3 stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload federatedSearchEnvelope
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("json = %s err=%v", stdout, err)
	}
	if len(payload.FailedSources) != 1 {
		t.Fatalf("failed_sources = %+v, want one entry", payload.FailedSources)
	}
	got := payload.FailedSources[0]
	if got.Source != "photos" || got.Reason != "timeout" {
		t.Fatalf("failed_sources[0] = %+v, want {photos timeout}", got)
	}
	// failed_sources must sit ahead of results, not buried at the end.
	if strings.Index(stdout, `"failed_sources"`) > strings.Index(stdout, `"results"`) {
		t.Fatalf("failed_sources buried after results:\n%s", stdout)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("surviving result dropped: %+v", payload.Results)
	}
}

// TestSearchTotalTimeoutExitsOne pins the deterministic exit contract:
// every source failing (here all time out) is exit 1, a partial failure
// is exit 3 — the same failure shape always yields the same code.
func TestSearchTotalTimeoutExitsOne(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "photoscrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"photos","display_name":"Photos"}`,
		searchSleep: "3",
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLITimeout(t, 100*time.Millisecond, "search", "grill")
	if code != 1 {
		t.Fatalf("total timeout exit = %d, want 1 stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "photos search failed: timed out after 100ms") {
		t.Fatalf("stderr missing timeout line:\n%s", stderr)
	}
}

func TestSearchUnknownSource(t *testing.T) {
	binDir := writeFakeCrawlers(t)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "missing")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, `Source "missing" was not found.`) {
		t.Fatalf("stderr missing source error:\n%s", stderr)
	}
}

// TestSearchOmitsAllEmptyColumns is the tripwire from the TRAWL-95
// adversarial review: a column with no values (tweets have no "where")
// must be omitted, never rendered as a strip of dashes.
func TestSearchOmitsAllEmptyColumns(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "birdcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","short_refs"],"id":"birdcrawl","display_name":"X"}`,
		search: `{"query":"boat","results":[
			{"ref":"birdcrawl:tweet/1","short_ref":"hmn42","time":"2026-05-14T09:12:00Z","who":"me","where":"","snippet":"tweet one"},
			{"ref":"birdcrawl:tweet/2","short_ref":"eygc4","time":"2026-05-13T09:12:00Z","who":"Anthony","where":"","snippet":"tweet two"}
		],"total_matches":2,"truncated":false}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COLUMNS", "200")

	stdout, stderr, code := runCLI(t, "search", "boat")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "where") {
		t.Fatalf("stdout rendered an all-empty where column:\n%s", stdout)
	}
	if !strings.Contains(stdout, "date              source  who      ref    text") {
		t.Fatalf("stdout missing compacted header:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}
