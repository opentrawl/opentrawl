package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var ErrEventNotFound = errors.New("calendar event not found")

func (s *Store) Status(ctx context.Context) (Status, error) {
	var out Status
	out.ArchivePath = s.path
	out.ArchiveBytes = fileSize(s.path)
	db := s.store.DB()
	archiveCalendarCount, err := countCalendarsContainingArchivedEvents(ctx, db)
	if err != nil {
		return Status{}, err
	}
	out.Calendars = archiveCalendarCount
	archiveEventCount, err := countTable(ctx, db, "events")
	if err != nil {
		return Status{}, err
	}
	out.Events = archiveEventCount
	_ = db.QueryRowContext(ctx, `select coalesce(min(start_unix), 0), coalesce(max(start_unix), 0) from events`).Scan(&out.EarliestUnix, &out.LatestUnix)
	stateStore := state.New(db)
	if rec, ok, err := stateStore.Get(ctx, updateSource, updateEntity, updateLastUpdate); err == nil && ok {
		out.LastUpdateAt = rec.Value
	}
	if rec, ok, err := stateStore.Get(ctx, updateSource, updateEntity, updateSourceModified); err == nil && ok {
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

func (s *Store) ListUpcomingEvents(
	ctx context.Context,
	now time.Time,
	limit int,
	calendarDisplayNameFilter string,
	calendarAccountDisplayNameFilter string,
) ([]EventListItem, error) {
	if limit <= 0 {
		limit = -1
	}
	nowUnix := now.Unix()
	rows, err := s.store.DB().QueryContext(ctx, `
select event_uid, start_time, all_day, summary, calendar_title,
       location_title, location_address, organizer_name, organizer_email,
       organizer_phone, attendees_json
from events
where start_unix >= ?
  and (? = '' or calendar_title = ?)
  and (? = '' or account_name = ?)
order by start_unix, event_uid
limit ?`,
		nowUnix,
		strings.TrimSpace(calendarDisplayNameFilter),
		strings.TrimSpace(calendarDisplayNameFilter),
		strings.TrimSpace(calendarAccountDisplayNameFilter),
		strings.TrimSpace(calendarAccountDisplayNameFilter),
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []EventListItem{}
	for rows.Next() {
		var item EventListItem
		var uid, locationTitle, locationAddress, attendeesJSON string
		var allDay int
		if err := rows.Scan(
			&uid,
			&item.Start,
			&allDay,
			&item.Title,
			&item.Calendar,
			&locationTitle,
			&locationAddress,
			&item.Organizer.DisplayName,
			&item.Organizer.Email,
			&item.Organizer.PhoneNumber,
			&attendeesJSON,
		); err != nil {
			return nil, err
		}
		item.Ref = RefForUID(uid)
		item.AllDay = allDay != 0
		if strings.TrimSpace(locationTitle) != "" || strings.TrimSpace(locationAddress) != "" {
			item.Location = &Location{Title: locationTitle, Address: locationAddress}
		}
		if err := json.Unmarshal([]byte(attendeesJSON), &item.Attendees); err != nil {
			return nil, fmt.Errorf("decode event attendees: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
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
	limitArg := options.Limit
	if limitArg <= 0 {
		limitArg = -1 // SQLite: no limit for internal unbounded callers.
	}
	searchStartedAtUnix := time.Now().Unix()
	rows, err := s.store.DB().QueryContext(ctx, searchSQL(where, hasQuery), append(args, searchStartedAtUnix, limitArg)...)
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
		attendees, err := row.Attendees()
		if err != nil {
			_ = rows.Close()
			return nil, 0, err
		}
		ref := RefForUID(row.UID)
		results = append(results, SearchResult{
			Ref:          ref,
			Time:         canonicalEventTime(row.Start),
			Title:        row.Summary,
			Calendar:     row.CalendarTitle,
			Account:      row.AccountName,
			Location:     row.Location(),
			Organizer:    Person{DisplayName: row.OrganizerName, Email: row.OrganizerEmail, PhoneNumber: row.OrganizerPhone},
			Attendees:    attendees,
			AllDay:       row.AllDay != 0,
			Availability: row.AvailabilityPtr(),
			Matches:      row.SearchMatches(),
		})
	}
	if err := rows.Close(); err != nil {
		return nil, 0, err
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return results, total, nil
}

func (s *Store) OpenEvent(ctx context.Context, ref string) (EventDetail, error) {
	uid, ok := UIDFromRef(ref)
	if !ok {
		return EventDetail{}, fmt.Errorf("invalid calendar event ref %q", ref)
	}
	row := eventRow{}
	err := s.store.DB().QueryRowContext(ctx, `
select event_uid, uuid, unique_identifier, calendar_id, calendar_title, calendar_type,
       calendar_external_id, account_name, account_type, start_time, end_time, all_day,
       summary, description, status, url, has_recurrences, availability, organizer_name,
       organizer_email, organizer_phone, location_title, location_address, attendees_json
from events
where event_uid = ?`, uid).Scan(&row.UID, &row.UUID, &row.UniqueIdentifier, &row.CalendarID,
		&row.CalendarTitle, &row.CalendarType, &row.CalendarExternalID, &row.AccountName,
		&row.AccountType, &row.Start, &row.End, &row.AllDay, &row.Summary, &row.Description,
		&row.Status, &row.URL, &row.HasRecurrences, &row.Availability, &row.OrganizerName, &row.OrganizerEmail,
		&row.OrganizerPhone, &row.LocationTitle, &row.LocationAddress, &row.AttendeesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return EventDetail{}, fmt.Errorf("%w: %s", ErrEventNotFound, ref)
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
		Availability:         row.AvailabilityPtr(),
		Location:             row.Location(),
		Organizer:            Person{DisplayName: row.OrganizerName, Email: row.OrganizerEmail, PhoneNumber: row.OrganizerPhone},
		Attendees:            attendees,
		URL:                  row.URL,
		Status:               NormalizeEventStatus(row.Status),
		HasRecurrences:       row.HasRecurrences != 0,
	}, nil
}

func (s *Store) ExportContacts(ctx context.Context) ([]*person.TrawlerPersonIdentity, error) {
	peopleWithCalendarActivity, err := s.WhoCandidates(ctx)
	if err != nil {
		return nil, err
	}
	personIdentities := make([]*person.TrawlerPersonIdentity, 0, len(peopleWithCalendarActivity))
	for _, personWithCalendarActivity := range peopleWithCalendarActivity {
		personDisplayName := strings.Join(strings.Fields(personWithCalendarActivity.Who), " ")
		if personDisplayName == "" ||
			whomatch.IsIdentifierLike(personDisplayName, personWithCalendarActivity.Identifiers) ||
			len(personWithCalendarActivity.filterIdentifiers) == 0 {
			continue
		}
		personIdentifierWithinTrawlerArchive := calendarPersonIdentifierWithinTrawlerArchive(
			personWithCalendarActivity.filterIdentifiers[0],
		)
		if personIdentifierWithinTrawlerArchive == "" {
			continue
		}
		personIdentity := &person.TrawlerPersonIdentity{
			PersonIdentifierWithinTrawlerArchive: personIdentifierWithinTrawlerArchive,
			PersonDisplayName:                    personDisplayName,
		}
		for _, identifier := range personWithCalendarActivity.Identifiers {
			identifier = strings.TrimSpace(identifier)
			switch {
			case identifier == "":
			case strings.Contains(identifier, "@"):
				personIdentity.PersonEmailAddresses = append(
					personIdentity.PersonEmailAddresses,
					strings.ToLower(identifier),
				)
			case identifierRank(identifier) == 1:
				personIdentity.PersonPhoneNumbers = append(personIdentity.PersonPhoneNumbers, identifier)
			default:
				if personIdentity.PersonAccountIdentifiersByServiceName == nil {
					personIdentity.PersonAccountIdentifiersByServiceName =
						map[string]*person.TrawlerPersonAccountIdentifiers{}
				}
				personIdentity.PersonAccountIdentifiersByServiceName["calendar"] =
					&person.TrawlerPersonAccountIdentifiers{
						PersonAccountIdentifiers: append(
							personIdentity.PersonAccountIdentifiersByServiceName["calendar"].GetPersonAccountIdentifiers(),
							identifier,
						),
					}
			}
		}
		if latestCalendarRecordTime, err := time.Parse(time.RFC3339Nano, personWithCalendarActivity.LastSeen); err == nil {
			personIdentity.LatestArchiveRecordTimeInvolvingPersonInTrawlerArchive =
				timestamppb.New(latestCalendarRecordTime)
		}
		personIdentities = append(personIdentities, personIdentity)
	}
	return personIdentities, nil
}

func calendarPersonIdentifierWithinTrawlerArchive(identifier string) string {
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" {
		return ""
	}
	return "calendar:" + identifier
}

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, `select count(*) from `+store.QuoteIdent(table)).Scan(&count)
	return count, err
}

func countCalendarsContainingArchivedEvents(ctx context.Context, db *sql.DB) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, `
select count(distinct c.calendar_id)
from calendars c
join events e on e.calendar_id = c.calendar_id`).Scan(&count)
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
	if values := uniqueStrings(who.Identifiers); len(values) > 0 {
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
	}
	// Names are only a fallback for archive rows that contain no stable
	// identifier. Exact identifiers must not expand into unrelated people who
	// have the same display name.
	if len(clauses) == 0 {
		names := uniqueStrings(who.Names)
		if len(names) > 0 {
			clauses = append(clauses, "e.organizer_name in ("+valuePlaceholders(len(names))+")")
			args = appendValues(args, names)
			clauses = append(clauses, "exists (select 1 from participants p where p.event_uid = e.event_uid and p.display_name in ("+valuePlaceholders(len(names))+"))")
			args = appendValues(args, names)
		}
	}
	if len(clauses) == 0 {
		// The entity owns no identifier and no display name of its own — a
		// nameless shared-mailbox cluster, which --who refuses as ambiguous
		// before search reaches here. Fall back to its label (an identifier
		// string in this case, so no cross-entity collision) rather than let
		// the filter silently become match-all.
		if name := strings.TrimSpace(who.Who); name != "" {
			clauses = append(clauses,
				"e.organizer_name = ?",
				"exists (select 1 from participants p where p.event_uid = e.event_uid and p.display_name = ?)")
			args = append(args, name, name)
		}
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
	order := `case when e.start_unix < ? then -e.start_unix else e.start_unix end, e.event_uid`
	matchColumns := `'' as summary_match, '' as description_match, '' as location_match, '' as participants_match`
	if hasQuery {
		from = `events_fts
join events e on e.event_uid = events_fts.event_uid`
		order = `rank, ` + order
		matchColumns = "\n" +
			store.FTS5MarkedSearchResultSnippetSQLExpression("events_fts", 1) + " as summary_match,\n" +
			store.FTS5MarkedSearchResultSnippetSQLExpression("events_fts", 2) + " as description_match,\n" +
			store.FTS5MarkedSearchResultSnippetSQLExpression("events_fts", 3) + " as location_match,\n" +
			store.FTS5MarkedSearchResultSnippetSQLExpression("events_fts", 4) + " as participants_match"
	}
	return `
select e.event_uid, e.uuid, e.unique_identifier, e.calendar_id, e.calendar_title,
       e.calendar_type, e.calendar_external_id, e.account_name, e.account_type,
       e.start_time, e.end_time, e.all_day, e.summary, e.description, e.status,
       e.url, e.has_recurrences, e.availability, e.organizer_name, e.organizer_email,
       e.organizer_phone, e.location_title, e.location_address, e.attendees_json,
       ` + matchColumns + `
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
	Availability       sql.NullInt64
	OrganizerName      string
	OrganizerEmail     string
	OrganizerPhone     string
	LocationTitle      string
	LocationAddress    string
	AttendeesJSON      string
	SummaryMatch       string
	DescriptionMatch   string
	LocationMatch      string
	ParticipantsMatch  string
}

func scanEventRow(rows *sql.Rows, row *eventRow) error {
	return rows.Scan(&row.UID, &row.UUID, &row.UniqueIdentifier, &row.CalendarID, &row.CalendarTitle,
		&row.CalendarType, &row.CalendarExternalID, &row.AccountName, &row.AccountType,
		&row.Start, &row.End, &row.AllDay, &row.Summary, &row.Description, &row.Status,
		&row.URL, &row.HasRecurrences, &row.Availability, &row.OrganizerName, &row.OrganizerEmail,
		&row.OrganizerPhone, &row.LocationTitle, &row.LocationAddress, &row.AttendeesJSON,
		&row.SummaryMatch, &row.DescriptionMatch, &row.LocationMatch, &row.ParticipantsMatch)
}

func (r eventRow) SearchMatches() []SearchMatch {
	values := []struct {
		field string
		value string
	}{
		{field: "summary", value: r.SummaryMatch},
		{field: "description", value: r.DescriptionMatch},
		{field: "location", value: r.LocationMatch},
		{field: "participant", value: r.ParticipantsMatch},
	}
	matches := make([]SearchMatch, 0, len(values))
	for _, value := range values {
		runs := store.ParseFTS5MarkedText(value.value)
		if len(runs) == 0 {
			continue
		}
		if value.field == "participant" {
			matches = append(matches, SearchMatch{Field: value.field})
			continue
		}
		matches = append(matches, SearchMatch{Field: value.field, Runs: runs})
	}
	return matches
}

func (r eventRow) Title() string {
	if strings.TrimSpace(r.Summary) != "" {
		return strings.TrimSpace(r.Summary)
	}
	return "(untitled event)"
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

func (r eventRow) AvailabilityPtr() *int64 {
	if !r.Availability.Valid {
		return nil
	}
	value := r.Availability.Int64
	return &value
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

func YearFromUnix(value int64) int64 {
	if value <= 0 {
		return 0
	}
	return int64(time.Unix(value, 0).Local().Year())
}
