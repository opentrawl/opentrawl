package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--source", "imessage,telegram", "--limit", "2")
	if code != 0 {
		t.Fatalf("search code = %d stderr=%s stdout=%s", code, stderr, stdout)
	}
	for _, want := range []string{
		"SOURCE    DATE        WHO             REF                SNIPPET",
		"imessage  2026-05-14  Alice → Family  imessage:msg/8842  …the boat trip is on Saturday…",
		"telegram  2026-05-12  Bob             telegram:msg/1930  …book the boat before June…",
		"…and 2 more; narrow the query or add --after, or use --json",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
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

func TestSearchHumanOutputKeepsFullRefsForMixedShortRefCapability(t *testing.T) {
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
	for _, want := range []string{"imessage:msg/8842", "telegram:msg/1930"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing full ref %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "t7k3f") {
		t.Fatalf("stdout used short ref in mixed table:\n%s", stdout)
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
			wantStderr: "telegram search failed. Remedy: run: trawl doctor telegram",
		},
		{
			name: "all failed",
			crawlers: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
				search:   `not-json`,
			}},
			wantCode:   1,
			wantStderr: "telegram search failed. Remedy: run: trawl doctor telegram",
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
	want := `{"query":"boat trip","results":[{"source":"imessage","ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","where":"","snippet":"Example match"}],"total_matches":1,"truncated":false,"failed_sources":["telegram"]}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if !strings.Contains(stderr, "telegram search failed. Remedy: run: trawl doctor telegram") {
		t.Fatalf("stderr missing failure:\n%s", stderr)
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
