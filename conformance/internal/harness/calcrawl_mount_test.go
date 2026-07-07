package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const calcrawlPluginEnv = "OPENTRAWL_CONFORMANCE_CALCRAWL_PLUGIN"

func TestMountedCalcrawlPlugin(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv(calcrawlPluginEnv))
	if binary == "" {
		t.Skip(calcrawlPluginEnv + " is not set")
	}
	abs, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	writeCalcrawlCalendarFixture(t, home)

	var trace bytes.Buffer
	runner := NewRunner(abs)
	runner.Trace = &trace
	metadata := runner.Run(context.Background(), "metadata", "--json")
	if !metadata.OK() {
		t.Fatalf("mounted calcrawl metadata failed: %s\nstdout:\n%s\nstderr:\n%s", metadata.FailureDetail(), metadata.Stdout, metadata.Stderr)
	}
	var manifest struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(metadata.Stdout), &manifest); err != nil {
		t.Fatalf("mounted calcrawl metadata JSON: %v\n%s", err, metadata.Stdout)
	}
	if !hasCapability(manifest.Capabilities, "short_refs") {
		t.Fatalf("mounted calcrawl metadata capabilities missing short_refs: %#v", manifest.Capabilities)
	}
	sync := runner.Run(context.Background(), "sync", "--json")
	if !sync.OK() {
		t.Fatalf("mounted calcrawl sync failed: %s\nstdout:\n%s\nstderr:\n%s", sync.FailureDetail(), sync.Stdout, sync.Stderr)
	}
	report := Suite{Runner: runner}.Run(context.Background())
	if report.HasFailures() {
		var table bytes.Buffer
		_ = WriteTable(&table, report)
		t.Fatalf("mounted calcrawl conformance failed:\n%s\ntrace:\n%s", table.String(), trace.String())
	}
	if !strings.Contains(trace.String(), "spawn "+abs+" sync --json") {
		t.Fatalf("trace did not record mounted sync spawn for %s:\n%s", abs, trace.String())
	}
	if !strings.Contains(trace.String(), "spawn "+abs+" metadata --json") {
		t.Fatalf("trace did not record mounted metadata probe for %s:\n%s", abs, trace.String())
	}
	t.Logf("mounted calcrawl plugin path: %s\n%s", abs, trace.String())
}

func writeCalcrawlCalendarFixture(t *testing.T, home string) {
	t.Helper()
	sqlite, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("sqlite3 is not available")
	}
	dir := filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "Calendar.sqlitedb")
	cmd := exec.Command(sqlite, db) // #nosec G204 -- test fixture database path is under t.TempDir.
	cmd.Stdin = strings.NewReader(calcrawlFixtureSQL)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create calcrawl fixture: %v\n%s", err, out)
	}
	stamp := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(db, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

const calcrawlFixtureSQL = `
create table Store (ROWID integer primary key, name text, type integer, disabled integer);
create table Calendar (ROWID integer primary key, store_id integer, title text, type integer, external_id text);
create table CalendarItem (
  ROWID integer primary key, summary text, description text, start_date real, end_date real,
  start_tz text, end_tz text, all_day integer, calendar_id integer, organizer_id integer,
  status integer, url text, has_recurrences integer, has_attendees integer, UUID text,
  unique_identifier text, entity_type integer, location_id integer
);
create table Participant (
  ROWID integer primary key, entity_type integer, type integer, status integer, role integer,
  identity_id integer, owner_id integer, email text, phone_number text, is_self integer,
  comment text
);
create table Identity (ROWID integer primary key, display_name text, address text, first_name text, last_name text);
create table Location (ROWID integer primary key, title text, address text, item_owner_id integer);
insert into Store(ROWID, name, type, disabled) values
  (1, 'iCloud', 1, 0),
  (2, 'Subscribed Calendars', 2, 0),
  (3, 'Reminders', 3, 0);
insert into Calendar(ROWID, store_id, title, type, external_id) values
  (10, 1, 'Work', 1, 'work-calendar'),
  (11, 2, 'Holidays', 2, 'holidays-calendar'),
  (12, 3, 'Reminders list', 3, 'reminders-calendar');
insert into CalendarItem(
  ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
  calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
  UUID, unique_identifier, entity_type, location_id
) values
  (100, 'Planning meeting', 'Discuss launch with Alice.', strftime('%s','2026-03-04T09:00:00Z') - 978307200, strftime('%s','2026-03-04T09:30:00Z') - 978307200, 'Europe/Amsterdam', 'Europe/Amsterdam', 0, 10, 1000, 1, 'https://example.com/event', 1, 1, '11111111-1111-1111-1111-111111111111', 'event-planning', 2, 900),
  (101, 'Public holiday', 'Subscribed holiday.', strftime('%s','2026-05-04T22:00:00Z') - 978307200, strftime('%s','2026-05-05T22:00:00Z') - 978307200, 'Europe/Amsterdam', 'Europe/Amsterdam', 1, 11, 0, 0, '', 0, 1, '22222222-2222-2222-2222-222222222222', 'event-holiday', 2, 901),
  (102, 'Conformance test review', 'Synthetic test event.', strftime('%s','2026-08-01T10:00:00Z') - 978307200, strftime('%s','2026-08-01T10:30:00Z') - 978307200, 'UTC', 'UTC', 0, 10, 1001, 1, '', 0, 1, '33333333-3333-3333-3333-333333333333', 'event-test', 2, 0),
  (103, 'Task row', '', 0, 0, 'UTC', 'UTC', 0, 10, 0, 1, '', 0, 0, '44444444-4444-4444-4444-444444444444', 'task-row', 1, 0);
insert into Identity(ROWID, display_name, address, first_name, last_name) values
  (500, 'Alice Example', 'alice@example.com', 'Alice', 'Example'),
  (501, 'Bob Example', 'bob@example.com', 'Bob', 'Example'),
  (502, 'Holiday Bot', 'holidays@example.com', 'Holiday', 'Bot');
insert into Participant(
  ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
) values
  (1000, 2, 1, 2, 3, 500, 100, 'alice@example.com', '+15550100', 1, ''),
  (1001, 2, 1, 4, 1, 501, 100, 'bob@example.com', '+15550101', 0, ''),
  (1002, 2, 1, 2, 1, 502, 101, 'holidays@example.com', '', 0, '');
insert into Location(ROWID, title, address, item_owner_id) values
  (900, 'Room 1', '1 Example Street', 100),
  (901, 'Netherlands', '', 101);
`
