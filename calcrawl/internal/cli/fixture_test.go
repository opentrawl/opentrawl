package cli_test

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const coreDataUnixOffset = 978307200

func setupCalendarFixture(t *testing.T) *sql.DB {
	t.Helper()
	dir := setupTestHome(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Calendar.sqlitedb")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	createCalendarSchema(t, db)
	insertBaseCalendarRows(t, db)
	return db
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
		`create table Identity (
			ROWID integer primary key, display_name text, address text, first_name text, last_name text
		)`,
		`create table Location (ROWID integer primary key, title text, address text, item_owner_id integer)`,
	} {
		mustExec(t, db, stmt)
	}
}

func insertBaseCalendarRows(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `insert into Store(ROWID, name, type, disabled) values
		(1, 'iCloud', 1, 0),
		(2, 'Subscribed Calendars', 2, 0),
		(3, 'Reminders', 3, 0)`)
	mustExec(t, db, `insert into Calendar(ROWID, store_id, title, type, external_id) values
		(10, 1, 'Work', 1, 'work-calendar'),
		(11, 2, 'Holidays', 2, 'holidays-calendar'),
		(12, 3, 'Reminders list', 3, 'reminders-calendar')`)
	insertEvent(t, db, eventFixture{
		rowID:          100,
		uuid:           "11111111-1111-1111-1111-111111111111",
		uniqueID:       "event-planning",
		summary:        "Planning meeting",
		description:    "Discuss launch with Alice.",
		start:          time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC),
		end:            time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC),
		startTZ:        "Europe/Amsterdam",
		endTZ:          "Europe/Amsterdam",
		calendarID:     10,
		organizerID:    1000,
		status:         1,
		url:            "https://example.com/event",
		hasRecurrences: true,
		locationID:     900,
	})
	insertEvent(t, db, eventFixture{
		rowID:       101,
		uuid:        "22222222-2222-2222-2222-222222222222",
		uniqueID:    "event-holiday",
		summary:     "Public holiday",
		description: "Subscribed holiday.",
		start:       time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC),
		end:         time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC),
		startTZ:     "UTC",
		endTZ:       "UTC",
		allDay:      true,
		calendarID:  11,
		status:      1,
		locationID:  901,
	})
	insertEvent(t, db, eventFixture{
		rowID:      102,
		uuid:       "33333333-3333-3333-3333-333333333333",
		summary:    "Reminder event",
		start:      time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		end:        time.Date(2026, 6, 1, 12, 30, 0, 0, time.UTC),
		calendarID: 12,
		status:     1,
	})
	insertNonEvent(t, db)
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

type eventFixture struct {
	rowID          int
	uuid           string
	uniqueID       string
	summary        string
	description    string
	start          time.Time
	end            time.Time
	startTZ        string
	endTZ          string
	allDay         bool
	calendarID     int
	organizerID    int
	status         int
	url            string
	hasRecurrences bool
	locationID     int
}

func insertEvent(t *testing.T, db *sql.DB, event eventFixture) {
	t.Helper()
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id
	) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, 2, ?)`,
		event.rowID, event.summary, event.description, coreDate(event.start), coreDate(event.end),
		event.startTZ, event.endTZ, boolInt(event.allDay), event.calendarID, event.organizerID,
		event.status, event.url, boolInt(event.hasRecurrences), event.uuid, event.uniqueID,
		event.locationID)
}

func insertNonEvent(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id
	) values (103, 'Task row', '', 0, 0, 'UTC', 'UTC', 0, 10, 0, 1, '', 0, 0,
		'44444444-4444-4444-4444-444444444444', 'task-row', 1, 0)`)
}

func insertManyEvents(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	for i := 0; i < count; i++ {
		rowID := 1000 + i
		insertEvent(t, db, eventFixture{
			rowID:       rowID,
			uuid:        fmt.Sprintf("aaaaaaaa-aaaa-aaaa-aaaa-%012d", i),
			uniqueID:    fmt.Sprintf("standup-%03d", i),
			summary:     fmt.Sprintf("Daily standup %03d", i),
			description: "Synthetic standup event.",
			start:       time.Date(2026, 7, 1, 9, i%60, 0, 0, time.UTC),
			end:         time.Date(2026, 7, 1, 9, (i%60)+1, 0, 0, time.UTC),
			startTZ:     "UTC",
			endTZ:       "UTC",
			calendarID:  10,
			status:      1,
		})
	}
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
