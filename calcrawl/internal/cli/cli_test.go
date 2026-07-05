package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/cli"
)

func TestSyncImportsCalendarStore(t *testing.T) {
	setupCalendarFixture(t)
	first := runSync(t)
	if first.Events != 2 || first.Calendars != 2 || first.NewEvents != 2 || first.ChangedEvents != 0 {
		t.Fatalf("first sync = %#v, want 2 events, 2 calendars, 2 new", first)
	}
	second := runSync(t)
	if second.NewEvents != 0 || second.ChangedEvents != 0 || second.UnchangedEvents != 2 {
		t.Fatalf("second sync = %#v, want idempotent unchanged events", second)
	}
	status := runJSON[map[string]any](t, "status", "--json")
	if got := status["state"]; got != "ok" {
		t.Fatalf("status state = %v, want ok", got)
	}
	if _, ok := status["database_path"]; ok {
		t.Fatalf("status leaked database_path: %#v", status)
	}
	if _, ok := status["databases"]; ok {
		t.Fatalf("status leaked databases: %#v", status)
	}
	freshness, ok := status["freshness"].(map[string]any)
	if !ok || freshness["last_sync"] == "" {
		t.Fatalf("freshness = %#v, want last_sync", status["freshness"])
	}
	if _, ok := status["last_sync_at"]; ok {
		t.Fatalf("status leaked top-level last_sync_at: %#v", status)
	}
	for _, oldKey := range []string{"status", "age_seconds", "stale_after_seconds"} {
		if _, ok := freshness[oldKey]; ok {
			t.Fatalf("freshness leaked %q: %#v", oldKey, freshness)
		}
	}
	counts := countValues(status["counts"])
	if counts["events"] != 2 || counts["calendars"] != 2 || counts["since"] != 2026 {
		t.Fatalf("counts = %#v, want events=2 calendars=2 since=2026", counts)
	}
}

func TestMetadataDeclaresShortRefsAndWho(t *testing.T) {
	setupCalendarFixture(t)
	manifest := runJSON[struct {
		Capabilities []string      `json:"capabilities"`
		Paths        control.Paths `json:"paths"`
		Commands     map[string]struct {
			Argv []string `json:"argv"`
		} `json:"commands"`
	}](t, "metadata", "--json")
	for _, want := range []string{"who", "short_refs", "verbose_logs"} {
		if !hasString(manifest.Capabilities, want) {
			t.Fatalf("capabilities = %#v, want %q", manifest.Capabilities, want)
		}
	}
	if want := filepath.Join(os.Getenv("HOME"), ".opentrawl", "calcrawl", "logs"); manifest.Paths.DefaultLogs != want {
		t.Fatalf("default logs = %q, want %q", manifest.Paths.DefaultLogs, want)
	}
	if command, ok := manifest.Commands["who"]; !ok || strings.Join(command.Argv, " ") != "calcrawl who NAME --json" {
		t.Fatalf("who command = %#v, want documented resolver", manifest.Commands["who"])
	}
	help := runOK(t, "help")
	if !strings.Contains(help, "who") || !strings.Contains(help, "Resolve a person") {
		t.Fatalf("top-level help does not mention who:\n%s", help)
	}
	searchHelp := runOK(t, "help", "search")
	if !strings.Contains(searchHelp, "Use calcrawl who NAME") {
		t.Fatalf("search help does not mention resolver:\n%s", searchHelp)
	}
}

func TestVerboseLogsWriteFileAndStreamToStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	logPath := filepath.Join(home, ".opentrawl", "calcrawl", "logs", "calcrawl.log")

	stdout, stderr, err := run(t, "metadata")
	if err != nil {
		t.Fatalf("metadata error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("metadata without -v wrote stderr:\n%s", stderr)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file missing at %s: %v", logPath, err)
	}
	logText := readTestLog(t)
	for _, want := range []string{"metadata start:", "metadata finish: outcome=success"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}

	stdout, stderr, err = run(t, "-v", "metadata")
	if err != nil {
		t.Fatalf("metadata -v error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if !strings.Contains(stderr, "metadata start:") || !strings.Contains(stderr, "metadata finish: outcome=success") {
		t.Fatalf("-v stderr missing log lines:\n%s", stderr)
	}
	if strings.Contains(stderr, "DEBUG") {
		t.Fatalf("-v streamed debug line:\n%s", stderr)
	}
}

func TestSyncVerboseLogsPhaseTimings(t *testing.T) {
	setupCalendarFixture(t)

	stdout, stderr, err := run(t, "-vv", "sync", "--json")
	if err != nil {
		t.Fatalf("sync -vv error = %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	logText := readTestLog(t)
	for _, want := range []string{
		"sync_done: calendars=2",
		"events=2",
		"new=2",
		"sync_phase: source=calendar_store",
		"read_ms=",
		"write_ms=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("sync log missing %q:\n%s", want, logText)
		}
	}
	if !strings.Contains(stderr, "sync_done: calendars=2") || !strings.Contains(stderr, "sync_phase: source=calendar_store") {
		t.Fatalf("-vv stderr missing sync log lines:\n%s", stderr)
	}
}

func TestHelpDocumentsDiagnosticsLine(t *testing.T) {
	setupTestHome(t)
	diagnostics := "Diagnostics: run with -v, or read ~/.opentrawl/calcrawl/logs/calcrawl.log"
	for _, args := range [][]string{
		{"help"},
		{"help", "metadata"},
		{"help", "status"},
		{"help", "sync"},
		{"help", "search"},
		{"help", "who"},
		{"help", "open"},
		{"help", "doctor"},
		{"help", "contacts", "export"},
		{"metadata", "--help"},
		{"status", "--help"},
		{"sync", "--help"},
		{"search", "--help"},
		{"who", "--help"},
		{"open", "--help"},
		{"doctor", "--help"},
		{"contacts", "--help"},
		{"contacts", "export", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout := runOK(t, args...)
			if !strings.Contains(stdout, diagnostics) {
				t.Fatalf("help missing diagnostics line:\n%s", stdout)
			}
			if !strings.HasSuffix(strings.TrimSpace(stdout), diagnostics) {
				t.Fatalf("help does not end with diagnostics line:\n%s", stdout)
			}
		})
	}
}

func TestCoreDataTimesProvenanceSearchAndOpen(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	search := runJSON[searchResponse](t, "search", "planning", "--json", "--limit", "1")
	if search.Query != "planning" || search.TotalMatches != 1 || search.Truncated {
		t.Fatalf("search envelope = %#v", search)
	}
	result := search.Results[0]
	if result.Ref != "calcrawl:event/11111111-1111-1111-1111-111111111111" {
		t.Fatalf("ref = %q", result.Ref)
	}
	if result.Time != "2026-03-04T10:00:00+01:00" {
		t.Fatalf("time = %q, want timezone-rendered RFC3339", result.Time)
	}
	if result.Who != "Alice Example" || result.Where != "Room 1" {
		t.Fatalf("who/where = %q/%q", result.Who, result.Where)
	}
	if !strings.Contains(result.Snippet, "Planning meeting") || !strings.Contains(result.Snippet, "Room 1") {
		t.Fatalf("snippet = %q", result.Snippet)
	}
	if result.ShortRef == "" {
		t.Fatalf("search JSON missing short_ref: %#v", result)
	}

	opened := runJSON[archive.EventDetail](t, "open", result.Ref, "--json")
	if opened.Calendar != "Work" || opened.Account != "iCloud" {
		t.Fatalf("provenance = %#v %#v", opened.Calendar, opened.Account)
	}
	if opened.Location == nil || opened.Location.Address != "1 Example Street" {
		t.Fatalf("location = %#v", opened.Location)
	}
	if len(opened.Attendees) != 2 || opened.Attendees[1].RSVPStatus != "tentative" {
		t.Fatalf("attendees = %#v", opened.Attendees)
	}
	if !opened.HasRecurrences || opened.URL != "https://example.com/event" {
		t.Fatalf("recurrence/url = %v %q", opened.HasRecurrences, opened.URL)
	}

	allDay := runJSON[searchResponse](t, "search", "holiday", "--json")
	if allDay.Results[0].Time != "2026-05-05T00:00:00+02:00" || allDay.Results[0].Who == "" {
		t.Fatalf("all-day search result = %#v", allDay.Results[0])
	}
	// TRAWL-104 tripwire: search JSON carries all_day so the federated
	// view can render the bare date too.
	if !allDay.Results[0].AllDay {
		t.Fatalf("all-day search result missing all_day: %#v", allDay.Results[0])
	}
	if search.Results[0].AllDay {
		t.Fatalf("timed search result must not be all_day: %#v", search.Results[0])
	}
	allDayOpen := runJSON[archive.EventDetail](t, "open", allDay.Results[0].Ref, "--json")
	if allDayOpen.Start != "2026-05-05T00:00:00+02:00" || allDayOpen.End != "2026-05-06T00:00:00+02:00" || !allDayOpen.AllDay {
		t.Fatalf("all-day event = %#v", allDayOpen)
	}
	if allDayOpen.Status != "confirmed" {
		t.Fatalf("all-day status = %q, want confirmed", allDayOpen.Status)
	}
	allDayText, _, err := run(t, "open", allDay.Results[0].Ref)
	if err != nil {
		t.Fatalf("open all-day text: %v\n%s", err, allDayText)
	}
	if strings.Contains(allDayText, "status_0") || !strings.Contains(allDayText, "Status: confirmed") {
		t.Fatalf("all-day text leaked status enum:\n%s", allDayText)
	}
	// TRAWL-100 tripwire: an all-day event lists as a bare date, never a
	// fake midnight.
	allDaySearchText := runOK(t, "search", "holiday")
	if !strings.Contains(allDaySearchText, "2026-05-05") || strings.Contains(allDaySearchText, "2026-05-05 00:00") {
		t.Fatalf("all-day search row must show a bare date:\n%s", allDaySearchText)
	}

	st, err := archive.OpenExistingWritable(context.Background(), archive.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, st.DB(), `update events set start_time = '2021-08-26', end_time = '2021-08-27' where event_uid = '22222222-2222-2222-2222-222222222222'`)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	legacyAllDay := runJSON[searchResponse](t, "search", "holiday", "--json")
	if legacyAllDay.Results[0].Time == "2021-08-26" || !strings.Contains(legacyAllDay.Results[0].Time, "T00:00:00") {
		t.Fatalf("legacy all-day search time = %q, want RFC3339 midnight", legacyAllDay.Results[0].Time)
	}
}

func TestOpenNormalizesStoredStatusEnums(t *testing.T) {
	for _, tc := range []struct {
		stored string
		want   string
	}{
		{stored: "status_0", want: "confirmed"},
		{stored: "status_1", want: "confirmed"},
		{stored: "status_2", want: "tentative"},
		{stored: "status_3", want: "cancelled"},
		{stored: "status_99", want: "unknown"},
	} {
		t.Run(tc.stored, func(t *testing.T) {
			setupCalendarFixture(t)
			runSync(t)
			ref := "calcrawl:event/11111111-1111-1111-1111-111111111111"
			st, err := archive.OpenExistingWritable(context.Background(), archive.DefaultPath())
			if err != nil {
				t.Fatal(err)
			}
			mustExec(t, st.DB(), `update events set status = ? where event_uid = '11111111-1111-1111-1111-111111111111'`, tc.stored)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}

			opened := runJSON[archive.EventDetail](t, "open", ref, "--json")
			if opened.Status != tc.want {
				t.Fatalf("open JSON status = %q, want %q", opened.Status, tc.want)
			}
			text, _, err := run(t, "open", ref)
			if err != nil {
				t.Fatalf("open text: %v\n%s", err, text)
			}
			if strings.Contains(text, tc.stored) || !strings.Contains(text, "Status: "+tc.want) {
				t.Fatalf("open text leaked status enum, want %q and no %q:\n%s", tc.want, tc.stored, text)
			}
		})
	}
}

func TestCLIOutputConformance(t *testing.T) {
	db := setupCalendarFixture(t)

	for _, command := range []struct {
		name string
		args []string
	}{
		{name: "status", args: []string{"status"}},
		{name: "doctor", args: []string{"doctor"}},
	} {
		t.Run(command.name, func(t *testing.T) {
			stdout, stderr, err := run(t, command.args...)
			if err != nil {
				t.Fatalf("calcrawl %v failed: %v\nstdout:\n%s\nstderr:\n%s", command.args, err, stdout, stderr)
			}
			conformance.AssertHumanOutput(t, stdout)
		})
	}

	insertEvent(t, db, eventFixture{
		rowID:       9000,
		uniqueID:    "event-conformance",
		summary:     "Conformance planning",
		description: "Synthetic conformance event.",
		start:       time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		end:         time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC),
		calendarID:  10,
		status:      1,
	})
	runSync(t)
	searchText := runOK(t, "search", "conformance")
	conformance.AssertHumanOutput(t, searchText)
	searchJSON := runOK(t, "search", "conformance", "--json")
	conformance.AssertSearchEnvelope(t, []byte(searchJSON))
	whoText := runOK(t, "who", "alice")
	conformance.AssertHumanOutput(t, whoText)
}

func TestSearchTruncatesLongQueryHeader(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)
	t.Setenv("COLUMNS", "80")
	query := strings.Repeat("q", 200)
	stdout := runOK(t, "search", query, "--limit", "5")
	assertLinesWithinDisplayWidth(t, stdout, 80)
	if !strings.Contains(stdout, "…") {
		t.Fatalf("long query was not display-truncated:\n%s", stdout)
	}
}

func TestWhoResolverDedupesAndMatchesGenerously(t *testing.T) {
	db := setupCalendarFixture(t)
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(503, 'Katja Rossen', 'katja@example.com', 'Katja', 'Rossen'),
		(504, 'katja rossen', 'katja.rossen@example.com', 'Katja', 'Rossen')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1003, 2, 1, 2, 1, 503, 100, 'katja@example.com', '', 0, ''),
		(1004, 2, 1, 2, 1, 504, 101, 'katja.rossen@example.com', '', 0, '')`)
	runSync(t)

	who := runJSON[whoResponse](t, "who", "alce", "--json")
	if who.Query != "alce" || len(who.Candidates) != 1 {
		t.Fatalf("who alce = %#v, want one generous match", who)
	}
	alice := who.Candidates[0]
	if alice.Who != "Alice Example" || alice.Messages != 1 {
		t.Fatalf("alice candidate = %#v, want deduped one-event identity", alice)
	}
	for _, want := range []string{"alice@example.com", "+15550100"} {
		if !hasString(alice.Identifiers, want) {
			t.Fatalf("alice identifiers = %#v, want %q", alice.Identifiers, want)
		}
	}
	if alice.LastSeen != "2026-03-04T10:00:00+01:00" {
		t.Fatalf("alice last_seen = %q", alice.LastSeen)
	}

	holiday := runJSON[whoResponse](t, "who", "bot", "--json")
	if len(holiday.Candidates) != 2 || holiday.Candidates[0].Who != "Holiday Bot" || holiday.Candidates[1].Who != "Bob Example" {
		t.Fatalf("who bot = %#v, want substring match before close-spelling match", holiday)
	}

	katja := runJSON[whoResponse](t, "who", "katja", "--json")
	if len(katja.Candidates) != 1 {
		t.Fatalf("who katja = %#v, want one merged candidate", katja)
	}
	katjaCandidate := katja.Candidates[0]
	if katjaCandidate.Who != "Katja Rossen" || katjaCandidate.Messages != 2 {
		t.Fatalf("katja candidate = %#v, want merged case variant identity", katjaCandidate)
	}
	for _, want := range []string{"katja@example.com", "katja.rossen@example.com"} {
		if !hasString(katjaCandidate.Identifiers, want) {
			t.Fatalf("katja identifiers = %#v, want %q", katjaCandidate.Identifiers, want)
		}
	}
}

func TestWhoTiedCountsNeverDisplayEmailCruft(t *testing.T) {
	// TRAWL-109 regression: with tied spelling counts, the old picker's
	// length-desc tie-break displayed "Ebba Krusenstierna <ebbak@spotify.com>".
	db := setupCalendarFixture(t)
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(505, 'Ebba Krusenstierna', 'ebbak@spotify.com', 'Ebba', 'Krusenstierna'),
		(506, 'Ebba Krusenstierna <ebbak@spotify.com>', 'ebbak@spotify.com', 'Ebba', 'Krusenstierna')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1005, 2, 1, 2, 1, 505, 100, 'ebbak@spotify.com', '', 0, ''),
		(1006, 2, 1, 2, 1, 506, 101, 'ebbak@spotify.com', '', 0, '')`)
	runSync(t)

	who := runJSON[whoResponse](t, "who", "ebbak@spotify.com", "--json")
	if len(who.Candidates) != 1 {
		t.Fatalf("who ebbak@spotify.com = %#v, want one candidate", who)
	}
	if who.Candidates[0].Who != "Ebba Krusenstierna" {
		t.Fatalf("who = %q, want the clean spelling on tied counts", who.Candidates[0].Who)
	}
}

func TestSearchWhoResolvesOnePersonAndFiltersParticipants(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	for _, tc := range []struct {
		args        []string
		resolvedWho string
		identifier  string
	}{
		{args: []string{"search", "planning", "--who", "alice example", "--json"}, resolvedWho: "Alice Example", identifier: "alice@example.com"},
		{args: []string{"search", "planning", "--who", "alice@example.com", "--json"}, resolvedWho: "Alice Example", identifier: "alice@example.com"},
		{args: []string{"search", "planning", "--who", "bob@example.com", "--json"}, resolvedWho: "Bob Example", identifier: "bob@example.com"},
		{args: []string{"search", "--who", " Alice   Example ", "planning", "--json"}, resolvedWho: "Alice Example", identifier: "alice@example.com"},
	} {
		search := runJSON[searchResponse](t, tc.args...)
		if len(search.Results) != 1 || search.TotalMatches != 1 || search.Results[0].Who != "Alice Example" {
			t.Fatalf("calcrawl %v = %#v, want planning match", tc.args, search)
		}
		if search.WhoResolved == nil || search.WhoResolved.Who != tc.resolvedWho || !hasString(search.WhoResolved.Identifiers, tc.identifier) {
			t.Fatalf("who_resolved for %v = %#v", tc.args, search.WhoResolved)
		}
		assertNoWhoMatched(t, tc.args...)
	}
}

func TestSearchWhoAmbiguousDoesNotSearch(t *testing.T) {
	db := setupCalendarFixture(t)
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(503, 'Alice Alternate', 'alice.alt@example.com', 'Alice', 'Alternate')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1003, 2, 1, 2, 1, 503, 101, 'alice.alt@example.com', '', 0, '')`)
	runSync(t)

	stdout, _, err := run(t, "search", "holiday", "--who", "alice", "--json")
	if err == nil || cli.ExitCode(err) != 4 {
		t.Fatalf("ambiguous who err = %v stdout = %s", err, stdout)
	}
	var out errorResponse
	if decodeErr := json.Unmarshal([]byte(stdout), &out); decodeErr != nil {
		t.Fatalf("decode ambiguous error: %v\n%s", decodeErr, stdout)
	}
	if out.Error.Code != "ambiguous_who" || len(out.Error.Candidates) != 2 {
		t.Fatalf("ambiguous error = %#v", out)
	}
	if strings.Contains(stdout, "results") || strings.Contains(stdout, "who_matched") {
		t.Fatalf("ambiguous search returned search fields:\n%s", stdout)
	}

	_, _, err = run(t, "search", "holiday", "--who", "alice")
	if err == nil {
		t.Fatal("ambiguous who text succeeded")
	}
	text := err.Error()
	for _, want := range []string{"--who \"alice\" matched more than one person.", "alice.alt@example.com", "Retry with an identifier: calcrawl search holiday --who alice.alt@example.com"} {
		if !strings.Contains(text, want) {
			t.Fatalf("ambiguous text missing %q:\n%s", want, text)
		}
	}
}

// TRAWL-111: a shared service mailbox (calendar-invite@lu.ma) fronts several
// distinct organizers. They must stay separate candidates, and --who on the
// shared mailbox must surface them instead of silently picking one.
func TestSearchWhoSharedMailboxSurfacesEachName(t *testing.T) {
	db := setupCalendarFixture(t)
	insertEvent(t, db, eventFixture{
		rowID:       110,
		uuid:        "55555555-5555-5555-5555-555555555555",
		uniqueID:    "event-frontier",
		summary:     "SF Show & Tell",
		start:       time.Date(2026, 2, 5, 2, 0, 0, 0, time.UTC),
		end:         time.Date(2026, 2, 5, 4, 0, 0, 0, time.UTC),
		calendarID:  10,
		organizerID: 1010,
		status:      1,
	})
	insertEvent(t, db, eventFixture{
		rowID:       111,
		uuid:        "66666666-6666-6666-6666-666666666666",
		uniqueID:    "event-clawcon",
		summary:     "ClawCon Singapore",
		start:       time.Date(2026, 5, 14, 10, 0, 0, 0, time.UTC),
		end:         time.Date(2026, 5, 14, 18, 0, 0, 0, time.UTC),
		calendarID:  10,
		organizerID: 1011,
		status:      1,
	})
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(510, 'Frontier Tower SF', 'MAILTO:calendar-invite@lu.ma', '', ''),
		(511, 'ClawCon', 'MAILTO:calendar-invite@lu.ma', '', '')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1010, 2, 1, 2, 3, 510, 110, 'calendar-invite@lu.ma', '', 0, ''),
		(1011, 2, 1, 2, 3, 511, 111, 'calendar-invite@lu.ma', '', 0, '')`)
	runSync(t)

	who := runJSON[whoResponse](t, "who", "calendar-invite@lu.ma", "--json")
	if len(who.Candidates) != 2 || who.Candidates[0].Who != "ClawCon" || who.Candidates[1].Who != "Frontier Tower SF" {
		t.Fatalf("who shared mailbox = %#v, want ClawCon and Frontier Tower SF separate", who)
	}
	for _, candidate := range who.Candidates {
		if candidate.Messages != 1 || !hasString(candidate.Identifiers, "calendar-invite@lu.ma") {
			t.Fatalf("shared-mailbox candidate = %#v, want one event and the mailbox listed", candidate)
		}
	}

	stdout, _, err := run(t, "search", "--who", "calendar-invite@lu.ma", "--json")
	if err == nil || cli.ExitCode(err) != 4 {
		t.Fatalf("shared-mailbox --who err = %v stdout = %s", err, stdout)
	}
	var out errorResponse
	if decodeErr := json.Unmarshal([]byte(stdout), &out); decodeErr != nil {
		t.Fatalf("decode shared-mailbox error: %v\n%s", decodeErr, stdout)
	}
	if out.Error.Code != "ambiguous_who" || len(out.Error.Candidates) != 2 {
		t.Fatalf("shared-mailbox error = %#v, want ambiguous_who with both candidates", out)
	}

	_, _, err = run(t, "search", "--who", "calendar-invite@lu.ma")
	if err == nil {
		t.Fatal("shared-mailbox --who text succeeded")
	}
	text := err.Error()
	for _, want := range []string{"matched more than one person", "ClawCon", "Frontier Tower SF", "Retry with a name: calcrawl search --who ClawCon"} {
		if !strings.Contains(text, want) {
			t.Fatalf("shared-mailbox text missing %q:\n%s", want, text)
		}
	}

	// The hinted retry must return only that entity's events: the shared
	// mailbox stays out of the event filter, so who and search agree.
	for _, tc := range []struct {
		who     string
		summary string
	}{
		{"ClawCon", "ClawCon Singapore"},
		{"Frontier Tower SF", "SF Show & Tell"},
	} {
		search := runJSON[searchResponse](t, "search", "--who", tc.who, "--json")
		if len(search.Results) != 1 || search.TotalMatches != 1 {
			t.Fatalf("search --who %q = %#v, want exactly its own event", tc.who, search)
		}
		if search.Results[0].Who != tc.who || !strings.Contains(search.Results[0].Snippet, tc.summary) {
			t.Fatalf("search --who %q result = %#v, want %q", tc.who, search.Results[0], tc.summary)
		}
	}
}

func TestSearchWhoUnknownHasSuggestionsOrHint(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	stdout, _, err := run(t, "search", "planning", "--who", "alice@exampl.com", "--json")
	if err == nil || cli.ExitCode(err) != 5 {
		t.Fatalf("unknown identifier err = %v stdout = %s", err, stdout)
	}
	var suggested errorResponse
	if decodeErr := json.Unmarshal([]byte(stdout), &suggested); decodeErr != nil {
		t.Fatalf("decode suggested error: %v\n%s", decodeErr, stdout)
	}
	if suggested.Error.Code != "unknown_who" || len(suggested.Error.DidYouMean) != 1 || suggested.Error.DidYouMean[0].Who != "Alice Example" {
		t.Fatalf("suggested unknown error = %#v", suggested)
	}

	stdout, _, err = run(t, "search", "planning", "--who", "No Such Person", "--json")
	if err == nil || cli.ExitCode(err) != 5 {
		t.Fatalf("unknown who err = %v stdout = %s", err, stdout)
	}
	var unknown errorResponse
	if decodeErr := json.Unmarshal([]byte(stdout), &unknown); decodeErr != nil {
		t.Fatalf("decode unknown error: %v\n%s", decodeErr, stdout)
	}
	if unknown.Error.Code != "unknown_who" || len(unknown.Error.DidYouMean) != 0 || unknown.Error.Hint == "" {
		t.Fatalf("unknown error = %#v, want empty did_you_mean and hint", unknown)
	}
	if !strings.Contains(unknown.Error.Hint, "Search without --who") {
		t.Fatalf("unknown hint = %q", unknown.Error.Hint)
	}
}

func TestSearchWhoFuzzyOnlySingleMatchSuggests(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	stdout, _, err := run(t, "search", "planning", "--who", "alce", "--json")
	if err == nil || cli.ExitCode(err) != 5 {
		t.Fatalf("fuzzy-only who err = %v stdout = %s", err, stdout)
	}
	var suggested errorResponse
	if decodeErr := json.Unmarshal([]byte(stdout), &suggested); decodeErr != nil {
		t.Fatalf("decode fuzzy-only error: %v\n%s", decodeErr, stdout)
	}
	if suggested.Error.Code != "unknown_who" || len(suggested.Error.DidYouMean) != 1 || suggested.Error.DidYouMean[0].Who != "Alice Example" {
		t.Fatalf("fuzzy-only error = %#v", suggested)
	}
	if strings.Contains(stdout, "who_resolved") || strings.Contains(stdout, "results") {
		t.Fatalf("fuzzy-only search returned search fields:\n%s", stdout)
	}
}

func TestSearchFilterOnlyAndIdentifierFiltering(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	byPerson := runJSON[searchResponse](t, "search", "--who", "bob@example.com", "--json")
	if byPerson.Query != "" || byPerson.TotalMatches != 1 || len(byPerson.Results) != 1 {
		t.Fatalf("filter-only who search = %#v", byPerson)
	}
	if byPerson.WhoResolved == nil || byPerson.WhoResolved.Who != "Bob Example" {
		t.Fatalf("filter-only who_resolved = %#v", byPerson.WhoResolved)
	}

	byDate := runJSON[searchResponse](t, "search", "--after", "2026-05-01", "--json")
	if byDate.Query != "" || byDate.TotalMatches != 1 || byDate.Results[0].Snippet != "Public holiday - Netherlands" {
		t.Fatalf("filter-only date search = %#v", byDate)
	}

	stdout, _, err := run(t, "search", "--json")
	if err == nil {
		t.Fatalf("search without query or filters succeeded: %s", stdout)
	}
	if cli.ExitCode(err) != 2 || !strings.Contains(err.Error(), "search query is required") {
		t.Fatalf("search without query err = %v", err)
	}
}

func TestSearchWhoRejectsBlankIdentity(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	stdout, _, err := run(t, "search", "planning", "--who", "  ", "--json")
	if err == nil {
		t.Fatalf("blank --who succeeded: %s", stdout)
	}
	if !strings.Contains(err.Error(), "search --who requires an identity") {
		t.Fatalf("blank --who err = %v", err)
	}
}

func TestShortRefSearchTextAndOpen(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	search := runJSON[searchResponse](t, "search", "planning", "--json")
	if len(search.Results) != 1 {
		t.Fatalf("search results = %#v, want one planning result", search.Results)
	}
	fullRef := "calcrawl:event/11111111-1111-1111-1111-111111111111"
	if search.Results[0].Ref != fullRef {
		t.Fatalf("search ref = %q, want %q", search.Results[0].Ref, fullRef)
	}
	alias := search.Results[0].ShortRef
	if !validTestShortRef(alias) {
		t.Fatalf("short_ref = %q, want lowercase shortref alias", alias)
	}

	text, _, err := run(t, "search", "planning")
	if err != nil {
		t.Fatalf("search text: %v\n%s", err, text)
	}
	if strings.Contains(text, fullRef) {
		t.Fatalf("search text leaked full ref instead of short ref:\n%s", text)
	}
	if !strings.Contains(text, alias) {
		t.Fatalf("human search text missing short ref %q:\n%s", alias, text)
	}
	opened := runJSON[archive.EventDetail](t, "open", alias, "--json")
	if opened.Ref != fullRef {
		t.Fatalf("open short ref = %q, want %q", opened.Ref, fullRef)
	}
}

func TestShortRefErrorsAreStructured(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	stdout, _, err := run(t, "open", "abc0d", "--json")
	if err == nil {
		t.Fatal("unknown short ref opened successfully")
	}
	if !strings.Contains(stdout, `"code":"unknown_short_ref"`) {
		t.Fatalf("unknown short ref JSON = %s", stdout)
	}
}

func TestSearchLimitAboveOldCapIsHonored(t *testing.T) {
	db := setupCalendarFixture(t)
	const limit = 205
	insertManyEvents(t, db, limit)
	runSync(t)

	jsonLimit := runJSON[searchResponse](t, "search", "standup", "--limit", "205", "--json")
	if len(jsonLimit.Results) != limit || jsonLimit.TotalMatches != limit || jsonLimit.Truncated {
		t.Fatalf("JSON limit = len %d total %d truncated %v", len(jsonLimit.Results), jsonLimit.TotalMatches, jsonLimit.Truncated)
	}
	textLimit := runOK(t, "search", "standup", "--limit", "205")
	if !strings.Contains(textLimit, `Search "standup": showing 205 of 205.`) {
		t.Fatalf("text limit summary missing exact count:\n%s", textLimit)
	}
	if got := strings.Count(textLimit, "Daily standup"); got != limit {
		t.Fatalf("text limit rendered %d rows, want %d:\n%s", got, limit, textLimit)
	}
	if strings.Contains(textLimit, "More:") {
		t.Fatalf("text limit above old cap reported hidden rows:\n%s", textLimit)
	}
}

func TestSearchDefaultLimitAndFlagsAfterQuery(t *testing.T) {
	db := setupCalendarFixture(t)
	insertManyEvents(t, db, 205)
	runSync(t)

	defaultLimit := runJSON[searchResponse](t, "search", "standup", "--json")
	if len(defaultLimit.Results) != archive.DefaultSearchLimit || defaultLimit.TotalMatches != 205 || !defaultLimit.Truncated {
		t.Fatalf("default bounded search = len %d total %d truncated %v", len(defaultLimit.Results), defaultLimit.TotalMatches, defaultLimit.Truncated)
	}
	afterQueryFlag := runJSON[searchResponse](t, "search", "standup", "--limit", "3", "--json")
	if len(afterQueryFlag.Results) != 3 {
		t.Fatalf("flags after query returned %d results, want 3", len(afterQueryFlag.Results))
	}
}

func TestSearchLimitZeroIsUsageError(t *testing.T) {
	setupTestHome(t)

	stdout, stderr, err := run(t, "search", "standup", "--limit", "0")
	if err == nil || cli.ExitCode(err) != 2 {
		t.Fatalf("limit zero err = %v code=%d stdout=%s stderr=%s", err, cli.ExitCode(err), stdout, stderr)
	}
	if !strings.Contains(err.Error(), "search --limit must be positive") {
		t.Fatalf("limit zero error = %v", err)
	}
}

func TestChangedEventIsReported(t *testing.T) {
	db := setupCalendarFixture(t)
	runSync(t)
	mustExec(t, db, `update CalendarItem set summary = 'Planning meeting updated' where ROWID = 100`)
	changed := runSync(t)
	if changed.NewEvents != 0 || changed.ChangedEvents != 1 {
		t.Fatalf("changed sync = %#v, want one changed event", changed)
	}
}

func TestContactsExportAndForeignRefRejection(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)
	export := runJSON[control.ContactExport](t, "contacts", "export", "--json")
	if len(export.Contacts) != 2 {
		t.Fatalf("contacts = %#v, want two phone-backed attendees", export.Contacts)
	}
	if export.Contacts[0].DisplayName != "Alice Example" || export.Contacts[0].PhoneNumbers[0] != "+15550100" {
		t.Fatalf("first contact = %#v", export.Contacts[0])
	}
	stdout, _, err := run(t, "open", "imsgcrawl:msg/1", "--json")
	if err == nil {
		t.Fatal("foreign ref opened successfully")
	}
	if !strings.Contains(stdout, `"code":"command_failed"`) {
		t.Fatalf("foreign ref error JSON = %s", stdout)
	}
}

func TestReadsNeverMutateArchive(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)
	path := archive.DefaultPath()
	before := fileHash(t, path)
	search := runJSON[searchResponse](t, "search", "planning", "--json")
	runJSON[map[string]any](t, "status", "--json")
	runJSON[archive.EventDetail](t, "open", search.Results[0].Ref, "--json")
	runJSON[control.ContactExport](t, "contacts", "export", "--json")
	runJSON[whoResponse](t, "who", "alice", "--json")
	after := fileHash(t, path)
	if before != after {
		t.Fatalf("archive hash changed across read commands: %s -> %s", before, after)
	}
}

func TestMissingArchiveReadBehaviour(t *testing.T) {
	setupCalendarFixture(t)
	status := runJSON[map[string]any](t, "status", "--json")
	if got := status["state"]; got != "missing" {
		t.Fatalf("status state = %v, want missing", got)
	}
	doctor := runJSON[map[string]any](t, "doctor", "--json")
	if _, ok := doctor["log"]; ok {
		t.Fatalf("doctor leaked log envelope: %#v", doctor)
	}
	for _, args := range [][]string{
		{"search", "planning", "--json"},
		{"open", "calcrawl:event/11111111-1111-1111-1111-111111111111", "--json"},
		{"contacts", "export", "--json"},
	} {
		if _, _, err := run(t, args...); err == nil {
			t.Fatalf("calcrawl %v succeeded with missing archive", args)
		}
	}
	if _, err := os.Stat(archive.DefaultPath()); !os.IsNotExist(err) {
		t.Fatalf("read commands created archive: %v", err)
	}
}

type searchResponse struct {
	Query        string                 `json:"query"`
	WhoResolved  *archive.WhoResolved   `json:"who_resolved"`
	Results      []archive.SearchResult `json:"results"`
	TotalMatches int64                  `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
}

type whoResponse struct {
	Query      string                 `json:"query"`
	Candidates []archive.WhoCandidate `json:"candidates"`
}

type errorResponse struct {
	Error struct {
		Code       string                 `json:"code"`
		Message    string                 `json:"message"`
		Remedy     string                 `json:"remedy"`
		Candidates []archive.WhoCandidate `json:"candidates"`
		DidYouMean []archive.WhoCandidate `json:"did_you_mean"`
		Hint       string                 `json:"hint"`
	} `json:"error"`
}

type syncComplete struct {
	Event           string `json:"event"`
	Calendars       int    `json:"calendars"`
	Events          int    `json:"events"`
	NewEvents       int    `json:"new_events"`
	ChangedEvents   int    `json:"changed_events"`
	UnchangedEvents int    `json:"unchanged_events"`
}

func runSync(t *testing.T) syncComplete {
	t.Helper()
	stdout := runOK(t, "sync", "--json")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	var complete syncComplete
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &complete); err != nil {
		t.Fatalf("decode sync JSONL: %v\n%s", err, stdout)
	}
	if complete.Event != "complete" {
		t.Fatalf("last sync event = %#v", complete)
	}
	return complete
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, err := run(t, args...)
	if err != nil {
		t.Fatalf("calcrawl %v failed: %v\nstdout:\n%s\nstderr:\n%s", args, err, stdout, stderr)
	}
	return stdout
}

func runJSON[T any](t *testing.T, args ...string) T {
	t.Helper()
	stdout := runOK(t, args...)
	var out T
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode JSON from %v: %v\n%s", args, err, stdout)
	}
	return out
}

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := cli.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), err
}

func readTestLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".opentrawl", "calcrawl", "logs", "calcrawl.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func countValues(value any) map[string]int64 {
	out := map[string]int64{}
	items, ok := value.([]any)
	if !ok {
		return out
	}
	for _, item := range items {
		row, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		number, _ := row["value"].(float64)
		out[id] = int64(number)
	}
	return out
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hasString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func validTestShortRef(value string) bool {
	if len(value) < 5 {
		return false
	}
	for _, r := range value {
		if !strings.ContainsRune("23456789abcdefghjkmnpqrstuvwxyz", r) {
			return false
		}
	}
	return true
}

func assertNoWhoMatched(t *testing.T, args ...string) {
	t.Helper()
	stdout := runOK(t, args...)
	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("decode JSON from %v: %v\n%s", args, err, stdout)
	}
	if _, ok := raw["who_matched"]; ok {
		t.Fatalf("who_matched leaked in %v: %s", args, stdout)
	}
}

func assertLinesWithinDisplayWidth(t *testing.T, got string, width int) {
	t.Helper()
	for lineNo, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if lineWidth := render.DisplayWidth(line); lineWidth > width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", lineNo+1, lineWidth, width, got)
		}
	}
}

func setupTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	return filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar")
}
