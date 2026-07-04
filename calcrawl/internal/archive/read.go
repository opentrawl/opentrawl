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

type SearchOptions struct {
	Limit  int
	After  int64
	Before int64
	Who    *WhoFilter
}

func (s *Store) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, int64, error) {
	query = strings.TrimSpace(query)
	ftsQuery := ""
	hasQuery := query != ""
	if hasQuery {
		var err error
		ftsQuery, err = store.FTS5Terms(query, "")
		if err != nil {
			return nil, 0, err
		}
	}
	where, args := searchWhere(ftsQuery, hasQuery, options.After, options.Before, options.Who)
	total, err := s.countSearch(ctx, where, args, hasQuery)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.store.DB().QueryContext(ctx, searchSQL(where, hasQuery), append(args, options.Limit)...)
	if err != nil {
		return nil, 0, err
	}
	results := []SearchResult{}
	for rows.Next() {
		var row eventRow
		if err := scanEventRow(rows, &row); err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		ref := RefForUID(row.UID)
		results = append(results, SearchResult{
			Ref:     ref,
			Time:    canonicalEventTime(row.Start),
			Who:     row.Who(),
			Where:   row.Where(),
			Snippet: row.Snippet(),
			AllDay:  row.AllDay != 0,
		})
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	for i := range results {
		shortRef, err := s.ShortRefForFullRef(ctx, results[i].Ref)
		if err != nil {
			return nil, 0, err
		}
		results[i].ShortRef = shortRef
	}
	return results, total, nil
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
		Start:                canonicalEventTime(row.Start),
		End:                  canonicalEventTime(row.End),
		AllDay:               row.AllDay != 0,
		Calendar:             row.CalendarTitle,
		Account:              row.AccountName,
		Location:             row.Location(),
		Organizer:            Person{DisplayName: row.OrganizerName, Email: row.OrganizerEmail, PhoneNumber: row.OrganizerPhone},
		Attendees:            attendees,
		URL:                  row.URL,
		Status:               NormalizeEventStatus(row.Status),
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

func (s *Store) countSearch(ctx context.Context, where string, args []any, hasQuery bool) (int64, error) {
	var total int64
	from := `events e`
	if hasQuery {
		from = `events_fts join events e on e.event_uid = events_fts.event_uid`
	}
	err := s.store.DB().QueryRowContext(ctx, `select count(*) from `+from+` `+where, args...).Scan(&total)
	return total, err
}

func searchWhere(ftsQuery string, hasQuery bool, after, before int64, who *WhoFilter) (string, []any) {
	parts := []string{}
	args := []any{}
	if hasQuery {
		parts = append(parts, "events_fts match ?")
		args = append(args, ftsQuery)
	}
	if after > 0 {
		parts = append(parts, "e.start_unix >= ?")
		args = append(args, after)
	}
	if before > 0 {
		parts = append(parts, "e.start_unix <= ?")
		args = append(args, before)
	}
	if who != nil {
		whoClause, whoArgs := whoWhere(who)
		if whoClause != "" {
			parts = append(parts, whoClause)
			args = append(args, whoArgs...)
		}
	}
	if len(parts) == 0 {
		return "", args
	}
	return "where " + strings.Join(parts, " and "), args
}

func whoWhere(who *WhoFilter) (string, []any) {
	clauses := []string{}
	args := []any{}
	values := uniqueStrings(who.Identifiers)
	if len(values) > 0 {
		clauses = append(clauses, "e.organizer_email in ("+valuePlaceholders(len(values))+")")
		args = appendValues(args, values)
		clauses = append(clauses, "e.organizer_phone in ("+valuePlaceholders(len(values))+")")
		args = appendValues(args, values)
		participantClauses := []string{
			"p.email in (" + valuePlaceholders(len(values)) + ")",
			"p.phone_number in (" + valuePlaceholders(len(values)) + ")",
			"p.address in (" + valuePlaceholders(len(values)) + ")",
		}
		args = appendValues(args, values)
		args = appendValues(args, values)
		args = appendValues(args, values)
		clauses = append(clauses, "exists (select 1 from participants p where p.event_uid = e.event_uid and ("+strings.Join(participantClauses, " or ")+"))")
	} else if strings.TrimSpace(who.Who) != "" {
		name := strings.TrimSpace(who.Who)
		clauses = append(clauses, "e.organizer_name = ?")
		args = append(args, name)
		clauses = append(clauses, "exists (select 1 from participants p where p.event_uid = e.event_uid and p.display_name = ?)")
		args = append(args, name)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "(" + strings.Join(clauses, " or ") + ")", args
}

func uniqueStrings(input []string) []string {
	values := []string{}
	seen := map[string]struct{}{}
	for _, item := range input {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func appendValues(args []any, values []string) []any {
	for _, value := range values {
		args = append(args, value)
	}
	return args
}

func valuePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	values := make([]string, count)
	for i := range values {
		values[i] = "?"
	}
	return strings.Join(values, ", ")
}

func searchSQL(where string, hasQuery bool) string {
	from := `events e`
	order := `e.start_unix desc, e.event_uid`
	if hasQuery {
		from = `events_fts
join events e on e.event_uid = events_fts.event_uid`
		order = `rank, e.start_unix desc, e.event_uid`
	}
	return `
select e.event_uid, e.uuid, e.unique_identifier, e.calendar_id, e.calendar_title,
       e.calendar_type, e.calendar_external_id, e.account_name, e.account_type,
       e.start_time, e.end_time, e.all_day, e.summary, e.description, e.status,
       e.url, e.has_recurrences, e.organizer_name, e.organizer_email,
       e.organizer_phone, e.location_title, e.location_address, e.attendees_json
from ` + from + `
` + where + `
order by ` + order + `
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
	if strings.TrimSpace(r.OrganizerPhone) != "" {
		return r.OrganizerPhone
	}
	attendees, err := r.Attendees()
	if err == nil && len(attendees) > 0 {
		for _, attendee := range attendees {
			for _, value := range []string{attendee.DisplayName, attendee.Email, attendee.PhoneNumber, attendee.Address} {
				if strings.TrimSpace(value) != "" {
					return strings.TrimSpace(value)
				}
			}
		}
	}
	// No organizer and no attendees means an event the owner put on
	// their own calendar.
	return "me"
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func canonicalEventTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return value
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return t.Format(time.RFC3339)
	}
	return value
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

func normalizeWho(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func YearFromUnix(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return int64(time.Unix(value, 0).Local().Year())
}
