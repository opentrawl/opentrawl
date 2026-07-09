package calendarstore

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const coreDataUnixOffset = 978307200

func CanaryRead(ctx context.Context, path string) error {
	snap, err := SnapshotPath(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = snap.Close() }()
	st, err := openSnapshot(ctx, snap.Path)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	var count int64
	return st.DB().QueryRowContext(ctx, `select count(*) from CalendarItem`).Scan(&count)
}

func Read(ctx context.Context, path string) (Data, error) {
	snap, err := SnapshotPath(ctx, path)
	if err != nil {
		return Data{}, err
	}
	defer func() { _ = snap.Close() }()
	st, err := openSnapshot(ctx, snap.Path)
	if err != nil {
		return Data{}, err
	}
	defer func() { _ = st.Close() }()
	calendars, err := readCalendars(ctx, st.DB())
	if err != nil {
		return Data{}, err
	}
	events, err := readEvents(ctx, st.DB(), calendars)
	if err != nil {
		return Data{}, err
	}
	info, err := os.Stat(snap.SourcePath)
	if err != nil {
		return Data{}, err
	}
	return Data{
		SourcePath:       snap.SourcePath,
		SourceModifiedAt: info.ModTime().UTC().Format(time.RFC3339Nano),
		Calendars:        calendars,
		Events:           events,
	}, nil
}

func ModifiedAt(path string) (time.Time, error) {
	if path == "" {
		path = DefaultPath()
	}
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func openSnapshot(ctx context.Context, path string) (*store.Store, error) {
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := requireTables(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func requireTables(ctx context.Context, db *sql.DB) error {
	for _, table := range []string{"CalendarItem", "Calendar", "Store", "Participant", "Identity", "Location"} {
		var name string
		err := db.QueryRowContext(ctx, `select name from sqlite_master where type = 'table' and name = ?`, table).Scan(&name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("Calendar database is missing table " + table)
			}
			return err
		}
	}
	return nil
}

func readCalendars(ctx context.Context, db *sql.DB) ([]Calendar, error) {
	rows, err := db.QueryContext(ctx, `
select c.ROWID, c.store_id, coalesce(c.title, ''), coalesce(c.type, 0),
       coalesce(c.external_id, ''), coalesce(s.name, ''), coalesce(s.type, 0),
       coalesce(s.disabled, 0)
from Calendar c
join Store s on s.ROWID = c.store_id
where coalesce(s.name, '') <> 'Reminders'
order by coalesce(s.name, ''), coalesce(c.title, ''), c.ROWID`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Calendar
	for rows.Next() {
		var cal Calendar
		var disabled int64
		if err := rows.Scan(&cal.RowID, &cal.StoreID, &cal.Title, &cal.Type, &cal.ExternalID, &cal.StoreName, &cal.StoreType, &disabled); err != nil {
			return nil, err
		}
		cal.StoreDisabled = disabled != 0
		out = append(out, cal)
	}
	return out, rows.Err()
}

func readEvents(ctx context.Context, db *sql.DB, calendars []Calendar) ([]Event, error) {
	calendarByID := map[int64]Calendar{}
	for _, calendar := range calendars {
		calendarByID[calendar.RowID] = calendar
	}
	participants, organizers, err := readParticipants(ctx, db)
	if err != nil {
		return nil, err
	}
	locationsByOwner, locationsByID, err := readLocations(ctx, db)
	if err != nil {
		return nil, err
	}
	availabilityExpr := "null"
	if ok, err := tableHasColumn(ctx, db, "CalendarItem", "availability"); err != nil {
		return nil, err
	} else if ok {
		availabilityExpr = "ci.availability"
	}
	rows, err := db.QueryContext(ctx, `
select ci.ROWID, coalesce(ci.UUID, ''), coalesce(ci.unique_identifier, ''),
       coalesce(ci.summary, ''), coalesce(ci.description, ''),
       coalesce(ci.start_date, 0), coalesce(ci.end_date, 0),
       coalesce(ci.start_tz, ''), coalesce(ci.end_tz, ''), coalesce(ci.all_day, 0),
       ci.calendar_id, coalesce(ci.organizer_id, 0), coalesce(ci.status, 0),
       coalesce(ci.url, ''), coalesce(ci.has_recurrences, 0), `+availabilityExpr+`, coalesce(ci.location_id, 0)
from CalendarItem ci
join Calendar c on c.ROWID = ci.calendar_id
join Store s on s.ROWID = c.store_id
where ci.entity_type = 2 and coalesce(s.name, '') <> 'Reminders'
order by ci.start_date, ci.ROWID`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Event
	for rows.Next() {
		var event Event
		var startCore, endCore float64
		var startTZ, endTZ string
		var allDay, hasRecurrences int64
		var availability sql.NullInt64
		var calendarID, organizerID, status, locationID int64
		if err := rows.Scan(&event.RowID, &event.UUID, &event.UniqueIdentifier, &event.Summary,
			&event.Description, &startCore, &endCore, &startTZ, &endTZ, &allDay, &calendarID,
			&organizerID, &status, &event.URL, &hasRecurrences, &availability, &locationID); err != nil {
			return nil, err
		}
		calendar, ok := calendarByID[calendarID]
		if !ok {
			continue
		}
		event.Calendar = calendar
		event.AllDay = allDay != 0
		event.Start = convertTime(startCore, startTZ, event.AllDay)
		event.End = convertTime(endCore, firstNonEmpty(endTZ, startTZ), event.AllDay)
		event.Status = eventStatus(status)
		event.HasRecurrences = hasRecurrences != 0
		if availability.Valid {
			value := availability.Int64
			event.Availability = &value
		}
		event.Attendees = participants[event.RowID]
		if organizer, ok := organizers[organizerID]; ok {
			event.Organizer = organizer
		}
		if location, ok := locationsByOwner[event.RowID]; ok {
			event.Location = location
		} else if location, ok := locationsByID[locationID]; ok {
			event.Location = location
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+store.QuoteIdent(table)+`)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int64
		var name string
		var typ string
		var notNull int64
		var defaultValue sql.NullString
		var pk int64
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func readParticipants(ctx context.Context, db *sql.DB) (map[int64][]Participant, map[int64]Person, error) {
	rows, err := db.QueryContext(ctx, `
select p.owner_id, p.ROWID, coalesce(p.status, 0), coalesce(p.role, 0),
       coalesce(p.email, ''), coalesce(p.phone_number, ''), coalesce(p.is_self, 0),
       coalesce(p.comment, ''), coalesce(i.display_name, ''), coalesce(i.address, ''),
       coalesce(i.first_name, ''), coalesce(i.last_name, '')
from Participant p
left join Identity i on i.ROWID = p.identity_id
order by p.owner_id, p.ROWID`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	byOwner := map[int64][]Participant{}
	organizers := map[int64]Person{}
	for rows.Next() {
		var ownerID, rowID, status, role, self int64
		var email, phone, comment, display, address, first, last string
		if err := rows.Scan(&ownerID, &rowID, &status, &role, &email, &phone, &self, &comment, &display, &address, &first, &last); err != nil {
			return nil, nil, err
		}
		person := Person{
			DisplayName: displayName(display, first, last, address, email, phone),
			Email:       firstNonEmpty(email, emailFromAddress(address)),
			PhoneNumber: strings.TrimSpace(phone),
			Address:     strings.TrimSpace(address),
		}
		item := Participant{
			DisplayName: person.DisplayName,
			Email:       person.Email,
			PhoneNumber: person.PhoneNumber,
			Address:     person.Address,
			RSVPStatus:  participantStatus(status),
			Role:        participantRole(role),
			Self:        self != 0,
			Comment:     strings.TrimSpace(comment),
		}
		byOwner[ownerID] = append(byOwner[ownerID], item)
		organizers[rowID] = person
	}
	return byOwner, organizers, rows.Err()
}

func readLocations(ctx context.Context, db *sql.DB) (map[int64]Location, map[int64]Location, error) {
	rows, err := db.QueryContext(ctx, `select ROWID, coalesce(item_owner_id, 0), coalesce(title, ''), coalesce(address, '') from Location`)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	byOwner := map[int64]Location{}
	byID := map[int64]Location{}
	for rows.Next() {
		var rowID, ownerID int64
		var location Location
		if err := rows.Scan(&rowID, &ownerID, &location.Title, &location.Address); err != nil {
			return nil, nil, err
		}
		location.Title = strings.TrimSpace(location.Title)
		location.Address = strings.TrimSpace(location.Address)
		byID[rowID] = location
		if ownerID != 0 {
			byOwner[ownerID] = location
		}
	}
	return byOwner, byID, rows.Err()
}

func convertTime(coreDataSeconds float64, zoneName string, allDay bool) EventTime {
	location := time.Local
	if strings.TrimSpace(zoneName) != "" {
		if loaded, err := time.LoadLocation(strings.TrimSpace(zoneName)); err == nil {
			location = loaded
		}
	}
	seconds, fraction := math.Modf(coreDataSeconds + coreDataUnixOffset)
	t := time.Unix(int64(seconds), int64(fraction*1e9)).In(location)
	if allDay {
		localMidnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, location)
		return EventTime{Value: localMidnight.Format(time.RFC3339), Unix: localMidnight.Unix()}
	}
	return EventTime{Value: t.Format(time.RFC3339), Unix: t.Unix()}
}

func displayName(display, first, last, address, email, phone string) string {
	name := strings.TrimSpace(display)
	if name != "" {
		return name
	}
	name = strings.TrimSpace(strings.TrimSpace(first) + " " + strings.TrimSpace(last))
	if name != "" {
		return name
	}
	return firstNonEmpty(emailFromAddress(address), email, phone)
}

func emailFromAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "@") {
		return address
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func eventStatus(value int64) string {
	switch value {
	case 0:
		return "confirmed"
	case 1:
		return "confirmed"
	case 2:
		return "tentative"
	case 3:
		return "cancelled"
	default:
		return "unknown"
	}
}

func participantStatus(value int64) string {
	switch value {
	case 1:
		return "pending"
	case 2:
		return "accepted"
	case 3:
		return "declined"
	case 4:
		return "tentative"
	case 5:
		return "delegated"
	case 6:
		return "completed"
	case 7:
		return "in_process"
	default:
		return "unknown"
	}
}

func participantRole(value int64) string {
	switch value {
	case 1:
		return "required"
	case 2:
		return "optional"
	case 3:
		return "chair"
	case 4:
		return "non_participant"
	default:
		return "unknown"
	}
}
