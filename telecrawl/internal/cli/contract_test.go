package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/telecrawl/internal/store"
)

func TestMetadataJSONUsesContractShape(t *testing.T) {
	stdout, stderr, err := runCLI(t, "metadata", "--json")
	if err != nil {
		t.Fatalf("metadata: %v stderr=%s", err, stderr)
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("metadata json = %s err=%v", stdout, err)
	}
	wantKeys := []string{"schema_version", "contract_version", "id", "display_name", "version", "capabilities"}
	if len(root) != len(wantKeys) {
		t.Fatalf("metadata keys = %#v, want %v", root, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := root[key]; !ok {
			t.Fatalf("metadata missing key %q: %#v", key, root)
		}
	}
	var payload struct {
		SchemaVersion   int      `json:"schema_version"`
		ContractVersion int      `json:"contract_version"`
		ID              string   `json:"id"`
		DisplayName     string   `json:"display_name"`
		Version         string   `json:"version"`
		Capabilities    []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("metadata json = %s err=%v", stdout, err)
	}
	if payload.SchemaVersion != 1 || payload.ContractVersion != 1 || payload.ID != "telecrawl" || payload.DisplayName != "Telegram" || payload.Version == "" {
		t.Fatalf("metadata = %#v", payload)
	}
	if !slices.Contains(payload.Capabilities, "contacts_export") {
		t.Fatalf("metadata capabilities = %#v, want contacts_export", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "open") {
		t.Fatalf("metadata capabilities = %#v, want open", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "who") {
		t.Fatalf("metadata capabilities = %#v, want who", payload.Capabilities)
	}
	if !slices.Contains(payload.Capabilities, "short_refs") {
		t.Fatalf("metadata capabilities = %#v, want short_refs", payload.Capabilities)
	}
}

func TestStatusJSONUsesContractShapeAndStates(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "telecrawl.db")
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "missing")
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatalf("status --json created missing archive: err=%v", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "telecrawl.db")
		st, err := store.Open(context.Background(), db)
		if err != nil {
			t.Fatal(err)
		}
		_ = st.Close()
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "empty")
		if status.Counts[0].Value != 0 || status.Counts[1].Value != 0 || status.Counts[2].Value != 0 {
			t.Fatalf("empty counts = %#v", status.Counts)
		}
	})

	t.Run("ok", func(t *testing.T) {
		db := seedArchive(t, 1, time.Now().Add(-time.Hour))
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "ok")
		if status.Freshness.LastSync == "" {
			t.Fatalf("missing freshness: %#v", status)
		}
		if _, err := time.Parse(time.RFC3339, status.Freshness.LastSync); err != nil {
			t.Fatalf("last_sync = %q err=%v", status.Freshness.LastSync, err)
		}
		if status.Counts[0].Value != 1 || status.Counts[1].Value != 1 || status.Counts[2].Value != 2020 {
			t.Fatalf("ok counts = %#v", status.Counts)
		}
	})

	t.Run("stale", func(t *testing.T) {
		db := seedArchive(t, 1, time.Now().Add(-48*time.Hour))
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "stale")
	})

	t.Run("error", func(t *testing.T) {
		db := filepath.Join(t.TempDir(), "telecrawl.db")
		if err := os.WriteFile(db, []byte("not sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
		status := runStatusJSON(t, db)
		assertStatusState(t, status, "error")
	})
}

func TestStatusJSONSerializesHumanLogTail(t *testing.T) {
	db := seedSearchArchive(t, 1)
	if _, _, err := runCLI(t, "--db", db, "open", "not-a-ref"); err == nil {
		t.Fatal("open not-a-ref succeeded, want logged error")
	}
	stdout, stderr, err := runCLI(t, "--db", db, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	for _, forbidden := range []string{`"run_id"`, `"last_event"`, `"event"`, "event=", "visibility="} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("status log leaked %q:\n%s", forbidden, stdout)
		}
	}
	var payload struct {
		Log *struct {
			LastRun         *testLogEvent `json:"last_run"`
			MostRecentError *testLogEvent `json:"most_recent_error"`
		} `json:"log"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("status json = %s err=%v", stdout, err)
	}
	if payload.Log == nil || payload.Log.LastRun == nil || payload.Log.LastRun.WhatHappened == "" || payload.Log.LastRun.When == "" {
		t.Fatalf("last run log = %#v", payload.Log)
	}
	if payload.Log.MostRecentError == nil || payload.Log.MostRecentError.WhatHappened == "" || payload.Log.MostRecentError.When == "" || payload.Log.MostRecentError.Remedy == "" {
		t.Fatalf("recent error log = %#v", payload.Log)
	}
}

func TestDoctorJSONUsesChecksShape(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		source := readableTelegramSource(t)
		db := seedArchive(t, 1, time.Now())
		stdout, stderr, err := runCLI(t, "--db", db, "doctor", "--path", source, "--json")
		if err != nil {
			t.Fatalf("doctor: %v stderr=%s", err, stderr)
		}
		checks := decodeDoctorChecks(t, stdout)
		if len(checks) != 2 {
			t.Fatalf("checks = %#v", checks)
		}
		for _, check := range checks {
			if check.State != "ok" {
				t.Fatalf("check = %#v, want ok", check)
			}
		}
	})

	t.Run("fail", func(t *testing.T) {
		dir := t.TempDir()
		stdout, stderr, err := runCLI(t, "--db", filepath.Join(dir, "missing.db"), "doctor", "--path", filepath.Join(dir, "missing-source"), "--json")
		if err != nil {
			t.Fatalf("doctor: %v stderr=%s", err, stderr)
		}
		checks := decodeDoctorChecks(t, stdout)
		if len(checks) != 2 {
			t.Fatalf("checks = %#v", checks)
		}
		for _, check := range checks {
			if check.State != "missing" || check.Message == "" || check.Remedy == "" {
				t.Fatalf("failing check needs message and remedy: %#v", check)
			}
		}
	})
}

func TestStatusHumanUsesShapedSummary(t *testing.T) {
	db := seedArchive(t, 1, time.Now().Add(-time.Hour))
	stdout, stderr, err := runCLI(t, "--db", db, "status")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	conformance.AssertHumanOutput(t, stdout)
	for _, disallowed := range []string{"db_path:", "last_import_at:", "oldest_message:", "unread_chats:"} {
		if strings.Contains(stdout, disallowed) {
			t.Fatalf("status leaked raw key %q:\n%s", disallowed, stdout)
		}
	}
	for _, want := range []string{"Status: ok", "archive is fresh", "Archive:", "Messages:", "Chats:", "Auth:", "Freshness:"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status missing %q:\n%s", want, stdout)
		}
	}
}

func TestDoctorHumanUsesChecksList(t *testing.T) {
	source := readableTelegramSource(t)
	db := seedArchive(t, 1, time.Now())
	stdout, stderr, err := runCLI(t, "--db", db, "doctor", "--path", source)
	if err != nil {
		t.Fatalf("doctor: %v stderr=%s", err, stderr)
	}
	conformance.AssertHumanOutput(t, stdout)
	for _, disallowed := range []string{"path:", "sqlite_files:", "tdesktop_files:", "files_scanned:"} {
		if strings.Contains(stdout, disallowed) {
			t.Fatalf("doctor leaked raw key %q:\n%s", disallowed, stdout)
		}
	}
	for _, want := range []string{"Doctor checks:", "source store: ok", "archive: ok"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("doctor missing %q:\n%s", want, stdout)
		}
	}
}

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

	t.Run("clamped limit", func(t *testing.T) {
		db := seedSearchArchive(t, 205)
		payload := runSearchJSON(t, db, "search", "--limit", "500", "launch", "--json")
		if len(payload.Results) != 200 || payload.TotalMatches != 205 || !payload.Truncated {
			t.Fatalf("search payload = %#v", payload)
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
	for _, want := range []string{"Who", "Last seen", "Messages", "Identifiers", "Alice Example", "@alice_example"} {
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
	if stdout != "" {
		t.Fatalf("blank --who stdout = %q, want empty", stdout)
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
		"Who",
		"Last seen",
		"Messages",
		"Identifiers",
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
	if payload.Error.Code != "unknown_who" || payload.Error.DidYouMean == nil || len(payload.Error.DidYouMean) != 0 || !strings.Contains(payload.Error.Hint, "Search without --who") {
		t.Fatalf("unknown error = %#v", payload)
	}
	var errorBody map[string]json.RawMessage
	if err := json.Unmarshal(raw["error"], &errorBody); err != nil {
		t.Fatalf("error body = %s err=%v", raw["error"], err)
	}
	if _, ok := errorBody["did_you_mean"]; !ok {
		t.Fatalf("unknown error omitted did_you_mean: %s", stdout)
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
	if stdout != "" || stderr != "" {
		t.Fatalf("blank search stdout=%q stderr=%q, want quiet streams", stdout, stderr)
	}
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
		if result.Where != "Recipient Person" {
			t.Fatalf("result outside recipient chat = %#v", result)
		}
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
	if !strings.Contains(stdout, "synthetic launch note") || !strings.Contains(stdout, "showing 2 of 2 matches") {
		t.Fatalf("search text = %s", stdout)
	}
}

func TestOpenJSONRoundTripsSearchRef(t *testing.T) {
	db := seedSearchArchive(t, 25)
	search := runSearchJSON(t, db, "search", "launch", "--json")
	if len(search.Results) == 0 {
		t.Fatal("search returned no refs")
	}
	payload := runOpenJSON(t, db, search.Results[0].Ref)
	if payload.Ref != search.Results[0].Ref || payload.Message.Ref != search.Results[0].Ref || !payload.Message.IsTarget {
		t.Fatalf("open target = %#v, want %s", payload, search.Results[0].Ref)
	}
	if payload.Chat.Name != "example chat" || payload.Message.Chat.Name != "example chat" {
		t.Fatalf("chat names = root %q message %q", payload.Chat.Name, payload.Message.Chat.Name)
	}
	if payload.Message.Sender.DisplayName != "Example Sender" || payload.Message.Text == "" {
		t.Fatalf("message = %#v", payload.Message)
	}
	if _, err := time.Parse(time.RFC3339, payload.Message.Time); err != nil {
		t.Fatalf("message time = %q err=%v", payload.Message.Time, err)
	}
	if payload.TargetPosition < 0 || payload.TargetPosition >= len(payload.Context) || !payload.Context[payload.TargetPosition].IsTarget {
		t.Fatalf("target position = %d context = %#v", payload.TargetPosition, payload.Context)
	}
	for _, message := range payload.Context {
		if message.Chat.Name != "example chat" || message.Sender.DisplayName == "" {
			t.Fatalf("context message = %#v", message)
		}
		if _, err := time.Parse(time.RFC3339, message.Time); err != nil {
			t.Fatalf("context time = %q err=%v", message.Time, err)
		}
	}
}

func TestOpenAcceptsShortRefAlias(t *testing.T) {
	db := seedSearchArchive(t, 3)
	stdout, stderr, err := runCLI(t, "--db", db, "search", "launch")
	if err != nil {
		t.Fatalf("search text: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	conformance.AssertHumanOutput(t, stdout)
	alias := firstSearchAlias(t, stdout)
	payload := runOpenJSON(t, db, alias)
	if payload.Ref == "" || !strings.HasPrefix(payload.Ref, "telecrawl:msg/") {
		t.Fatalf("open by alias payload = %#v", payload)
	}
}

func TestOpenShortRefErrorsAreContractCodes(t *testing.T) {
	db := seedSearchArchive(t, 1)
	stdout, _, err := runCLI(t, "--db", db, "open", "22222", "--json")
	if err == nil {
		t.Fatalf("unknown alias succeeded: stdout=%s", stdout)
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "unknown_short_ref" {
		t.Fatalf("error code = %q, want unknown_short_ref", payload.Error.Code)
	}
}

func TestOpenRejectsForeignRefWithContractError(t *testing.T) {
	db := seedSearchArchive(t, 1)
	stdout, stderr, err := runCLI(t, "--db", db, "open", "othercrawl:msg/1", "--json")
	if err == nil {
		t.Fatalf("open foreign ref succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if code := ExitCode(err); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Remedy  string `json:"remedy"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("error json = %s err=%v", stdout, err)
	}
	if payload.Error.Code != "invalid_ref" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error payload = %#v", payload)
	}
}

func TestOpenContextWindowIsBounded(t *testing.T) {
	db := seedSearchArchive(t, 35)
	payload := runOpenJSON(t, db, "telecrawl:msg/18")
	if len(payload.Context) != 21 {
		t.Fatalf("context messages = %d, want 21", len(payload.Context))
	}
	if payload.ContextWindow.Before != 10 || payload.ContextWindow.After != 10 {
		t.Fatalf("context window = %#v", payload.ContextWindow)
	}
	if !payload.ContextWindow.BeforeTruncated || !payload.ContextWindow.AfterTruncated {
		t.Fatalf("context truncation = %#v", payload.ContextWindow)
	}
	if payload.Context[0].Ref != "telecrawl:msg/8" || payload.Context[20].Ref != "telecrawl:msg/28" {
		t.Fatalf("context refs = first %s last %s", payload.Context[0].Ref, payload.Context[20].Ref)
	}
}

func TestContractTimestampsUseLocalOffset(t *testing.T) {
	loc := useFixedLocalZone(t)

	statusTime := time.Now().Add(-time.Hour).UTC()
	statusDB := seedArchive(t, 1, statusTime)
	wantStatusTime := statusTime.In(loc).Format(time.RFC3339)
	status := runStatusJSON(t, statusDB)
	if status.Freshness.LastSync != wantStatusTime {
		t.Fatalf("status last_sync = %q, want %q", status.Freshness.LastSync, wantStatusTime)
	}
	statusText, stderr, err := runCLI(t, "--db", statusDB, "status")
	if err != nil {
		t.Fatalf("status text: %v stderr=%s", err, stderr)
	}
	assertContainsLocalTime(t, statusText, wantStatusTime, statusTime)

	db := seedSearchArchive(t, 1)
	messageTime := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	wantMessageTime := messageTime.In(loc).Format(time.RFC3339)

	search := runSearchJSON(t, db, "search", "launch", "--json")
	if len(search.Results) != 1 || search.Results[0].Time != wantMessageTime {
		t.Fatalf("search result time = %#v, want %q", search.Results, wantMessageTime)
	}
	searchText, stderr, err := runCLI(t, "--db", db, "search", "launch")
	if err != nil {
		t.Fatalf("search text: %v stderr=%s", err, stderr)
	}
	assertContainsLocalTime(t, searchText, wantMessageTime, messageTime)

	open := runOpenJSON(t, db, "telecrawl:msg/1")
	if open.Message.Time != wantMessageTime || len(open.Context) != 1 || open.Context[0].Time != wantMessageTime {
		t.Fatalf("open times = message %q context %#v, want %q", open.Message.Time, open.Context, wantMessageTime)
	}
	openText, stderr, err := runCLI(t, "--db", db, "open", "telecrawl:msg/1")
	if err != nil {
		t.Fatalf("open text: %v stderr=%s", err, stderr)
	}
	assertContainsLocalTime(t, openText, wantMessageTime, messageTime)

	whoText, stderr, err := runCLI(t, "--db", db, "who", "Example Sender")
	if err != nil {
		t.Fatalf("who text: %v stderr=%s", err, stderr)
	}
	assertContainsLocalTime(t, whoText, wantMessageTime, messageTime)
}

func TestStatusSinceYearUsesLocalOffset(t *testing.T) {
	loc := useFixedLocalZone(t)
	messageTime := time.Date(2020, 12, 31, 23, 30, 0, 0, time.UTC)
	db := seedArchiveWithMessageTime(t, 1, time.Now(), messageTime)
	status := runStatusJSON(t, db)
	if got := statusCountValue(t, status, "since"); got != int64(messageTime.In(loc).Year()) {
		t.Fatalf("since count = %d, want local year %d", got, messageTime.In(loc).Year())
	}
}

func TestPerVerbHelpExitsZero(t *testing.T) {
	tests := [][]string{
		{"metadata", "--help"},
		{"doctor", "--help"},
		{"import", "--help"},
		{"sync", "--help"},
		{"status", "--help"},
		{"folders", "--help"},
		{"contacts", "--help"},
		{"contacts", "export", "--help"},
		{"chats", "--help"},
		{"topics", "--help"},
		{"messages", "--help"},
		{"search", "--help"},
		{"who", "--help"},
		{"open", "--help"},
		{"backup", "--help"},
		{"backup", "init", "--help"},
		{"backup", "push", "--help"},
		{"backup", "pull", "--help"},
		{"backup", "status", "--help"},
		{"backup", "snapshots", "--help"},
		{"version", "--help"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, stderr, err := runCLI(t, args...)
			if err != nil {
				t.Fatalf("%v: err=%v stderr=%s stdout=%s", args, err, stderr, stdout)
			}
			if stderr != "" {
				t.Fatalf("%v: stderr=%q", args, stderr)
			}
			if !strings.Contains(stdout, "usage: telecrawl") {
				t.Fatalf("%v: help missing usage:\n%s", args, stdout)
			}
		})
	}
}

type statusJSON struct {
	AppID     string `json:"app_id"`
	State     string `json:"state"`
	Summary   string `json:"summary"`
	Freshness struct {
		LastSync string `json:"last_sync"`
	} `json:"freshness"`
	Counts []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Value int64  `json:"value"`
	} `json:"counts"`
	Auth struct {
		Authorized bool    `json:"authorized"`
		Expires    *string `json:"expires"`
	} `json:"auth"`
}

type doctorCheckJSON struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

type testLogEvent struct {
	WhatHappened string `json:"what_happened"`
	When         string `json:"when"`
	Remedy       string `json:"remedy"`
}

type searchJSON struct {
	Query       string           `json:"query"`
	WhoResolved *testWhoResolved `json:"who_resolved"`
	Results     []struct {
		Ref     string `json:"ref"`
		Time    string `json:"time"`
		Who     string `json:"who"`
		Where   string `json:"where"`
		Snippet string `json:"snippet"`
	} `json:"results"`
	TotalMatches int  `json:"total_matches"`
	Truncated    bool `json:"truncated"`
}

type testWhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type whoJSON struct {
	Query      string             `json:"query"`
	Candidates []testWhoCandidate `json:"candidates"`
}

type testWhoCandidate struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int      `json:"messages"`
}

type openJSON struct {
	Ref  string `json:"ref"`
	Chat struct {
		Ref  string `json:"ref"`
		Name string `json:"name"`
	} `json:"chat"`
	Message       openMessageJSON   `json:"message"`
	Context       []openMessageJSON `json:"context"`
	ContextWindow struct {
		Before          int  `json:"before"`
		After           int  `json:"after"`
		BeforeTruncated bool `json:"before_truncated"`
		AfterTruncated  bool `json:"after_truncated"`
	} `json:"context_window"`
	TargetPosition int `json:"target_position"`
}

type openMessageJSON struct {
	Ref      string `json:"ref"`
	IsTarget bool   `json:"is_target"`
	Time     string `json:"time"`
	Chat     struct {
		Ref  string `json:"ref"`
		Name string `json:"name"`
	} `json:"chat"`
	Sender struct {
		Ref         string `json:"ref"`
		DisplayName string `json:"display_name"`
	} `json:"sender"`
	Text string `json:"text"`
}

func runCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	if !hasArg(args, "--db") {
		args = append([]string{"--db", filepath.Join(t.TempDir(), "telecrawl.db")}, args...)
	}
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func useFixedLocalZone(t *testing.T) *time.Location {
	t.Helper()
	loc := time.FixedZone("test-local", 2*60*60)
	previous := time.Local
	time.Local = loc
	t.Cleanup(func() {
		time.Local = previous
	})
	return loc
}

func assertContainsLocalTime(t *testing.T, output, want string, utc time.Time) {
	t.Helper()
	if !strings.Contains(output, want) {
		t.Fatalf("output missing local time %q:\n%s", want, output)
	}
	if utcText := utc.UTC().Format(time.RFC3339); strings.Contains(output, utcText) {
		t.Fatalf("output contains UTC time %q:\n%s", utcText, output)
	}
}

func hasArg(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func runStatusJSON(t *testing.T, db string) statusJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, "--db", db, "status", "--json")
	if err != nil {
		t.Fatalf("status: %v stderr=%s", err, stderr)
	}
	var status statusJSON
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatalf("status json = %s err=%v", stdout, err)
	}
	return status
}

func assertStatusState(t *testing.T, status statusJSON, state string) {
	t.Helper()
	if status.AppID != "telecrawl" || status.State != state || status.Summary == "" || !status.Auth.Authorized || status.Auth.Expires != nil {
		t.Fatalf("status = %#v, want state %q", status, state)
	}
	if len(status.Counts) != 3 {
		t.Fatalf("counts = %#v, want 3", status.Counts)
	}
	want := []string{"messages", "chats", "since"}
	for i, count := range status.Counts {
		if count.ID != want[i] || count.Label != want[i] {
			t.Fatalf("counts = %#v, want ids %v", status.Counts, want)
		}
	}
}

func statusCountValue(t *testing.T, status statusJSON, id string) int64 {
	t.Helper()
	for _, count := range status.Counts {
		if count.ID == id {
			return count.Value
		}
	}
	t.Fatalf("counts = %#v, missing %q", status.Counts, id)
	return 0
}

func decodeDoctorChecks(t *testing.T, stdout string) []doctorCheckJSON {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout, err)
	}
	if _, ok := root["checks"]; !ok {
		t.Fatalf("doctor keys = %#v, want checks", root)
	}
	if len(root) > 2 {
		t.Fatalf("doctor keys = %#v, want checks and optional log", root)
	}
	var payload struct {
		Checks []doctorCheckJSON `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("doctor json = %s err=%v", stdout, err)
	}
	return payload.Checks
}

func runSearchJSON(t *testing.T, db string, args ...string) searchJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, append([]string{"--db", db}, args...)...)
	if err != nil {
		t.Fatalf("search: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	conformance.AssertSearchEnvelope(t, []byte(stdout))
	if strings.Contains(stdout, "who_matched") {
		t.Fatalf("search json contains removed who_matched field: %s", stdout)
	}
	var payload searchJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	return payload
}

func runWhoJSON(t *testing.T, db string, args ...string) whoJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, append([]string{"--db", db, "who"}, args...)...)
	if err != nil {
		t.Fatalf("who: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	assertWhoEnvelopeShape(t, stdout)
	var payload whoJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("who json = %s err=%v", stdout, err)
	}
	return payload
}

func assertWhoEnvelopeShape(t *testing.T, stdout string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("who json = %s err=%v", stdout, err)
	}
	wantRoot := []string{"query", "candidates"}
	if len(root) != len(wantRoot) {
		t.Fatalf("who json keys = %#v, want %v", root, wantRoot)
	}
	for _, key := range wantRoot {
		if _, ok := root[key]; !ok {
			t.Fatalf("who json missing key %q: %#v", key, root)
		}
	}
	var candidates []map[string]json.RawMessage
	if err := json.Unmarshal(root["candidates"], &candidates); err != nil {
		t.Fatalf("who candidates json = %s err=%v", root["candidates"], err)
	}
	wantCandidate := []string{"who", "identifiers", "last_seen", "messages"}
	for _, candidate := range candidates {
		if len(candidate) != len(wantCandidate) {
			t.Fatalf("who candidate keys = %#v, want %v", candidate, wantCandidate)
		}
		for _, key := range wantCandidate {
			if _, ok := candidate[key]; !ok {
				t.Fatalf("who candidate missing key %q: %#v", key, candidate)
			}
		}
	}
}

func assertWhoResolved(t *testing.T, resolved *testWhoResolved, wantWho, wantIdentifier string) {
	t.Helper()
	if resolved == nil {
		t.Fatalf("who_resolved missing, want %q", wantWho)
	}
	if resolved.Who != wantWho || !hasString(resolved.Identifiers, wantIdentifier) {
		t.Fatalf("who_resolved = %#v, want %q with %q", resolved, wantWho, wantIdentifier)
	}
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func runOpenJSON(t *testing.T, db string, ref string) openJSON {
	t.Helper()
	stdout, stderr, err := runCLI(t, "--db", db, "open", ref, "--json")
	if err != nil {
		t.Fatalf("open: %v stderr=%s stdout=%s", err, stderr, stdout)
	}
	var payload openJSON
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("open json = %s err=%v", stdout, err)
	}
	return payload
}

func assertSearchResultShape(t *testing.T, result struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}) {
	t.Helper()
	if !strings.HasPrefix(result.Ref, "telecrawl:msg/") || result.Who == "" || result.Where == "" || result.Snippet == "" {
		t.Fatalf("bad search result = %#v", result)
	}
	if strings.ContainsAny(result.Who+result.Where+result.Snippet, "\n\t") || strings.ContainsAny(result.Snippet, "[]") || strings.Contains(result.Snippet, "...") {
		t.Fatalf("search result kept marker or multiline fields = %#v", result)
	}
	if _, err := time.Parse(time.RFC3339, result.Time); err != nil {
		t.Fatalf("search result time = %q err=%v", result.Time, err)
	}
}

func firstSearchAlias(t *testing.T, stdout string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if !strings.HasPrefix(line, "ref: ") {
			continue
		}
		ref := strings.TrimSpace(strings.TrimPrefix(line, "ref: "))
		alias, _, _ := strings.Cut(ref, " ")
		if alias == "" || strings.HasPrefix(alias, "telecrawl:") {
			t.Fatalf("search ref line did not include alias: %q", line)
		}
		return alias
	}
	t.Fatalf("search output had no ref line:\n%s", stdout)
	return ""
}

func seedArchive(t *testing.T, messages int, finishedAt time.Time) string {
	t.Helper()
	return seedArchiveWithMessageTime(t, messages, finishedAt, time.Date(2020, 1, 2, 12, 0, 0, 0, time.UTC))
}

func seedArchiveWithMessageTime(t *testing.T, messages int, finishedAt, messageTime time.Time) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	var chats []store.Chat
	var rows []store.Message
	if messages > 0 {
		chats = []store.Chat{{JID: "100", Kind: "chat", Name: "example chat", LastMessageAt: messageTime, MessageCount: messages}}
		for i := 0; i < messages; i++ {
			rows = append(rows, store.Message{
				SourcePK:   int64(i + 1),
				ChatJID:    "100",
				ChatName:   "example chat",
				MessageID:  fmt.Sprintf("0:%d", i+1),
				SenderJID:  "200",
				SenderName: "Example Sender",
				Timestamp:  messageTime.Add(time.Duration(i) * time.Minute),
				Text:       "synthetic launch note",
			})
		}
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: finishedAt, FinishedAt: finishedAt}, nil, chats, nil, nil, nil, nil, rows); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedDirectSenderArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{{JID: "300", FullName: "Direct Person"}}
	chats := []store.Chat{{JID: "300", Kind: "chat", Name: "Direct Person", LastMessageAt: now, MessageCount: 1}}
	messages := []store.Message{{
		SourcePK:  1,
		ChatJID:   "300",
		ChatName:  "Direct Person",
		MessageID: "0:1",
		SenderJID: "300",
		Timestamp: now,
		Text:      "direct sender needle",
	}}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.RebuildShortRefs(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSearchArchive(t *testing.T, count int) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	chats := []store.Chat{{JID: "100", Kind: "chat", Name: "example chat", LastMessageAt: now, MessageCount: count}}
	messages := make([]store.Message, 0, count)
	for i := 0; i < count; i++ {
		messages = append(messages, store.Message{
			SourcePK:   int64(i + 1),
			ChatJID:    "100",
			ChatName:   "example chat",
			MessageID:  fmt.Sprintf("0:%d", i+1),
			SenderJID:  "200",
			SenderName: "Example Sender",
			Timestamp:  now.Add(time.Duration(i) * time.Minute),
			Text:       fmt.Sprintf("synthetic launch note %03d", i+1),
		})
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, nil, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedWhoSearchArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "200", Phone: "+1555200200", FullName: "Alice Example", Username: "alice_example"},
		{JID: "300", FullName: "Recipient Person"},
		{JID: "400", FullName: "Jordan Example", Username: "jordan_lower"},
		{JID: "401", FullName: "JORDAN EXAMPLE", Username: "jordan_upper"},
	}
	chats := []store.Chat{
		{JID: "100", Kind: "chat", Name: "example chat", LastMessageAt: now.Add(4 * time.Minute), MessageCount: 4},
		{JID: "300", Kind: "chat", Name: "Recipient Person", LastMessageAt: now.Add(6 * time.Minute), MessageCount: 2},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "example chat", MessageID: "0:1", SenderJID: "200", SenderName: "Alice Example", Timestamp: now, Text: "needle from alice"},
		{SourcePK: 2, ChatJID: "100", ChatName: "example chat", MessageID: "0:2", SenderJID: "201", SenderName: "Other Person", Timestamp: now.Add(time.Minute), Text: "needle from other"},
		{SourcePK: 3, ChatJID: "100", ChatName: "example chat", MessageID: "0:3", SenderJID: "400", SenderName: "Jordan Example", Timestamp: now.Add(2 * time.Minute), Text: "needle from jordan lower"},
		{SourcePK: 4, ChatJID: "100", ChatName: "example chat", MessageID: "0:4", SenderJID: "401", SenderName: "JORDAN EXAMPLE", Timestamp: now.Add(3 * time.Minute), Text: "needle from jordan upper"},
		{SourcePK: 5, ChatJID: "300", ChatName: "Recipient Person", MessageID: "0:5", SenderJID: "999", SenderName: "Archive Owner", Timestamp: now.Add(5 * time.Minute), FromMe: true, Text: "needle to recipient"},
		{SourcePK: 6, ChatJID: "300", ChatName: "Recipient Person", MessageID: "0:6", SenderJID: "300", SenderName: "Recipient Person", Timestamp: now.Add(6 * time.Minute), Text: "needle from recipient"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedWhoResolverDefectArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "200", PeerType: "user", FullName: "Jef Example"},
		{JID: "-1001", PeerType: "group", FullName: "Jefs bachelor drive"},
		{JID: "-1002", PeerType: "group", FullName: "Presents for Tini and Sjefke"},
	}
	chats := []store.Chat{
		{JID: "-1001", Kind: "group", Name: "Jefs bachelor drive", LastMessageAt: now.Add(time.Minute), MessageCount: 2},
		{JID: "-1002", Kind: "group", Name: "Presents for Tini and Sjefke", LastMessageAt: now.Add(2 * time.Minute), MessageCount: 1},
	}
	participants := []store.GroupParticipant{
		{GroupJID: "-1001", UserJID: "200", ContactName: "Jef Example", FirstName: "Jef", IsActive: true},
		{GroupJID: "-1001", UserJID: "165355235", IsActive: true},
		{GroupJID: "-1002", UserJID: "700", ContactName: "Other Person", FirstName: "Other", IsActive: true},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "-1001", ChatName: "Jefs bachelor drive", MessageID: "0:1", SenderJID: "200", SenderName: "Jef Example", Timestamp: now, Text: "jef group message"},
		{SourcePK: 2, ChatJID: "-1001", ChatName: "Jefs bachelor drive", MessageID: "0:2", SenderJID: "165355235", SenderName: "165355235", Timestamp: now.Add(time.Minute), Text: "numeric group message"},
		{SourcePK: 3, ChatJID: "-1002", ChatName: "Presents for Tini and Sjefke", MessageID: "0:3", SenderJID: "700", SenderName: "Other Person", Timestamp: now.Add(2 * time.Minute), Text: "other group message"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, participants, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSearchWhoArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "87092563", Username: "fixture_user"},
		{JID: "87092564", FirstName: "Ada"},
		{JID: "87092565", FirstName: "+15551234567"},
	}
	chats := []store.Chat{{JID: "100", Kind: "chat", Name: "example chat", LastMessageAt: now, MessageCount: 3}}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "example chat", MessageID: "0:1", SenderJID: "87092563", SenderName: "87092563", Timestamp: now, Text: "username-fallback needle"},
		{SourcePK: 2, ChatJID: "100", ChatName: "example chat", MessageID: "0:2", SenderJID: "87092564", SenderName: "", Timestamp: now.Add(time.Minute), Text: "firstname-fallback needle"},
		{SourcePK: 3, ChatJID: "100", ChatName: "example chat", MessageID: "0:3", SenderJID: "87092565", SenderName: "87092565", Timestamp: now.Add(2 * time.Minute), Text: "no-human-fallback needle"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func readableTelegramSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".tempkeyEncrypted"), []byte("synthetic-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "account-1", "postbox", "db", "db_sqlite")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, []byte("SQLite format 3\x00synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
