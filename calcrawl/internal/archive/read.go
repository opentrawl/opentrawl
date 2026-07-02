package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/crawlkit/store"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	var out Status
	out.ArchivePath = s.path
	out.ArchiveBytes = fileSize(s.path)
	version, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	out.SchemaVersion = version
	db := s.store.DB()
	if out.Calendars, err = countTable(ctx, db, "calendars"); err != nil {
		return Status{}, err
	}
	if out.Events, err = countTable(ctx, db, "events"); err != nil {
		return Status{}, err
	}
	_ = db.QueryRowContext(ctx, `select coalesce(min(start_unix), 0), coalesce(max(start_unix), 0) from events`).Scan(&out.EarliestUnix, &out.LatestUnix)
	stateStore := state.New(db)
	if rec, ok, err := stateStore.Get(ctx, syncSource, syncEntity, syncLastSync); err == nil && ok {
		out.LastSyncAt = rec.Value
	}
	if rec, ok, err := stateStore.Get(ctx, syncSource, syncEntity, syncSourceModified); err == nil && ok {
		out.SourceModifiedAt = rec.Value
	}
	return out, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int, after, before int64) ([]SearchResult, int64, error) {
	ftsQuery, err := store.FTS5Terms(query, "")
	if err != nil {
		return nil, 0, err
	}
	where, args := searchWhere(ftsQuery, after, before)
	total, err := s.countSearch(ctx, where, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.store.DB().QueryContext(ctx, searchSQL(where), append(args, limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	results := []SearchResult{}
	for rows.Next() {
		var row eventRow
		if err := scanEventRow(rows, &row); err != nil {
			return nil, 0, err
		}
		results = append(results, SearchResult{
			Ref:     RefForUID(row.UID),
			Time:    row.Start,
			Who:     row.Who(),
			Where:   row.Where(),
			Snippet: row.Snippet(),
		})
	}
	return results, total, rows.Err()
}

func (s *Store) OpenEvent(ctx context.Context, ref string) (EventDetail, error) {
	uid, ok := UIDFromRef(ref)
	if !ok {
		return EventDetail{}, fmt.Errorf("invalid calcrawl event ref %q", ref)
	}
	row := eventRow{}
	err := s.store.DB().QueryRowContext(ctx, `
select event_uid, uuid, unique_identifier, calendar_id, calendar_title, calendar_type,
       calendar_external_id, account_name, account_type, start_time, end_time, all_day,
       summary, description, status, url, has_recurrences, organizer_name,
       organizer_email, organizer_phone, location_title, location_address, attendees_json
from events
where event_uid = ?`, uid).Scan(&row.UID, &row.UUID, &row.UniqueIdentifier, &row.CalendarID,
		&row.CalendarTitle, &row.CalendarType, &row.CalendarExternalID, &row.AccountName,
		&row.AccountType, &row.Start, &row.End, &row.AllDay, &row.Summary, &row.Description,
		&row.Status, &row.URL, &row.HasRecurrences, &row.OrganizerName, &row.OrganizerEmail,
		&row.OrganizerPhone, &row.LocationTitle, &row.LocationAddress, &row.AttendeesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return EventDetail{}, fmt.Errorf("event not found: %s", ref)
	}
	if err != nil {
		return EventDetail{}, err
	}
	attendees, err := row.Attendees()
	if err != nil {
		return EventDetail{}, err
	}
	description, cut := shorten(row.Description, maxOpenDescriptionRunes)
	return EventDetail{
		Ref:                  RefForUID(row.UID),
		UUID:                 row.UUID,
		UniqueIdentifier:     row.UniqueIdentifier,
		Title:                row.Title(),
		Description:          description,
		DescriptionTruncated: cut,
		Start:                row.Start,
		End:                  row.End,
		AllDay:               row.AllDay != 0,
		Calendar:             row.CalendarTitle,
		Account:              row.AccountName,
		Location:             row.Location(),
		Organizer:            Person{DisplayName: row.OrganizerName, Email: row.OrganizerEmail, PhoneNumber: row.OrganizerPhone},
		Attendees:            attendees,
		URL:                  row.URL,
		Status:               row.Status,
		HasRecurrences:       row.HasRecurrences != 0,
	}, nil
}

func (s *Store) ExportContacts(ctx context.Context) ([]control.Contact, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select display_name, email, phone_number
from participants
where trim(phone_number) <> ''
order by display_name, email, phone_number`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type candidate struct {
		name  string
		email string
		phone string
	}
	byPhone := map[string]candidate{}
	order := []string{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.name, &item.email, &item.phone); err != nil {
			return nil, err
		}
		item.name = contactName(item.name, item.email, item.phone)
		item.phone = strings.TrimSpace(item.phone)
		if item.name == "" || item.phone == "" {
			continue
		}
		if current, ok := byPhone[item.phone]; ok {
			if len([]rune(item.name)) > len([]rune(current.name)) {
				byPhone[item.phone] = item
			}
			continue
		}
		byPhone[item.phone] = item
		order = append(order, item.phone)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]control.Contact, 0, len(order))
	for _, phone := range order {
		item := byPhone[phone]
		out = append(out, control.Contact{DisplayName: item.name, PhoneNumbers: []string{phone}})
	}
	return out, nil
}

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, `select count(*) from `+store.QuoteIdent(table)).Scan(&count)
	return count, err
}

func (s *Store) countSearch(ctx context.Context, where string, args []any) (int64, error) {
	var total int64
	err := s.store.DB().QueryRowContext(ctx, `select count(*) from events_fts join events e on e.event_uid = events_fts.event_uid `+where, args...).Scan(&total)
	return total, err
}

func searchWhere(ftsQuery string, after, before int64) (string, []any) {
	parts := []string{"where events_fts match ?"}
	args := []any{ftsQuery}
	if after > 0 {
		parts = append(parts, "e.start_unix >= ?")
		args = append(args, after)
	}
	if before > 0 {
		parts = append(parts, "e.start_unix <= ?")
		args = append(args, before)
	}
	return strings.Join(parts, " and "), args
}

func searchSQL(where string) string {
	return `
select e.event_uid, e.uuid, e.unique_identifier, e.calendar_id, e.calendar_title,
       e.calendar_type, e.calendar_external_id, e.account_name, e.account_type,
       e.start_time, e.end_time, e.all_day, e.summary, e.description, e.status,
       e.url, e.has_recurrences, e.organizer_name, e.organizer_email,
       e.organizer_phone, e.location_title, e.location_address, e.attendees_json
from events_fts
join events e on e.event_uid = events_fts.event_uid
` + where + `
order by rank, e.start_unix desc, e.event_uid
limit ?`
}

type eventRow struct {
	UID                string
	UUID               string
	UniqueIdentifier   string
	CalendarID         string
	CalendarTitle      string
	CalendarType       int64
	CalendarExternalID string
	AccountName        string
	AccountType        int64
	Start              string
	End                string
	AllDay             int
	Summary            string
	Description        string
	Status             string
	URL                string
	HasRecurrences     int
	OrganizerName      string
	OrganizerEmail     string
	OrganizerPhone     string
	LocationTitle      string
	LocationAddress    string
	AttendeesJSON      string
}

func scanEventRow(rows *sql.Rows, row *eventRow) error {
	return rows.Scan(&row.UID, &row.UUID, &row.UniqueIdentifier, &row.CalendarID, &row.CalendarTitle,
		&row.CalendarType, &row.CalendarExternalID, &row.AccountName, &row.AccountType,
		&row.Start, &row.End, &row.AllDay, &row.Summary, &row.Description, &row.Status,
		&row.URL, &row.HasRecurrences, &row.OrganizerName, &row.OrganizerEmail,
		&row.OrganizerPhone, &row.LocationTitle, &row.LocationAddress, &row.AttendeesJSON)
}

func (r eventRow) Title() string {
	if strings.TrimSpace(r.Summary) != "" {
		return strings.TrimSpace(r.Summary)
	}
	return "(untitled event)"
}

func (r eventRow) Who() string {
	if strings.TrimSpace(r.OrganizerName) != "" {
		return r.OrganizerName
	}
	if strings.TrimSpace(r.OrganizerEmail) != "" {
		return r.OrganizerEmail
	}
	attendees, err := r.Attendees()
	if err == nil && len(attendees) > 0 {
		if attendees[0].DisplayName != "" {
			return attendees[0].DisplayName
		}
		if attendees[0].Email != "" {
			return attendees[0].Email
		}
	}
	// No organizer recorded (typical for self-created events): omit
	// who rather than emit a literal "unknown".
	return ""
}

func (r eventRow) Where() string {
	if strings.TrimSpace(r.LocationTitle) != "" {
		return r.LocationTitle
	}
	if strings.TrimSpace(r.LocationAddress) != "" {
		return r.LocationAddress
	}
	if strings.TrimSpace(r.CalendarTitle) != "" {
		return r.CalendarTitle
	}
	return "calendar"
}

func (r eventRow) Snippet() string {
	parts := []string{r.Title()}
	if location := joinNonEmpty(r.LocationTitle, r.LocationAddress); location != "" {
		parts = append(parts, location)
	}
	return strings.Join(parts, " - ")
}

func (r eventRow) Calendar() CalendarProvenance {
	return CalendarProvenance{
		ID:         r.CalendarID,
		Title:      r.CalendarTitle,
		Type:       r.CalendarType,
		ExternalID: r.CalendarExternalID,
	}
}

func (r eventRow) Account() AccountProvenance {
	return AccountProvenance{Name: r.AccountName, Type: r.AccountType}
}

func (r eventRow) Location() *Location {
	if strings.TrimSpace(r.LocationTitle) == "" && strings.TrimSpace(r.LocationAddress) == "" {
		return nil
	}
	return &Location{Title: r.LocationTitle, Address: r.LocationAddress}
}

func (r eventRow) Attendees() ([]Attendee, error) {
	if strings.TrimSpace(r.AttendeesJSON) == "" {
		return nil, nil
	}
	var attendees []Attendee
	if err := json.Unmarshal([]byte(r.AttendeesJSON), &attendees); err != nil {
		return nil, err
	}
	return attendees, nil
}

func contactName(name, email, phone string) string {
	for _, value := range []string{name, email, phone} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func joinNonEmpty(values ...string) string {
	parts := []string{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			parts = append(parts, strings.TrimSpace(value))
		}
	}
	return strings.Join(parts, ", ")
}

func YearFromUnix(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return int64(time.Unix(value, 0).Local().Year())
}
