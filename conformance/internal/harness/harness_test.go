package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeCommand struct {
	stdout string
	exit   int
	mutate bool
}

type fakeCrawler struct {
	dbPath          string
	metadata        fakeCommand
	status          fakeCommand
	flagFirstStatus fakeCommand
	doctor          fakeCommand
	search          fakeCommand
	searchA         fakeCommand
	searchThe       fakeCommand
	open            fakeCommand
	openRef         string
}

func TestMetadataCheck(t *testing.T) {
	tests := []struct {
		name     string
		metadata string
		want     Status
	}{
		{
			name:     "complete metadata passes",
			metadata: `{"schema_version":1,"id":"fakecrawl","capabilities":["search"]}`,
			want:     StatusPass,
		},
		{
			name:     "missing version warns",
			metadata: `{"id":"fakecrawl","capabilities":["search"]}`,
			want:     StatusWarn,
		},
		{
			name:     "missing id fails",
			metadata: `{"schema_version":1,"capabilities":["search"]}`,
			want:     StatusFail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.metadata.stdout = tc.metadata
			result, _ := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}.CheckMetadata(context.Background())
			assertStatus(t, result, tc.want)
		})
	}
}

func TestStatusCheck(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   Status
	}{
		{
			name:   "valid status passes",
			status: `{"state":"ok","counts":[],"auth":{"authorized":true,"expires":null}}`,
			want:   StatusPass,
		},
		{
			name:   "too many counts warns",
			status: `{"state":"ok","counts":[{"id":"a","label":"a","value":1},{"id":"b","label":"b","value":1},{"id":"c","label":"c","value":1},{"id":"d","label":"d","value":1},{"id":"e","label":"e","value":1}]}`,
			want:   StatusWarn,
		},
		{
			name:   "auth token fails",
			status: `{"state":"ok","counts":[],"auth":{"authorized":true,"access_token":"sk-test-123456"}}`,
			want:   StatusFail,
		},
		{
			name:   "unknown state fails",
			status: `{"state":"ready","counts":[]}`,
			want:   StatusFail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.status.stdout = tc.status
			result, _ := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}.CheckStatus(context.Background())
			assertStatus(t, result, tc.want)
		})
	}
}

func TestDoctorCheck(t *testing.T) {
	tests := []struct {
		name   string
		doctor string
		want   Status
	}{
		{
			name:   "ok check passes",
			doctor: `{"checks":[{"id":"archive","state":"ok"}]}`,
			want:   StatusPass,
		},
		{
			name:   "non-ok check with remedy passes",
			doctor: `{"checks":[{"id":"auth","state":"fail","remedy":"refresh the fake session"}]}`,
			want:   StatusPass,
		},
		{
			name:   "non-ok check without remedy fails",
			doctor: `{"checks":[{"id":"auth","state":"fail"}]}`,
			want:   StatusFail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.doctor.stdout = tc.doctor
			result := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}.CheckDoctor(context.Background())
			assertStatus(t, result, tc.want)
		})
	}
}

func TestSecretsCheck(t *testing.T) {
	tests := []struct {
		name   string
		status string
		doctor string
		want   Status
	}{
		{
			name:   "short key value passes",
			status: `{"state":"ok","counts":[],"key":"ok"}`,
			doctor: `{"checks":[{"id":"archive","state":"ok"}]}`,
			want:   StatusPass,
		},
		{
			name:   "token field fails",
			status: `{"state":"ok","counts":[],"auth":{"authorized":true,"access_token":"sk-test-123456"}}`,
			doctor: `{"checks":[{"id":"archive","state":"ok"}]}`,
			want:   StatusFail,
		},
		{
			name:   "long hex fails",
			status: `{"state":"ok","counts":[]}`,
			doctor: `{"checks":[{"id":"archive","state":"fail","message":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","remedy":"remove fake hex"}]}`,
			want:   StatusFail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.status.stdout = tc.status
			cfg.doctor.stdout = tc.doctor
			result := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}.CheckSecrets(context.Background())
			assertStatus(t, result, tc.want)
		})
	}
}

func TestReadsNeverMutateCheck(t *testing.T) {
	tests := []struct {
		name      string
		withDB    bool
		mutate    bool
		want      Status
		wantValid bool
	}{
		{name: "stable database passes", withDB: true, want: StatusPass, wantValid: true},
		{name: "missing path warns", withDB: false, want: StatusWarn, wantValid: true},
		{name: "changed database fails", withDB: true, mutate: true, want: StatusFail, wantValid: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, tc.withDB)
			cfg.status.mutate = tc.mutate
			suite := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}
			_, status := suite.CheckStatus(context.Background())
			if status.Valid != tc.wantValid {
				t.Fatalf("status valid = %v, want %v", status.Valid, tc.wantValid)
			}
			result := suite.CheckReadsNeverMutate(context.Background(), status)
			assertStatus(t, result, tc.want)
		})
	}
}

func TestSearchCheck(t *testing.T) {
	validTime := "2026-07-02T12:00:00Z"
	searchResult := func(extraFields string) string {
		return fmt.Sprintf(`{"query":"test","results":[{"ref":"fakecrawl:item/1","time":%s%s}],"total_matches":1,"truncated":false}`, jsonString(t, validTime), extraFields)
	}
	tests := []struct {
		name               string
		capabilities       string
		search             string
		want               Status
		wantRemedy         string
		wantDetailContains string
	}{
		{
			name:         "bounded results pass",
			capabilities: `["search"]`,
			search:       searchResult(""),
			want:         StatusPass,
		},
		{
			name:         "human optional fields pass",
			capabilities: `["search"]`,
			search: searchResult(fmt.Sprintf(
				`,"who":%s,"where":%s,"snippet":%s`,
				jsonString(t, "Alex Example"),
				jsonString(t, "Example project"),
				jsonString(t, "plain test result"),
			)),
			want: StatusPass,
		},
		{
			name:         "too many results fails",
			capabilities: `["search"]`,
			search:       fmt.Sprintf(`{"query":"test","results":[{"ref":"fakecrawl:item/1","time":%s},{"ref":"fakecrawl:item/2","time":%s},{"ref":"fakecrawl:item/3","time":%s},{"ref":"fakecrawl:item/4","time":%s}],"total_matches":4,"truncated":false}`, jsonString(t, validTime), jsonString(t, validTime), jsonString(t, validTime), jsonString(t, validTime)),
			want:         StatusFail,
		},
		{
			name:         "bad time fails",
			capabilities: `["search"]`,
			search:       `{"query":"test","results":[{"ref":"fakecrawl:item/1","time":"not-a-time"}],"total_matches":1,"truncated":false}`,
			want:         StatusFail,
		},
		{
			name:         "missing ref and time fails",
			capabilities: `["search"]`,
			search:       `{"query":"test","results":[{"snippet":"synthetic result"}],"total_matches":1,"truncated":false}`,
			want:         StatusFail,
		},
		{
			name:         "missing total matches fails",
			capabilities: `["search"]`,
			search:       fmt.Sprintf(`{"query":"test","results":[{"ref":"fakecrawl:item/1","time":%s}],"truncated":false}`, jsonString(t, validTime)),
			want:         StatusFail,
		},
		{
			name:         "query echo mismatch fails",
			capabilities: `["search"]`,
			search:       fmt.Sprintf(`{"query":"test --json --limit 3","results":[{"ref":"fakecrawl:item/1","time":%s}],"total_matches":1,"truncated":false}`, jsonString(t, validTime)),
			want:         StatusFail,
		},
		{
			name:         "items only envelope fails",
			capabilities: `["search"]`,
			search:       fmt.Sprintf(`{"query":"test","items":[{"ref":"fakecrawl:item/1","time":%s}],"total_matches":1,"truncated":false}`, jsonString(t, validTime)),
			want:         StatusFail,
		},
		{
			name:         "unprefixed ref fails",
			capabilities: `["search"]`,
			search:       fmt.Sprintf(`{"query":"test","results":[{"ref":"item/1","time":%s}],"total_matches":1,"truncated":false}`, jsonString(t, validTime)),
			want:         StatusFail,
		},
		{
			name:         "numeric who fails",
			capabilities: `["search"]`,
			search:       searchResult(`,"who":"123456"`),
			want:         StatusFail,
			wantRemedy:   "map ids to display names or omit the field",
		},
		{
			name:         "numeric where fails",
			capabilities: `["search"]`,
			search:       searchResult(`,"where":"987654"`),
			want:         StatusFail,
			wantRemedy:   "map ids to display names or omit the field",
		},
		{
			name:         "unknown who placeholder fails",
			capabilities: `["search"]`,
			search:       searchResult(`,"who":"Unknown"`),
			want:         StatusFail,
			wantRemedy:   "omit instead of placeholder",
		},
		{
			name:         "n/a where placeholder fails",
			capabilities: `["search"]`,
			search:       searchResult(`,"where":"n/a"`),
			want:         StatusFail,
			wantRemedy:   "omit instead of placeholder",
		},
		{
			name:         "unknown snippet placeholder fails",
			capabilities: `["search"]`,
			search:       searchResult(`,"snippet":"unknown"`),
			want:         StatusFail,
			wantRemedy:   "omit instead of placeholder",
		},
		{
			name:         "newline where fails",
			capabilities: `["search"]`,
			search:       searchResult(fmt.Sprintf(`,"where":%s`, jsonString(t, "Example\nproject"))),
			want:         StatusFail,
			wantRemedy:   "flatten whitespace",
		},
		{
			name:         "tab snippet fails",
			capabilities: `["search"]`,
			search:       searchResult(fmt.Sprintf(`,"snippet":%s`, jsonString(t, "plain\ttest result"))),
			want:         StatusFail,
			wantRemedy:   "flatten whitespace",
		},
		{
			name:         "highlight marker fails",
			capabilities: `["search"]`,
			search:       searchResult(fmt.Sprintf(`,"snippet":%s`, jsonString(t, "plain [TEST] result"))),
			want:         StatusFail,
			wantRemedy:   "emit plain snippets without match markers",
		},
		{
			name:         "non-query brackets pass",
			capabilities: `["search"]`,
			search:       searchResult(fmt.Sprintf(`,"snippet":%s`, jsonString(t, "plain [today] test result"))),
			want:         StatusPass,
		},
		{
			name:               "phone where warns",
			capabilities:       `["search"]`,
			search:             searchResult(`,"where":"+15551234567"`),
			want:               StatusWarn,
			wantDetailContains: "contact mapping",
		},
		{
			name:               "phone who warns",
			capabilities:       `["search"]`,
			search:             searchResult(`,"who":"+15551234567"`),
			want:               StatusWarn,
			wantDetailContains: "contact mapping",
		},
		{
			name:         "undeclared search warns",
			capabilities: `["status"]`,
			search:       `{"results":[]}`,
			want:         StatusWarn,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.metadata.stdout = fmt.Sprintf(`{"schema_version":1,"id":"fakecrawl","capabilities":%s}`, tc.capabilities)
			cfg.search.stdout = tc.search
			suite := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}
			_, metadata := suite.CheckMetadata(context.Background())
			_, status := suite.CheckStatus(context.Background())
			result := suite.CheckSearch(context.Background(), metadata, status)
			assertStatus(t, result, tc.want)
			if tc.wantRemedy != "" && result.Remedy != tc.wantRemedy {
				t.Fatalf("remedy = %q, want %q; result=%#v", result.Remedy, tc.wantRemedy, result)
			}
			if tc.wantDetailContains != "" && !strings.Contains(result.Detail, tc.wantDetailContains) {
				t.Fatalf("detail = %q, want it to contain %q; result=%#v", result.Detail, tc.wantDetailContains, result)
			}
		})
	}
}

func TestOpenCheck(t *testing.T) {
	validTime := "2026-07-02T12:00:00Z"
	searchWithRef := fmt.Sprintf(`{"query":"test","results":[{"ref":"fakecrawl:item/1","time":%s}],"total_matches":1,"truncated":false}`, jsonString(t, validTime))
	tests := []struct {
		name         string
		capabilities string
		search       fakeCommand
		open         fakeCommand
		want         Status
	}{
		{
			name:         "round trip passes",
			capabilities: `["search","open"]`,
			search:       fakeCommand{stdout: searchWithRef},
			open:         fakeCommand{stdout: `{"ref":"fakecrawl:item/1","body":"synthetic detail"}`},
			want:         StatusPass,
		},
		{
			name:         "open failure fails",
			capabilities: `["search","open"]`,
			search:       fakeCommand{stdout: searchWithRef},
			open:         fakeCommand{exit: 64},
			want:         StatusFail,
		},
		{
			name:         "missing open capability fails",
			capabilities: `["search"]`,
			search:       fakeCommand{stdout: searchWithRef},
			open:         fakeCommand{stdout: `{"ref":"fakecrawl:item/1"}`},
			want:         StatusFail,
		},
		{
			name:         "no search rows warns",
			capabilities: `["search","open"]`,
			search:       fakeCommand{stdout: `{"query":"test","results":[],"total_matches":0,"truncated":false}`},
			open:         fakeCommand{stdout: `{"ref":"fakecrawl:item/1"}`},
			want:         StatusWarn,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.metadata.stdout = fmt.Sprintf(`{"schema_version":1,"id":"fakecrawl","capabilities":%s}`, tc.capabilities)
			cfg.search = tc.search
			cfg.open = tc.open
			suite := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}
			_, metadata := suite.CheckMetadata(context.Background())
			_, status := suite.CheckStatus(context.Background())
			result := suite.CheckOpen(context.Background(), metadata, status)
			assertStatus(t, result, tc.want)
		})
	}
}

func TestGrammarCheck(t *testing.T) {
	tests := []struct {
		name            string
		statusExit      int
		flagFirstStatus fakeCommand
		want            Status
	}{
		{
			name: "verb-first status passes",
			want: StatusPass,
		},
		{
			name:       "flag-first only fails",
			statusExit: 64,
			flagFirstStatus: fakeCommand{
				stdout: `{"state":"ok","counts":[]}`,
			},
			want: StatusFail,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultFakeCrawler(t, true)
			cfg.status.exit = tc.statusExit
			if tc.flagFirstStatus.stdout != "" || tc.flagFirstStatus.exit != 0 {
				cfg.flagFirstStatus = tc.flagFirstStatus
			}
			result := Suite{Runner: NewRunner(writeFakeCrawler(t, cfg))}.CheckGrammar(context.Background())
			assertStatus(t, result, tc.want)
		})
	}
}

func TestRunReportHasNoFailuresForConformingFakeCrawler(t *testing.T) {
	cfg := defaultFakeCrawler(t, true)
	report := Run(context.Background(), writeFakeCrawler(t, cfg))
	if report.HasFailures() {
		t.Fatalf("report has failures: %#v", report)
	}
	for _, name := range []string{CheckMetadata, CheckGrammar, CheckStatus, CheckDoctor, CheckSecrets, CheckReadsNeverMutate, CheckSearch, CheckOpen} {
		if resultByName(report, name).Name == "" {
			t.Fatalf("missing result %q in %#v", name, report)
		}
	}
}

func defaultFakeCrawler(t *testing.T, withDB bool) fakeCrawler {
	t.Helper()
	dbPath := ""
	if withDB {
		dbPath = filepath.Join(t.TempDir(), "archive.db")
		if err := os.WriteFile(dbPath, []byte("synthetic archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status := `{"state":"ok","counts":[],"auth":{"authorized":true,"expires":null}}`
	if dbPath != "" {
		status = fmt.Sprintf(`{"state":"ok","database_path":%s,"counts":[],"auth":{"authorized":true,"expires":null}}`, jsonString(t, dbPath))
	}
	return fakeCrawler{
		dbPath:          dbPath,
		metadata:        fakeCommand{stdout: `{"schema_version":1,"id":"fakecrawl","capabilities":["search","open"]}`},
		status:          fakeCommand{stdout: status},
		flagFirstStatus: fakeCommand{stdout: status},
		doctor:          fakeCommand{stdout: `{"checks":[{"id":"archive","state":"ok"}]}`},
		search:          fakeCommand{stdout: fmt.Sprintf(`{"query":"test","results":[{"ref":"fakecrawl:item/1","time":%s}],"total_matches":1,"truncated":false}`, jsonString(t, "2026-07-02T12:00:00Z"))},
		open:            fakeCommand{stdout: `{"ref":"fakecrawl:item/1","body":"synthetic detail"}`},
		openRef:         "fakecrawl:item/1",
	}
}

func writeFakeCrawler(t *testing.T, cfg fakeCrawler) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fakecrawl")
	script := strings.Join([]string{
		"#!/bin/sh",
		"db_path=" + shellQuote(cfg.dbPath),
		`if [ "$1" = "metadata" ] && [ "$2" = "--json" ]; then`,
		commandScript(cfg.metadata),
		"fi",
		`if [ "$1" = "status" ] && [ "$2" = "--json" ]; then`,
		commandScript(cfg.status),
		"fi",
		`if [ "$1" = "--json" ] && [ "$2" = "status" ]; then`,
		commandScript(cfg.flagFirstStatus),
		"fi",
		`if [ "$1" = "doctor" ] && [ "$2" = "--json" ]; then`,
		commandScript(cfg.doctor),
		"fi",
		`if [ "$1" = "search" ] && [ "$2" = "test" ] && [ "$3" = "--json" ] && [ "$4" = "--limit" ] && [ "$5" = "3" ]; then`,
		commandScript(cfg.search),
		"fi",
		`if [ "$1" = "search" ] && [ "$2" = "a" ] && [ "$3" = "--json" ] && [ "$4" = "--limit" ] && [ "$5" = "3" ]; then`,
		commandScript(cfg.searchA),
		"fi",
		`if [ "$1" = "search" ] && [ "$2" = "the" ] && [ "$3" = "--json" ] && [ "$4" = "--limit" ] && [ "$5" = "3" ]; then`,
		commandScript(cfg.searchThe),
		"fi",
		`if [ "$1" = "open" ] && [ "$2" = ` + shellQuote(cfg.openRef) + ` ] && [ "$3" = "--json" ]; then`,
		commandScript(cfg.open),
		"fi",
		`if [ "$1" = "status" ] && [ "$#" = "1" ]; then`,
		`printf '%s\n' "Status: ok"`,
		"exit 0",
		"fi",
		`if [ "$1" = "doctor" ] && [ "$#" = "1" ]; then`,
		`printf '%s\n' "All checks passed."`,
		"exit 0",
		"fi",
		`if [ "$1" = "search" ] && [ "$3" = "--limit" ] && [ "$4" = "1" ]; then`,
		`printf '%s\n' "No matches for \"$2\"."`,
		"exit 0",
		"fi",
		`printf '%s\n' "unsupported command" >&2`,
		"exit 64",
		"",
	}, "\n")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func commandScript(command fakeCommand) string {
	lines := []string{}
	if command.mutate {
		lines = append(lines, `printf '%s' "x" >> "$db_path"`)
	}
	if command.stdout != "" {
		lines = append(lines, "printf '%s\\n' "+shellQuote(command.stdout))
	}
	lines = append(lines, fmt.Sprintf("exit %d", command.exit))
	return strings.Join(lines, "\n")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func jsonString(t *testing.T, value string) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertStatus(t *testing.T, result CheckResult, want Status) {
	t.Helper()
	if result.Status != want {
		t.Fatalf("status = %s, want %s; result=%#v", result.Status, want, result)
	}
}

func resultByName(report Report, name string) CheckResult {
	for _, result := range report {
		if result.Name == name {
			return result
		}
	}
	return CheckResult{}
}

func TestCheckHumanReadable(t *testing.T) {
	writeScript := func(t *testing.T, statusLine string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "fakecrawl")
		script := strings.Join([]string{
			"#!/bin/sh",
			`if [ "$1" = "status" ]; then`,
			"printf '%s\\n' " + shellQuote(statusLine),
			"exit 0",
			"fi",
			`if [ "$1" = "doctor" ]; then`,
			`printf '%s\n' "All checks passed."`,
			"exit 0",
			"fi",
			`if [ "$1" = "search" ]; then`,
			`printf '%s\n' "No matches."`,
			"exit 0",
			"fi",
			"exit 64",
			"",
		}, "\n")
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	human := Suite{Runner: NewRunner(writeScript(t, "Status: ok"))}.CheckHumanReadable(context.Background())
	assertStatus(t, human, StatusPass)

	leak := Suite{Runner: NewRunner(writeScript(t, `{"state":"ok"}`))}.CheckHumanReadable(context.Background())
	assertStatus(t, leak, StatusFail)
	if !strings.Contains(leak.Detail, "printed JSON in human mode") {
		t.Fatalf("detail = %q, want JSON-leak detail", leak.Detail)
	}
}
