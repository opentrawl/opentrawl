package calcrawl

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
	ckoutput "github.com/openclaw/crawlkit/output"
	ckstore "github.com/openclaw/crawlkit/store"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"

	_ "github.com/mattn/go-sqlite3"
)

const coreDataUnixOffset = 978307200

func TestCrawlerSyncSearchOpenAndContacts(t *testing.T) {
	ctx := context.Background()
	stateRoot := setupCalendarFixture(t)
	paths := crawlkit.Paths{
		Archive: filepath.Join(stateRoot, "calendar", "calendar.db"),
		Config:  filepath.Join(stateRoot, "calendar", "config.toml"),
		Logs:    filepath.Join(stateRoot, "calendar", "logs"),
	}
	source := New()

	writeStore, err := ckstore.Open(ctx, ckstore.Options{Path: paths.Archive})
	if err != nil {
		t.Fatal(err)
	}
	report, err := source.Sync(ctx, &crawlkit.Request{
		Store:    writeStore,
		Paths:    paths,
		Format:   ckoutput.Text,
		Out:      &bytes.Buffer{},
		Progress: func(crawlkit.Progress) {},
	})
	if closeErr := writeStore.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 2 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %#v, want 2 added, 0 updated, 0 removed", report)
	}

	readStore := openReadStore(t, ctx, paths.Archive)
	search, err := source.Search(ctx, readRequest(readStore, paths), crawlkit.Query{Text: "planning", Limit: 20})
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || len(search.Results) != 1 {
		t.Fatalf("search = %#v, want one result", search)
	}
	hit := search.Results[0]
	if hit.Ref != "calendar:event/11111111-1111-1111-1111-111111111111" || hit.ShortRef == "" {
		t.Fatalf("search hit refs = %#v", hit)
	}
	if hit.Who != "Alice Example" || hit.Where != "Room 1" {
		t.Fatalf("search hit who/where = %q/%q", hit.Who, hit.Where)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	var openOut bytes.Buffer
	err = source.Open(ctx, &crawlkit.Request{Store: readStore, Paths: paths, Format: ckoutput.JSON, Out: &openOut}, hit.Ref)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	var opened archive.EventDetail
	if err := json.Unmarshal(openOut.Bytes(), &opened); err != nil {
		t.Fatalf("open JSON: %v\n%s", err, openOut.String())
	}
	if opened.Calendar != "Work" || opened.Location == nil || opened.Location.Address != "1 Example Street" {
		t.Fatalf("opened event = %#v", opened)
	}

	readStore = openReadStore(t, ctx, paths.Archive)
	contacts, err := source.ContactExport(ctx, readRequest(readStore, paths))
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(contacts.Contacts) != 2 || contacts.Contacts[0].PhoneNumbers[0] != "+15550100" {
		t.Fatalf("contacts = %#v", contacts)
	}
}

func readRequest(st *ckstore.Store, paths crawlkit.Paths) *crawlkit.Request {
	return &crawlkit.Request{
		Store:  st,
		Paths:  paths,
		Format: ckoutput.Text,
		Out:    &bytes.Buffer{},
	}
}

func openReadStore(t *testing.T, ctx context.Context, path string) *ckstore.Store {
	t.Helper()
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func setupCalendarFixture(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TZ", "UTC")
	dir := filepath.Join(home, "Library", "Group Containers", "group.com.apple.calendar")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Calendar.sqlitedb")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	createCalendarSchema(t, db)
	insertCalendarRows(t, db)
	if err := os.Chtimes(path, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(home, ".opentrawl")
}

func createCalendarSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, stmt := range []string{
		`create table Store (ROWID integer primary key, name text, type integer, disabled integer)`,
		`create table Calendar (ROWID integer primary key, store_id integer, title text, type integer, external_id text)`,
		`create table CalendarItem (
			ROWID integer primary key, summary text, description text, start_date real, end_date real,
			start_tz text, end_tz text, all_day integer, calendar_id integer, organizer_id integer,
			status integer, url text, has_recurrences integer, has_attendees integer, UUID text,
			unique_identifier text, entity_type integer, location_id integer
		)`,
		`create table Participant (
			ROWID integer primary key, entity_type integer, type integer, status integer, role integer,
			identity_id integer, owner_id integer, email text, phone_number text, is_self integer,
			comment text
		)`,
		`create table Identity (ROWID integer primary key, display_name text, address text, first_name text, last_name text)`,
		`create table Location (ROWID integer primary key, title text, address text, item_owner_id integer)`,
	} {
		mustExec(t, db, stmt)
	}
}

func insertCalendarRows(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `insert into Store(ROWID, name, type, disabled) values
		(1, 'iCloud', 1, 0),
		(2, 'Subscribed Calendars', 2, 0),
		(3, 'Reminders', 3, 0)`)
	mustExec(t, db, `insert into Calendar(ROWID, store_id, title, type, external_id) values
		(10, 1, 'Work', 1, 'work-calendar'),
		(11, 2, 'Holidays', 2, 'holidays-calendar'),
		(12, 3, 'Reminders list', 3, 'reminders-calendar')`)
	insertEvent(t, db, 100, "11111111-1111-1111-1111-111111111111", "event-planning", "Planning meeting", "Discuss launch with Alice.", time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC), time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC), false, 10, 1000, 1, "https://example.com/event", true, 900)
	insertEvent(t, db, 101, "22222222-2222-2222-2222-222222222222", "event-holiday", "Public holiday", "Subscribed holiday.", time.Date(2026, 5, 4, 22, 0, 0, 0, time.UTC), time.Date(2026, 5, 5, 22, 0, 0, 0, time.UTC), true, 11, 0, 0, "", false, 901)
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id
	) values (103, 'Task row', '', 0, 0, 'UTC', 'UTC', 0, 10, 0, 1, '', 0, 0,
		'44444444-4444-4444-4444-444444444444', 'task-row', 1, 0)`)
	mustExec(t, db, `insert into Identity(ROWID, display_name, address, first_name, last_name) values
		(500, 'Alice Example', 'alice@example.com', 'Alice', 'Example'),
		(501, 'Bob Example', 'bob@example.com', 'Bob', 'Example'),
		(502, 'Holiday Bot', 'holidays@example.com', 'Holiday', 'Bot')`)
	mustExec(t, db, `insert into Participant(
		ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment
	) values
		(1000, 2, 1, 2, 3, 500, 100, 'alice@example.com', '+15550100', 1, ''),
		(1001, 2, 1, 4, 1, 501, 100, 'bob@example.com', '+15550101', 0, ''),
		(1002, 2, 1, 2, 1, 502, 101, 'holidays@example.com', '', 0, '')`)
	mustExec(t, db, `insert into Location(ROWID, title, address, item_owner_id) values
		(900, 'Room 1', '1 Example Street', 100),
		(901, 'Netherlands', '', 101)`)
}

func insertEvent(t *testing.T, db *sql.DB, rowID int, uuid, uniqueID, summary, description string, start, end time.Time, allDay bool, calendarID, organizerID, status int, url string, recurs bool, locationID int) {
	t.Helper()
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id
	) values (?, ?, ?, ?, ?, 'Europe/Amsterdam', 'Europe/Amsterdam', ?, ?, ?, ?, ?, ?, 1, ?, ?, 2, ?)`,
		rowID, summary, description, coreDate(start), coreDate(end), boolInt(allDay), calendarID, organizerID, status, url, boolInt(recurs), uuid, uniqueID, locationID)
}

func coreDate(t time.Time) float64 {
	return float64(t.Unix() - coreDataUnixOffset)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
