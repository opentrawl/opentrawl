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
	counts := countValues(status["counts"])
	if counts["events"] != 2 || counts["calendars"] != 2 || counts["since"] != 2026 {
		t.Fatalf("counts = %#v, want events=2 calendars=2 since=2026", counts)
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
	allDayOpen := runJSON[archive.EventDetail](t, "open", allDay.Results[0].Ref, "--json")
	if allDayOpen.Start != "2026-05-05" || allDayOpen.End != "2026-05-06" || !allDayOpen.AllDay {
		t.Fatalf("all-day event = %#v", allDayOpen)
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

func setupTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	return filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar")
}
