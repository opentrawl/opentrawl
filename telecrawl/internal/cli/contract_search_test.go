package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
)

func TestSearchJSONIsBoundedAndReportsTruncation(t *testing.T) {
	t.Run("default limit", func(t *testing.T) {
		db := seedSearchArchive(t, 25)
		payload := runSearchJSON(t, db, "search", "launch", "--json")
		if len(payload.Results) != 20 || payload.TotalMatches != 25 || !payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
		assertSearchResultShape(t, payload.Results[0])
	})

	t.Run("explicit limit without truncation", func(t *testing.T) {
		db := seedSearchArchive(t, 25)
		payload := runSearchJSON(t, db, "search", "--limit", "30", "launch", "--json")
		if len(payload.Results) != 25 || payload.TotalMatches != 25 || payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
	})

	t.Run("no hidden cap", func(t *testing.T) {
		db := seedSearchArchive(t, 205)
		payload := runSearchJSON(t, db, "search", "--limit", "500", "launch", "--json")
		if len(payload.Results) != 205 || payload.TotalMatches != 205 || payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
	})
	t.Run("all returns everything", func(t *testing.T) {
		db := seedSearchArchive(t, 205)
		payload := runSearchJSON(t, db, "search", "--all", "launch", "--json")
		if len(payload.Results) != 205 || payload.TotalMatches != 205 || payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
		}
	})
	t.Run("all with limit is refused", func(t *testing.T) {
		db := seedSearchArchive(t, 5)
		_, stderr, err := runCLI(t, "--db", db, "search", "--all", "--limit", "3", "launch", "--json")
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("search --all --limit err = %v exit = %d stderr=%s", err, ExitCode(err), stderr)
		}
	})
	t.Run("all resolves short refs across the chunk boundary", func(t *testing.T) {
		// Above the 900 short-ref chunk size, so --all exercises the batched
		// alias lookup (SQLite host-parameter limit). runSearchJSON asserts
		// every result carries a valid short_ref.
		db := seedSearchArchive(t, 1000)
		payload := runSearchJSON(t, db, "search", "--all", "launch", "--json")
		if len(payload.Results) != 1000 || payload.Truncated {
			t.Fatalf("search --all = %d results trunc=%v, want 1000 and not truncated", len(payload.Results), payload.Truncated)
		}
	})
}

func TestSearchWhoFiltersParticipants(t *testing.T) {
	db := seedWhoSearchArchive(t)

	beforeQuery := runSearchJSON(t, db, "search", "--who", "Alice", "needle", "--json")
	if len(beforeQuery.Results) != 1 || beforeQuery.TotalMatches != 1 || beforeQuery.Results[0].Who != "Alice Example" {
		t.Fatalf("search --who before query = %#v", beforeQuery)
	}
	assertWhoResolved(t, beforeQuery.WhoResolved, "Alice Example", "@alice_example")

	afterQuery := runSearchJSON(t, db, "search", "needle", "--who", "Alice Example", "--json")
	if len(afterQuery.Results) != 1 || afterQuery.TotalMatches != 1 || afterQuery.Results[0].Who != "Alice Example" {
		t.Fatalf("search --who after query = %#v", afterQuery)
	}
	assertWhoResolved(t, afterQuery.WhoResolved, "Alice Example", "+1555200200")

	collapsed := runSearchJSON(t, db, "search", "needle", "--who", " Alice   Example ", "--json")
	if len(collapsed.Results) != 1 || collapsed.TotalMatches != 1 || collapsed.Results[0].Who != "Alice Example" {
		t.Fatalf("search --who collapsed whitespace = %#v", collapsed)
	}
}

func TestWhoCommandResolvesGenerouslyAndDedupes(t *testing.T) {
	db := seedWhoSearchArchive(t)

	payload := runWhoJSON(t, db, "Alic Exampel", "--json")
	if payload.Query != "Alic Exampel" || len(payload.Candidates) != 1 {
		t.Fatalf("who payload = %#v, want one close-spelling candidate", payload)
	}
	candidate := payload.Candidates[0]
	if candidate.Who != "Alice Example" || candidate.Messages != 1 {
		t.Fatalf("who candidate = %#v, want Alice with one message", candidate)
	}
	if !hasString(candidate.Identifiers, "+1555200200") || !hasString(candidate.Identifiers, "@alice_example") || !hasString(candidate.Identifiers, "200") {
		t.Fatalf("identifiers = %#v, want phone, handle, and jid", candidate.Identifiers)
	}
	if _, err := time.Parse(time.RFC3339, candidate.LastSeen); err != nil {
		t.Fatalf("last_seen = %q err=%v", candidate.LastSeen, err)
	}

	substring := runWhoJSON(t, db, "example", "--json")
	if len(substring.Candidates) != 3 {
		t.Fatalf("substring candidates = %#v, want Alice and both Jordans", substring.Candidates)
	}
}

func TestWhoTextUsesFittedTable(t *testing.T) {
	t.Setenv("COLUMNS", "72")
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "who", "alice")
	if err != nil {
		t.Fatalf("who text: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	conformance.AssertHumanOutput(t, stdout)
	for _, want := range []string{"who", "last seen", "messages", "identifiers", "Alice Example", "@alice_example"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("who text missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "{") || strings.Contains(stdout, "}") {
		t.Fatalf("who text looks like JSON:\n%s", stdout)
	}
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if len([]rune(line)) > 72 {
			t.Fatalf("who line exceeds COLUMNS=72: %d %q\n%s", len([]rune(line)), line, stdout)
		}
	}
}

func TestSearchWhoRejectsBlankIdentity(t *testing.T) {
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "needle", "--who", "   ", "--json")
	if err == nil {
		t.Fatalf("blank --who succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(err.Error(), "--who requires an identity") {
		t.Fatalf("blank --who error = %v, want identity error", err)
	}
	// One shape (crawlkit/output): a usage error in --json mode is the
	// structured {"error": {...}} envelope on stdout, code "usage".
	assertUsageErrorEnvelope(t, stdout, "--who requires an identity")
}

func assertUsageErrorEnvelope(t *testing.T, stdout, wantMessage string) {
	t.Helper()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("usage error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "usage" || !strings.Contains(payload.Error.Message, wantMessage) {
		t.Fatalf("usage error = %#v, want code usage with %q", payload.Error, wantMessage)
	}
}

func TestSearchWhoReportsAmbiguousMatches(t *testing.T) {
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "needle", "--who", "jordan", "--json")
	if err == nil {
		t.Fatalf("ambiguous --who succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if code := ExitCode(err); code != 4 {
		t.Fatalf("exit code = %d, want 4", code)
	}
	var payload struct {
		Error struct {
			Code       string             `json:"code"`
			Candidates []testWhoCandidate `json:"candidates"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("ambiguous error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "ambiguous_who" || len(payload.Error.Candidates) != 2 {
		t.Fatalf("ambiguous error = %#v", payload)
	}
	if strings.Contains(stdout, `"results"`) {
		t.Fatalf("ambiguous error ran a search: %s", stdout)
	}
}

func TestSearchWhoHumanAmbiguityShowsRetryTable(t *testing.T) {
	t.Setenv("COLUMNS", "100")
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "needle", "--who", "jordan")
	if err == nil {
		t.Fatalf("ambiguous --who text succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("ambiguous text stdout=%q stderr=%q, want quiet streams", stdout, stderr)
	}
	text := err.Error()
	conformance.AssertHumanOutput(t, text)
	for _, want := range []string{
		`ambiguous --who "jordan": 2 people match.`,
		"who",
		"last seen",
		"messages",
		"identifiers",
		"Retry with: telecrawl search needle --who @jordan_upper",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("ambiguous text missing %q:\n%s", want, text)
		}
	}
}

func TestSearchWhoReportsUnknown(t *testing.T) {
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "needle", "--who", "Nobody", "--json")
	if err == nil {
		t.Fatalf("unknown --who succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if code := ExitCode(err); code != 5 {
		t.Fatalf("exit code = %d, want 5", code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("unknown error json = %s err=%v", stdout, err)
	}
	var payload struct {
		Error struct {
			Code       string             `json:"code"`
			DidYouMean []testWhoCandidate `json:"did_you_mean"`
			Hint       string             `json:"hint"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("unknown error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "unknown_who" || len(payload.Error.DidYouMean) != 0 || !strings.Contains(payload.Error.Hint, "Search without --who") {
		t.Fatalf("unknown error = %#v", payload)
	}
	// One shape (crawlkit/output): empty fields are omitted. With no
	// suggestions there is no did_you_mean; the hint carries the guidance.
	var errorBody map[string]json.RawMessage
	if err := json.Unmarshal(raw["error"], &errorBody); err != nil {
		t.Fatalf("error body = %s err=%v", raw["error"], err)
	}
	if _, ok := errorBody["did_you_mean"]; ok {
		t.Fatalf("empty did_you_mean should be omitted: %s", stdout)
	}
}

func TestSearchWhoReportsFuzzyOnlySingleMatchAsUnknownSuggestion(t *testing.T) {
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "needle", "--who", "Alic Exampel", "--json")
	if err == nil {
		t.Fatalf("fuzzy-only --who succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if code := ExitCode(err); code != 5 {
		t.Fatalf("exit code = %d, want 5", code)
	}
	var payload struct {
		Error struct {
			Code       string             `json:"code"`
			DidYouMean []testWhoCandidate `json:"did_you_mean"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("fuzzy-only error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "unknown_who" || len(payload.Error.DidYouMean) != 1 || payload.Error.DidYouMean[0].Who != "Alice Example" {
		t.Fatalf("fuzzy-only error = %#v", payload)
	}
	if strings.Contains(stdout, `"results"`) {
		t.Fatalf("fuzzy-only error ran a search: %s", stdout)
	}
}

func TestSearchWhoIsCaseInsensitive(t *testing.T) {
	db := seedWhoSearchArchive(t)

	payload := runSearchJSON(t, db, "search", "needle", "--who", "aLiCe eXaMpLe", "--json")
	if len(payload.Results) != 1 || payload.TotalMatches != 1 || payload.Results[0].Who != "Alice Example" {
		t.Fatalf("case-insensitive --who payload = %#v", payload)
	}
}

func TestSearchWhoFiltersByExactIdentifier(t *testing.T) {
	db := seedWhoSearchArchive(t)

	handle := runSearchJSON(t, db, "search", "needle", "--who", "@alice_example", "--json")
	if len(handle.Results) != 1 || handle.Results[0].Who != "Alice Example" {
		t.Fatalf("handle --who payload = %#v", handle)
	}
	assertWhoResolved(t, handle.WhoResolved, "Alice Example", "@alice_example")

	phone := runSearchJSON(t, db, "search", "needle", "--who", "+1555200200", "--json")
	if len(phone.Results) != 1 || phone.Results[0].Who != "Alice Example" {
		t.Fatalf("phone --who payload = %#v", phone)
	}
	assertWhoResolved(t, phone.WhoResolved, "Alice Example", "+1555200200")
}

func TestSearchAllowsFilterOnlyWithWhoAfterOrBefore(t *testing.T) {
	db := seedWhoSearchArchive(t)

	whoOnly := runSearchJSON(t, db, "search", "--who", "Recipient Person", "--json")
	if whoOnly.Query != "" || len(whoOnly.Results) != 2 || whoOnly.TotalMatches != 2 || whoOnly.Truncated {
		t.Fatalf("who-only search = %#v, want two newest recipient messages", whoOnly)
	}
	assertWhoResolved(t, whoOnly.WhoResolved, "Recipient Person", "300")

	afterOnly := runSearchJSON(t, db, "search", "--after", "2026-07-02T12:05:00Z", "--json")
	if afterOnly.Query != "" || len(afterOnly.Results) != 2 || afterOnly.TotalMatches != 2 || afterOnly.Truncated || afterOnly.WhoResolved != nil {
		t.Fatalf("after-only search = %#v, want two newest messages", afterOnly)
	}
}

func TestOwnerWhoResolverRendersMe(t *testing.T) {
	db := seedWhoSearchArchive(t)

	ownerByID := runWhoJSON(t, db, "999", "--json")
	if len(ownerByID.Candidates) == 0 || ownerByID.Candidates[0].Who != "me" {
		t.Fatalf("owner id candidates = %#v, want owner as me", ownerByID.Candidates)
	}
	if !hasString(ownerByID.Candidates[0].Identifiers, "me") || hasString(ownerByID.Candidates[0].Identifiers, "999") {
		t.Fatalf("owner id identifiers = %#v, want only rendered owner label", ownerByID.Candidates[0].Identifiers)
	}

	ownerSearch := runSearchJSON(t, db, "search", "--who", "me", "needle", "--json")
	if ownerSearch.WhoResolved == nil || ownerSearch.WhoResolved.Who != "me" || len(ownerSearch.Results) != 1 || ownerSearch.Results[0].Who != "me" {
		t.Fatalf("owner search = %#v, want only owner row as me", ownerSearch)
	}
	if !hasString(ownerSearch.WhoResolved.Identifiers, "me") || hasString(ownerSearch.WhoResolved.Identifiers, "999") {
		t.Fatalf("owner search identifiers = %#v, want only rendered owner label", ownerSearch.WhoResolved.Identifiers)
	}

	ownerOpen := runOpenJSON(t, db, ownerSearch.Results[0].Ref)
	if ownerOpen.Message.Sender.DisplayName != "me" || ownerOpen.Message.Sender.Ref != "" {
		t.Fatalf("owner open sender = %#v, want me without raw sender ref", ownerOpen.Message.Sender)
	}
}

func TestSearchWhoFilterOnlyExcludesChatTitlesAndMatchesUnnamedIDs(t *testing.T) {
	db := seedWhoResolverDefectArchive(t)

	payload := runSearchJSON(t, db, "search", "--who", "jef", "--after", "2026-07-02T12:00:00Z", "--json")
	if payload.WhoResolved == nil || payload.WhoResolved.Who != "Jef Example" || !hasString(payload.WhoResolved.Identifiers, "200") {
		t.Fatalf("who resolved = %#v, want Jef Example", payload.WhoResolved)
	}
	if payload.Query != "" || len(payload.Results) != 2 || payload.TotalMatches != 2 {
		t.Fatalf("filter-only --who payload = %#v, want two group messages", payload)
	}

	partialID := runWhoJSON(t, db, "165", "--json")
	if len(partialID.Candidates) != 1 || partialID.Candidates[0].Who != "165355235" {
		t.Fatalf("partial id candidates = %#v, want unnamed numeric participant", partialID.Candidates)
	}

	fullID := runWhoJSON(t, db, "165355235", "--json")
	if len(fullID.Candidates) != 1 || fullID.Candidates[0].Who != "165355235" {
		t.Fatalf("full id candidates = %#v, want unnamed numeric participant", fullID.Candidates)
	}
}

func TestSearchWithoutQueryOrV12FilterShowsUsage(t *testing.T) {
	db := seedWhoSearchArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "--json")
	if err == nil {
		t.Fatalf("blank search succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if code := ExitCode(err); code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if stderr != "" {
		t.Fatalf("blank search stderr=%q, want quiet stderr", stderr)
	}
	// One shape (crawlkit/output): the structured error envelope on stdout
	// keeps a short message; the full usage text stays on err for stderr.
	assertUsageErrorEnvelope(t, stdout, "search takes a query")
	if !strings.Contains(err.Error(), "usage: telecrawl search") {
		t.Fatalf("blank search error missing usage: %v", err)
	}
}

func TestSearchJSONEmitsWhoForDirectSenderRows(t *testing.T) {
	db := seedDirectSenderArchive(t)
	payload := runSearchJSON(t, db, "search", "needle", "--json")
	if len(payload.Results) != 1 {
		t.Fatalf("search results = %#v", payload.Results)
	}
	if payload.Results[0].Who != "Direct Person" {
		t.Fatalf("search who = %q, want Direct Person", payload.Results[0].Who)
	}
}

func TestSearchWhoTotalsStayFilteredUnderLimit(t *testing.T) {
	db := seedWhoSearchArchive(t)

	payload := runSearchJSON(t, db, "search", "needle", "--who", "Recipient Person", "--limit", "1", "--json")
	if len(payload.Results) != 1 || payload.TotalMatches != 2 || !payload.Truncated {
		t.Fatalf("filtered limit payload = %#v", payload)
	}
	for _, result := range payload.Results {
		if result.Where != "Recipient Person" && result.Where != "direct" {
			t.Fatalf("result outside recipient chat = %#v", result)
		}
	}
}

func TestSearchHumanRefFallsBackToFullRef(t *testing.T) {
	var out strings.Builder
	r := &runtime{stdout: &out}
	err := r.printSearch(searchEnvelope{
		Query:        "launch",
		Limit:        defaultSearchLimit,
		TotalMatches: 1,
		Results: []searchResult{{
			Ref:     "telecrawl:msg/1",
			Time:    "2026-07-02T14:00:00+02:00",
			Who:     "Ada Example",
			Where:   "direct",
			Snippet: "synthetic launch note",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "telecrawl:msg/1") {
		t.Fatalf("search output did not fall back to full ref:\n%s", out.String())
	}
}

func TestSearchTruncationHintsUncapped(t *testing.T) {
	db := seedSearchArchive(t, 205)
	stdout, stderr, err := runCLI(t, "--db", db, "search", "--limit", "100", "launch")
	if err != nil {
		t.Fatalf("search text: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	// The More hint doubles the shown limit with no hidden cap, and the All
	// hint offers the whole result set (TRAWL-84).
	if !strings.Contains(stdout, `More: telecrawl search "launch" --limit 200`) {
		t.Fatalf("search more hint (uncapped double) missing:\n%s", stdout)
	}
	// The All hint carries the query so it is runnable verbatim (bare search
	// without a query is a usage error).
	if !strings.Contains(stdout, `All: telecrawl search "launch" --all`) {
		t.Fatalf("search all hint missing or dropped the query:\n%s", stdout)
	}
}

func TestSearchUsesHumaneWhoFallbacks(t *testing.T) {
	db := seedSearchWhoArchive(t)

	username := runSearchJSON(t, db, "search", "username-fallback", "--json")
	if len(username.Results) != 1 || username.Results[0].Who != "fixture_user" {
		t.Fatalf("username fallback result = %#v", username.Results)
	}

	firstName := runSearchJSON(t, db, "search", "firstname-fallback", "--json")
	if len(firstName.Results) != 1 || firstName.Results[0].Who != "Ada" {
		t.Fatalf("first-name fallback result = %#v", firstName.Results)
	}

	stdout, stderr, err := runCLI(t, "--db", db, "search", "no-human-fallback", "--json")
	if err != nil {
		t.Fatalf("search without human fallback: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	var payload struct {
		Results []map[string]json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	conformance.AssertSearchEnvelope(t, []byte(stdout))
	if len(payload.Results) != 1 {
		t.Fatalf("search results = %#v", payload.Results)
	}
	if _, ok := payload.Results[0]["who"]; ok {
		t.Fatalf("machine-only sender should omit who: %s", stdout)
	}
	if strings.Contains(stdout, "87092563") || strings.Contains(stdout, "unknown") {
		t.Fatalf("search leaked raw peer fallback: %s", stdout)
	}
}

func TestSearchTextUsesContractRows(t *testing.T) {
	db := seedSearchArchive(t, 2)
	stdout, stderr, err := runCLI(t, "--db", db, "search", "launch")
	if err != nil {
		t.Fatalf("search text: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	conformance.AssertHumanOutput(t, stdout)
	for _, disallowed := range []string{"[launch]", `"source_pk"`, `"sender_jid"`, "unknown"} {
		if strings.Contains(stdout, disallowed) {
			t.Fatalf("search text contains %q:\n%s", disallowed, stdout)
		}
	}
	if !strings.Contains(stdout, "synthetic launch note") || !strings.Contains(stdout, `Search "launch": showing 2 of 2.`) {
		t.Fatalf("search text = %s", stdout)
	}
}

func TestSearchJSONShortRefsMatchHumanAliases(t *testing.T) {
	db := seedSearchArchive(t, 2)
	payload := runSearchJSON(t, db, "search", "launch", "--json")
	if len(payload.Results) == 0 {
		t.Fatal("search returned no results")
	}
	jsonAlias := payload.Results[0].ShortRef
	assertShortRefAlias(t, jsonAlias)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "launch")
	if err != nil {
		t.Fatalf("search text: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	conformance.AssertHumanOutput(t, stdout)
	if !containsTableToken(stdout, jsonAlias) {
		t.Fatalf("search text missing json alias %q:\n%s", jsonAlias, stdout)
	}
}
