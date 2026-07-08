package cli

import (
	"strings"
	"testing"
)

func TestSearchWhoPassesThroughToEveryCapableCrawler(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:        "imsgcrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			searchQuery: "boat trip",
			searchWho:   "Alice Example",
			search:      `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
			whoQuery:    "Alice Example",
			who:         `{"query":"Alice Example","candidates":[{"who":"Alice Example","match_quality":"exact","sources":["imessage"],"messages":4}]}`,
		},
		fakeCrawler{
			name:        "telecrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"telegram","display_name":"Telegram"}`,
			searchQuery: "boat trip",
			searchWho:   "Alice Example",
			search:      `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
			whoQuery:    "Alice Example",
			who:         `{"query":"Alice Example","candidates":[{"who":"Alice Example","match_quality":"exact","sources":["telegram"],"messages":7}]}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--who", "Alice Example")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Alice Example → Alice Example (Messages, Telegram)") {
		t.Fatalf("stdout missing resolution line:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoOnlyFansOutToSourcesThatResolvedPerson(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:        "imsgcrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			searchQuery: "boat trip",
			searchWho:   "alice@example.com",
			search:      `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice Example","snippet":"Example match"}],"total_matches":1,"truncated":false}`,
			whoQuery:    "alice@example.com",
			who:         `{"query":"alice@example.com","candidates":[{"who":"Alice Example","identifiers":["alice@example.com"],"match_quality":"exact","sources":["imessage"],"messages":4}]}`,
		},
		fakeCrawler{
			name:       "telecrawl",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"telegram","display_name":"Telegram"}`,
			search:     `{"error":{"code":"unknown_who","message":"No person matched \"alice@example.com\".","remedy":"retry with a suggestion or search without --who"}}`,
			searchExit: 5,
			whoQuery:   "alice@example.com",
			who:        `{"query":"alice@example.com","candidates":[]}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip", "--who", "alice@example.com")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload federatedSearchEnvelope
	if err := decodeContractJSON([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(payload.Results) != 1 || payload.Results[0].Source != "imessage" {
		t.Fatalf("results = %+v", payload.Results)
	}
	if len(payload.FailedSources) != 0 {
		t.Fatalf("failed_sources = %+v", payload.FailedSources)
	}
	if strings.Contains(stderr, "search failed") || strings.Contains(stderr, "trawl doctor") {
		t.Fatalf("stderr reported unknown person as source failure:\n%s", stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoHumanHeadingShowsResolvedPerson(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who","short_refs"],"id":"imessage","display_name":"Messages"}`,
		searchQuery: "boat trip",
		searchWho:   "alice@example.com",
		search:      `{"query":"boat trip","results":[{"ref":"imessage:msg/1","short_ref":"abc12","time":"2026-05-14T09:12:00Z","who":"Alice Example","snippet":"Example match"}],"total_matches":1,"truncated":false}`,
		whoQuery:    "alice@example.com",
		who:         `{"query":"alice@example.com","candidates":[{"who":"Alice Example","identifiers":["alice@example.com"],"match_quality":"exact","sources":["imessage"],"messages":4}]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--who", "alice@example.com")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `Search "boat trip" with Alice Example: showing 1 of 1, newest first.`) {
		t.Fatalf("stdout missing resolved who heading:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoTreatsFannedOutUnknownWhoAsEmpty(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:        "imsgcrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			searchQuery: "boat trip",
			searchWho:   "alice@example.com",
			search:      `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice Example","snippet":"Example match"}],"total_matches":1,"truncated":false}`,
			whoQuery:    "alice@example.com",
			who:         `{"query":"alice@example.com","candidates":[{"who":"Alice Example","identifiers":["alice@example.com"],"match_quality":"exact","sources":["imessage"],"messages":4}]}`,
		},
		fakeCrawler{
			name:        "telecrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"telegram","display_name":"Telegram"}`,
			searchQuery: "boat trip",
			searchWho:   "alice@example.com",
			search:      `{"error":{"code":"unknown_who","message":"No person matched \"alice@example.com\".","remedy":"retry with a suggestion or search without --who"}}`,
			searchExit:  5,
			whoQuery:    "alice@example.com",
			who:         `{"query":"alice@example.com","candidates":[{"who":"Alice Example","identifiers":["alice@example.com"],"match_quality":"exact","sources":["telegram"],"messages":7}]}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "boat trip", "--who", "alice@example.com")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload federatedSearchEnvelope
	if err := decodeContractJSON([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(payload.Results) != 1 || payload.Results[0].Source != "imessage" {
		t.Fatalf("results = %+v", payload.Results)
	}
	if len(payload.FailedSources) != 0 {
		t.Fatalf("failed_sources = %+v", payload.FailedSources)
	}
	if strings.Contains(stderr, "search failed") || strings.Contains(stderr, "trawl doctor") {
		t.Fatalf("stderr reported unknown person as source failure:\n%s", stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoPassesThroughWithPositionalSource(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:        "imsgcrawl",
		metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		searchQuery: "boat trip",
		searchWho:   "Alice Example",
		search:      `{"query":"boat trip","results":[],"total_matches":0,"truncated":false}`,
		whoQuery:    "Alice Example",
		who:         `{"query":"Alice Example","candidates":[{"who":"Alice Example","match_quality":"exact","sources":["imessage"],"messages":4}]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "imessage", "boat", "trip", "--who", "Alice Example")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoSkipsSourcesWithoutCapability(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "imsgcrawl",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			search:   `{"query":"boat trip","results":[{"ref":"imessage:msg/1","time":"2026-05-14T09:12:00Z","who":"Alice","snippet":"Example match"}],"total_matches":1,"truncated":false}`,
			whoQuery: "Alice",
			who:      `{"query":"Alice","candidates":[{"who":"Alice Example","match_quality":"exact","sources":["imessage"],"messages":4}]}`,
		},
		fakeCrawler{
			name:       "telecrawl",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"telegram","display_name":"Telegram"}`,
			searchExit: 64,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "boat trip", "--who", "Alice")
	if code != 3 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"imessage:msg/1",
		"note: 1 of 2 sources skipped — results are partial (see stderr)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "telegram cannot filter by person yet") {
		t.Fatalf("stderr missing capability note:\n%s", stderr)
	}
	if strings.Contains(stderr, "telegram search failed") {
		t.Fatalf("stderr reported skipped source as failure:\n%s", stderr)
	}
}

func TestSearchWhoAmbiguousFederatedResolutionJSON(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:       "imsgcrawl",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			searchExit: 99,
			whoQuery:   "alex",
			who: `{"query":"alex","candidates":[
				{"who":"Alex Jones","identifiers":["alex.jones@example.com"],"match_quality":"prefix","sources":["imessage"],"last_seen":"2026-06-30T20:30:00Z","messages":9},
				{"who":"Alex Chen","identifiers":["alex.chen@example.com"],"match_quality":"prefix","sources":["imessage"],"last_seen":"2026-05-30T20:30:00Z","messages":3}
			]}`,
		},
		fakeCrawler{
			name:       "telecrawl",
			metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"telegram","display_name":"Telegram"}`,
			searchExit: 99,
			whoQuery:   "alex",
			who: `{"query":"alex","candidates":[
				{"who":"Alex Jones","identifiers":["@alexj"],"match_quality":"prefix","sources":["telegram"],"last_seen":"2026-07-01T08:00:00Z","messages":12}
			]}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "specs", "--who", "alex")
	if code != 4 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload whoResolutionErrorEnvelope
	if err := decodeContractJSON([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if payload.Error.Code != "ambiguous_who" {
		t.Fatalf("code = %q payload=%+v", payload.Error.Code, payload)
	}
	if len(payload.Candidates) != 2 {
		t.Fatalf("candidates = %+v", payload.Candidates)
	}
	if payload.Candidates[0].Who != "Alex Jones" || len(payload.Candidates[0].Identifiers) != 2 {
		t.Fatalf("joined Alex Jones candidate = %+v", payload.Candidates[0])
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoAmbiguousHumanOutputCapsCandidates(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:       "imsgcrawl",
		metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		searchExit: 99,
		whoQuery:   "michael",
		who:        whoCandidatesJSON(t, "michael", numberedWhoCandidates("Michael", 12)),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "specs", "--who", "michael")
	if code != 4 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %s", stdout)
	}
	if !strings.Contains(stderr, "…and 2 more; narrow the name") {
		t.Fatalf("stderr missing candidate cap note:\n%s", stderr)
	}
	if strings.Contains(stderr, "Michael 10") || strings.Contains(stderr, "Michael 11") {
		t.Fatalf("stderr leaked candidates beyond display cap:\n%s", stderr)
	}
}

func TestSearchWhoAmbiguousJSONCapsCandidatesWithTotal(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:       "imsgcrawl",
		metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		searchExit: 99,
		whoQuery:   "michael",
		who:        whoCandidatesJSON(t, "michael", numberedWhoCandidates("Michael", 55)),
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "specs", "--who", "michael")
	if code != 4 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload whoResolutionErrorEnvelope
	if err := decodeContractJSON([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if payload.TotalCandidates != 55 {
		t.Fatalf("total_candidates = %d payload=%+v", payload.TotalCandidates, payload)
	}
	if len(payload.Candidates) != 50 {
		t.Fatalf("candidates = %d, want 50", len(payload.Candidates))
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoUnknownFederatedResolutionJSON(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:       "imsgcrawl",
		metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		searchExit: 99,
		whoQuery:   "alxe",
		who:        `{"query":"alxe","candidates":[]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "specs", "--who", "alxe")
	if code != 5 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload whoResolutionErrorEnvelope
	if err := decodeContractJSON([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if payload.Error.Code != "unknown_who" {
		t.Fatalf("code = %q payload=%+v", payload.Error.Code, payload)
	}
	if len(payload.DidYouMean) != 0 {
		t.Fatalf("did_you_mean = %+v", payload.DidYouMean)
	}
	if !strings.Contains(payload.Hint, "without --who") {
		t.Fatalf("hint = %q", payload.Hint)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoCloseSpellingOnlyFederatedResolutionJSON(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:       "imsgcrawl",
		metadata:   `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		searchExit: 99,
		whoQuery:   "alxe",
		who: `{"query":"alxe","candidates":[
			{"who":"Alex Jones","identifiers":["alex.jones@example.com"],"match_quality":"close_spelling","sources":["imessage"],"last_seen":"2026-06-30T20:30:00Z","messages":9}
		]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "specs", "--who", "alxe")
	if code != 5 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload whoResolutionErrorEnvelope
	if err := decodeContractJSON([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if payload.Error.Code != "unknown_who" {
		t.Fatalf("code = %q payload=%+v", payload.Error.Code, payload)
	}
	if len(payload.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want none", payload.Candidates)
	}
	if len(payload.DidYouMean) != 1 || payload.DidYouMean[0].Who != "Alex Jones" {
		t.Fatalf("did_you_mean = %+v", payload.DidYouMean)
	}
	if payload.DidYouMean[0].MatchQuality != "close_spelling" {
		t.Fatalf("match_quality = %q, want close_spelling", payload.DidYouMean[0].MatchQuality)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchWhoUsesClawdexIdentifierUpgradeJoin(t *testing.T) {
	binDir := writeFakeCrawlers(t,
		fakeCrawler{
			name:     "clawdex",
			metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","doctor"],"id":"clawdex","display_name":"Contacts"}`,
			whoQuery: "alex",
			who: `{"query":"alex","candidates":[
				{"who":"Alex Jones","identifiers":["+15550100123"],"match_quality":"prefix","sources":["imessage","whatsapp"],"last_seen":"2026-07-01T08:00:00Z","messages":20}
			]}`,
		},
		fakeCrawler{
			name:        "imsgcrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
			searchQuery: "specs",
			searchWho:   "+1 (555) 010-0123",
			search:      `{"query":"specs","results":[],"total_matches":0,"truncated":false}`,
			whoQuery:    "alex",
			who: `{"query":"alex","candidates":[
				{"who":"+1 (555) 010-0123","identifiers":["+1 (555) 010-0123"],"match_quality":"contains","sources":["imessage"],"last_seen":"2026-06-30T20:30:00Z","messages":9}
			]}`,
		},
		fakeCrawler{
			name:        "wacrawl",
			metadata:    `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"whatsapp","display_name":"WhatsApp"}`,
			searchQuery: "specs",
			searchWho:   "+15550100123",
			search:      `{"query":"specs","results":[],"total_matches":0,"truncated":false}`,
			whoQuery:    "alex",
			who: `{"query":"alex","candidates":[
				{"who":"AJ","identifiers":["+15550100123"],"match_quality":"contains","sources":["whatsapp"],"last_seen":"2026-07-01T08:00:00Z","messages":11}
			]}`,
		},
	)
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "specs", "--who", "alex")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "alex → Alex Jones (Contacts, Messages, WhatsApp)") {
		t.Fatalf("stdout missing clawdex-upgraded resolution:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchFilterOnlyPassesThroughWithoutQuery(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:          "imsgcrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor","who"],"id":"imessage","display_name":"Messages"}`,
		searchNoQuery: true,
		searchWho:     "Alice Example",
		search:        `{"query":"","results":[],"total_matches":0,"truncated":false}`,
		whoQuery:      "Alice Example",
		who:           `{"query":"Alice Example","candidates":[{"who":"Alice Example","match_quality":"exact","sources":["imessage"],"messages":4}]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "search", "--who", "Alice Example", "--after", "2026-01-01")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Alice Example → Alice Example (Messages)") {
		t.Fatalf("stdout missing resolution line:\n%s", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}

func TestSearchJSONIgnoresLegacyWhoMatched(t *testing.T) {
	binDir := writeFakeCrawlers(t, fakeCrawler{
		name:     "imsgcrawl",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open","doctor"],"id":"imessage","display_name":"Messages"}`,
		search:   `{"query":"specs","results":[],"total_matches":0,"truncated":false,"who_matched":["Alex Jones","Alex Chen"]}`,
	})
	t.Setenv("PATH", binDir)
	t.Setenv("HOME", syntheticHome(t))

	stdout, stderr, code := runCLI(t, "--json", "search", "specs")
	if code != 0 {
		t.Fatalf("code = %d stdout=%s stderr=%s", code, stdout, stderr)
	}
	want := `{"query":"specs","results":[],"total_matches":0,"truncated":false}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %s\nwant = %s", stdout, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %s", stderr)
	}
}
