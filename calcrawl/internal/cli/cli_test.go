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
		Capabilities []string `json:"capabilities"`
	}](t, "metadata", "--json")
	for _, want := range []string{"who", "short_refs"} {
		if !hasString(manifest.Capabilities, want) {
			t.Fatalf("capabilities = %#v, want %q", manifest.Capabilities, want)
		}
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
	if result.ShortRef != "" {
		t.Fatalf("search JSON exposed short ref: %#v", result)
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
}

func TestSearchWhoFiltersParticipants(t *testing.T) {
	setupCalendarFixture(t)
	runSync(t)

	for _, args := range [][]string{
		{"search", "planning", "--who", "alice example", "--json"},
		{"search", "planning", "--who", "alice@example.com", "--json"},
		{"search", "planning", "--who", "bob@example.com", "--json"},
		{"search", "--who", " Alice   Example ", "planning", "--json"},
	} {
		search := runJSON[searchResponse](t, args...)
		if len(search.Results) != 1 || search.TotalMatches != 1 || search.Results[0].Who != "Alice Example" {
			t.Fatalf("calcrawl %v = %#v, want Alice match", args, search)
		}
		if len(search.WhoMatched) != 0 {
			t.Fatalf("unique who reported ambiguity: %#v", search.WhoMatched)
		}
	}

	none := runJSON[searchResponse](t, "search", "planning", "--who", "No Such Person", "--json")
	if len(none.Results) != 0 || none.TotalMatches != 0 || none.Truncated {
		t.Fatalf("unknown who = %#v, want empty filtered result", none)
	}
}

func TestSearchWhoReportsAmbiguousMatchedPeople(t *testing.T) {
	db := setupCalendarFixture(t)
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(503, 'ALICE EXAMPLE', 'alice.alt@example.com', 'Alice', 'Example')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1003, 2, 1, 2, 1, 503, 101, 'alice.alt@example.com', '', 0, '')`)
	runSync(t)

	search := runJSON[searchResponse](t, "search", "holiday", "--who", "alice example", "--json")
	if len(search.WhoMatched) != 2 || search.WhoMatched[0] != "ALICE EXAMPLE" || search.WhoMatched[1] != "Alice Example" {
		t.Fatalf("who_matched = %#v, want two case-folded people", search.WhoMatched)
	}
	if len(search.Results) != 1 || search.TotalMatches != 1 {
		t.Fatalf("ambiguous who filtered results = %#v", search)
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

	text, _, err := run(t, "search", "planning")
	if err != nil {
		t.Fatalf("search text: %v\n%s", err, text)
	}
	fullRef := "calcrawl:event/11111111-1111-1111-1111-111111111111"
	if strings.Contains(text, fullRef) {
		t.Fatalf("search text leaked full ref instead of short ref:\n%s", text)
	}
	alias := lastTableField(text)
	if alias == "" {
		t.Fatalf("could not find short ref in search text:\n%s", text)
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

func TestSearchLimitBoundsAndFlagsAfterQuery(t *testing.T) {
	db := setupCalendarFixture(t)
	insertManyEvents(t, db, 205)
	runSync(t)

	defaultLimit := runJSON[searchResponse](t, "search", "standup", "--json")
	if len(defaultLimit.Results) != archive.DefaultSearchLimit || defaultLimit.TotalMatches != 205 || !defaultLimit.Truncated {
		t.Fatalf("default bounded search = len %d total %d truncated %v", len(defaultLimit.Results), defaultLimit.TotalMatches, defaultLimit.Truncated)
	}
	maxLimit := runJSON[searchResponse](t, "search", "standup", "--limit", "500", "--json")
	if len(maxLimit.Results) != archive.MaxSearchLimit || maxLimit.TotalMatches != 205 || !maxLimit.Truncated {
		t.Fatalf("max bounded search = len %d total %d truncated %v", len(maxLimit.Results), maxLimit.TotalMatches, maxLimit.Truncated)
	}
	afterQueryFlag := runJSON[searchResponse](t, "search", "standup", "--limit", "3", "--json")
	if len(afterQueryFlag.Results) != 3 {
		t.Fatalf("flags after query returned %d results, want 3", len(afterQueryFlag.Results))
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
	WhoMatched   []string               `json:"who_matched"`
	Results      []archive.SearchResult `json:"results"`
	TotalMatches int64                  `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
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

func lastTableField(value string) string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		fields := strings.Fields(lines[i])
		if len(fields) == 0 || fields[0] == "time" {
			continue
		}
		return fields[len(fields)-1]
	}
	return ""
}

func setupTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	return filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar")
}
