package qa

import "time"

func createCalendarFixture(path string) error {
	db, err := openSQLite(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	start := coreDate(time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC))
	end := coreDate(time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC))
	if err := execAll(db,
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
		`insert into Store(ROWID, name, type, disabled) values (1, 'iCloud', 1, 0)`,
		`insert into Calendar(ROWID, store_id, title, type, external_id) values (10, 1, 'Work', 1, 'work-calendar')`,
		`insert into Identity(ROWID, display_name, address, first_name, last_name) values (500, 'Alice Example', 'alice@example.com', 'Alice', 'Example')`,
		`insert into Participant(ROWID, entity_type, type, status, role, identity_id, owner_id, email, phone_number, is_self, comment) values (1000, 2, 1, 2, 3, 500, 100, 'alice@example.com', '+15550100', 1, '')`,
		`insert into Location(ROWID, title, address, item_owner_id) values (900, 'Launch room', '1 Example Street', 100)`,
	); err != nil {
		return err
	}
	if _, err := db.Exec(`insert into CalendarItem(
		ROWID, summary, description, start_date, end_date, start_tz, end_tz, all_day,
		calendar_id, organizer_id, status, url, has_recurrences, has_attendees,
		UUID, unique_identifier, entity_type, location_id
	) values (100, 'Launch planning', 'Discuss launch with Alice.', ?, ?, 'Europe/Amsterdam', 'Europe/Amsterdam', 0, 10, 1000, 1, 'https://example.com/event', 0, 1, '11111111-1111-1111-1111-111111111111', 'event-launch', 2, 900)`, start, end); err != nil {
		return err
	}
	return touch(path, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
}
