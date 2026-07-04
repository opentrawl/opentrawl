package cli

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestWhoResolverRendersTransparentTable(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "clawdex",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","doctor"],"id":"clawdex","display_name":"Contacts"}`,
			whoQuery: "dave",
			who: `{"query":"dave","candidates":[
				{"who":"Dave Archive","identifiers":["dave.archive@example.com"],"match_quality":"contains","sources":["gmail"],"messages":900,"last_seen":"2019-01-02T09:00:00Z"},
				{"who":"Dave Daily","identifiers":["+15550100001"],"match_quality":"prefix","sources":["telegram","imessage"],"messages":1200,"last_seen":"2026-06-30T20:30:00Z"},
				{"who":"Dave Exact","identifiers":["dave@example.com"],"match_quality":"exact","sources":["imessage"],"messages":12,"last_seen":"2020-03-04T08:00:00Z"}
			]}`,
		},
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			whoQuery: "dave",
			who: `{"query":"dave","candidates":[
				{"who":"Dave Daily","identifiers":["+15550100001"],"match_quality":"prefix","sources":["imessage"],"messages":300,"last_seen":"2026-06-29T20:30:00Z"}
			]}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "who", "dave")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{"who", "match", "sources", "last seen", "messages", "identifiers"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	for _, want := range []string{
		"Dave Exact",
		"exact",
		"2020-03-04",
		"12",
		"Messages",
		"dave@example.com",
		"Dave Daily",
		"prefix",
		"2026-06-30",
		"1200",
		"Messages, telegram",
		"+15550100001",
		"Dave Archive",
		"substring",
		"2019-01-02",
		"900",
		"gmail",
		"dave.archive@example.com",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestWhoRejectsLegacyResultsEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		whoQuery: "alex",
		who:      `{"query":"alex","results":[{"who":"Alex Example","messages":1}]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "who", "alex")
	if code != 1 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "imessage who failed") {
		t.Fatalf("stderr missing who failure:\n%s", stderr)
	}
}

func TestWhoIgnoresLegacyCandidateFieldAliases(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		whoQuery: "dave",
		who: `{"query":"dave","candidates":[
			{"person":"Legacy Person","volume":50,"latest":"2026-06-30T20:30:00Z"},
			{"who":"Real Dave","identifiers":["dave@example.com"],"match_quality":"exact","sources":["imessage"],"messages":5,"last_seen":"2026-06-30T20:30:00Z"}
		]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "who", "dave")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Real Dave") {
		t.Fatalf("stdout missing canonical candidate:\n%s", stdout)
	}
	if strings.Contains(stdout, "Legacy Person") {
		t.Fatalf("stdout resolved a legacy-aliased candidate:\n%s", stdout)
	}
}

func TestWhoTableFitsTerminalWidthWithManyIdentifiers(t *testing.T) {
	identifiers := make([]string, 40)
	for i := range identifiers {
		identifiers[i] = fmt.Sprintf("id%02d", i)
	}
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		whoQuery: "michael",
		who: whoCandidatesJSON(t, "michael", []WhoCandidate{{
			Who:          "Michael",
			Identifiers:  identifiers,
			MatchQuality: "exact",
			Sources:      []string{"imessage"},
			LastSeen:     "2026-07-01T08:00:00Z",
			Messages:     40,
		}}),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COLUMNS", "80")

	stdout, stderr, code := runCLI(t, "who", "michael")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if width := runewidth.StringWidth(line); width > 80 {
			t.Fatalf("line width = %d, want <= 80:\n%s\nfull output:\n%s", width, line, stdout)
		}
	}
	if !strings.Contains(stdout, "+37 more") {
		t.Fatalf("stdout missing identifier cap:\n%s", stdout)
	}
	if strings.Contains(stdout, "id39") {
		t.Fatalf("stdout leaked uncapped identifiers:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestWhoTableCapsCandidateList(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		whoQuery: "michael",
		who:      whoCandidatesJSON(t, "michael", numberedWhoCandidates("Michael", 12)),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "who", "michael")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "…and 2 more; narrow the name") {
		t.Fatalf("stdout missing candidate cap note:\n%s", stdout)
	}
	if strings.Contains(stdout, "Michael 10") || strings.Contains(stdout, "Michael 11") {
		t.Fatalf("stdout leaked candidates beyond display cap:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestWhoJSONReturnsPlainEnvelope(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "clawdex",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","doctor"],"id":"clawdex","display_name":"Contacts"}`,
		who:      `{"query":"ali","candidates":[{"who":"Alice Example","identifiers":["alice@example.com"],"match_quality":"prefix","sources":["imessage"],"messages":4,"last_seen":"2026-06-30T20:30:00Z"}]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", t.TempDir())

	stdout, stderr, code := runCLI(t, "--json", "who", "ali")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := `{"query":"ali","total_candidates":1,"candidates":[{"who":"Alice Example","identifiers":["alice@example.com"],"match_quality":"prefix","sources":["imessage"],"last_seen":"2026-06-30T20:30:00Z","messages":4}],"sources_consulted":["clawdex"]}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func whoCandidatesJSON(t *testing.T, query string, candidates []WhoCandidate) string {
	t.Helper()
	payload := struct {
		Query      string         `json:"query"`
		Candidates []WhoCandidate `json:"candidates"`
	}{
		Query:      query,
		Candidates: candidates,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func numberedWhoCandidates(namePrefix string, count int) []WhoCandidate {
	candidates := make([]WhoCandidate, 0, count)
	for i := range count {
		candidates = append(candidates, WhoCandidate{
			Who:          fmt.Sprintf("%s %02d", namePrefix, i),
			Identifiers:  []string{fmt.Sprintf("%s%02d@example.com", strings.ToLower(namePrefix), i)},
			MatchQuality: "prefix",
			Sources:      []string{"imessage"},
			LastSeen:     "2026-07-01T08:00:00Z",
			Messages:     count - i,
		})
	}
	return candidates
}
