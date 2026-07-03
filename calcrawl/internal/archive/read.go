package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	Who    []WhoMatch
}

func (s *Store) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, int64, error) {
	ftsQuery, err := store.FTS5Terms(query, "")
	if err != nil {
		return nil, 0, err
	}
	where, args := searchWhere(ftsQuery, options.After, options.Before, options.Who)
	total, err := s.countSearch(ctx, where, args)
	if err != nil {
		return nil, 0, err
	}
	rows, err := s.store.DB().QueryContext(ctx, searchSQL(where), append(args, options.Limit)...)
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

func (s *Store) ResolveWho(ctx context.Context, identity string) ([]WhoMatch, error) {
	identity = normalizeWho(identity)
	if identity == "" {
		return nil, nil
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select display_name, email, phone_number, address from (
  select distinct trim(organizer_name) as display_name,
         trim(organizer_email) as email,
         trim(organizer_phone) as phone_number,
         '' as address
  from events
  where trim(organizer_name) <> '' or trim(organizer_email) <> '' or trim(organizer_phone) <> ''
  union
  select distinct trim(display_name) as display_name,
         trim(email) as email,
         trim(phone_number) as phone_number,
         trim(address) as address
  from participants
  where trim(display_name) <> '' or trim(email) <> '' or trim(phone_number) <> '' or trim(address) <> ''
)
order by display_name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	matches := []WhoMatch{}
	seen := map[string]int{}
	for rows.Next() {
		var candidate WhoMatch
		if err := rows.Scan(&candidate.DisplayName, &candidate.Email, &candidate.PhoneNumber, &candidate.Address); err != nil {
			return nil, err
		}
		candidate = cleanWhoMatch(candidate)
		if !identityMatches(identity, candidate.DisplayName, candidate.Email, candidate.PhoneNumber, candidate.Address) {
			continue
		}
		key := whoMatchKey(candidate)
		if index, ok := seen[key]; ok {
			matches[index] = mergeWhoMatch(matches[index], candidate)
			continue
		}
		seen[key] = len(matches)
		matches = append(matches, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		left := strings.ToLower(whoLabel(matches[i]))
		right := strings.ToLower(whoLabel(matches[j]))
		if left != right {
			return left < right
		}
		return whoMatchKey(matches[i]) < whoMatchKey(matches[j])
	})
	return matches, nil
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

func (s *Store) countSearch(ctx context.Context, where string, args []any) (int64, error) {
	var total int64
	err := s.store.DB().QueryRowContext(ctx, `select count(*) from events_fts join events e on e.event_uid = events_fts.event_uid `+where, args...).Scan(&total)
	return total, err
}

func searchWhere(ftsQuery string, after, before int64, who []WhoMatch) (string, []any) {
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
	if len(who) > 0 {
		whoClause, whoArgs := whoWhere(who)
		if whoClause != "" {
			parts = append(parts, whoClause)
			args = append(args, whoArgs...)
		}
	}
	return strings.Join(parts, " and "), args
}

func whoWhere(who []WhoMatch) (string, []any) {
	names := uniqueWhoValues(who, func(item WhoMatch) string { return item.DisplayName })
	emails := uniqueWhoValues(who, func(item WhoMatch) string { return item.Email })
	phones := uniqueWhoValues(who, func(item WhoMatch) string { return item.PhoneNumber })
	addresses := uniqueWhoValues(who, func(item WhoMatch) string { return item.Address })

	clauses := []string{}
	args := []any{}
	if len(names) > 0 {
		clauses = append(clauses, "e.organizer_name in ("+valuePlaceholders(len(names))+")")
		args = appendValues(args, names)
	}
	if len(emails) > 0 {
		clauses = append(clauses, "e.organizer_email in ("+valuePlaceholders(len(emails))+")")
		args = appendValues(args, emails)
	}
	if len(phones) > 0 {
		clauses = append(clauses, "e.organizer_phone in ("+valuePlaceholders(len(phones))+")")
		args = appendValues(args, phones)
	}

	participantClauses := []string{}
	if len(names) > 0 {
		participantClauses = append(participantClauses, "p.display_name in ("+valuePlaceholders(len(names))+")")
		args = appendValues(args, names)
	}
	if len(emails) > 0 {
		participantClauses = append(participantClauses, "p.email in ("+valuePlaceholders(len(emails))+")")
		args = appendValues(args, emails)
	}
	if len(phones) > 0 {
		participantClauses = append(participantClauses, "p.phone_number in ("+valuePlaceholders(len(phones))+")")
		args = appendValues(args, phones)
	}
	if len(addresses) > 0 {
		participantClauses = append(participantClauses, "p.address in ("+valuePlaceholders(len(addresses))+")")
		args = appendValues(args, addresses)
	}
	if len(participantClauses) > 0 {
		clauses = append(clauses, "exists (select 1 from participants p where p.event_uid = e.event_uid and ("+strings.Join(participantClauses, " or ")+"))")
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return "(" + strings.Join(clauses, " or ") + ")", args
}

func uniqueWhoValues(who []WhoMatch, pick func(WhoMatch) string) []string {
	values := []string{}
	seen := map[string]struct{}{}
	for _, item := range who {
		value := strings.TrimSpace(pick(item))
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

func cleanWhoMatch(match WhoMatch) WhoMatch {
	return WhoMatch{
		DisplayName: strings.TrimSpace(match.DisplayName),
		Email:       strings.TrimSpace(match.Email),
		PhoneNumber: strings.TrimSpace(match.PhoneNumber),
		Address:     strings.TrimSpace(match.Address),
	}
}

func mergeWhoMatch(left, right WhoMatch) WhoMatch {
	return WhoMatch{
		DisplayName: firstNonEmpty(left.DisplayName, right.DisplayName),
		Email:       firstNonEmpty(left.Email, right.Email),
		PhoneNumber: firstNonEmpty(left.PhoneNumber, right.PhoneNumber),
		Address:     firstNonEmpty(left.Address, right.Address),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func identityMatches(identity string, values ...string) bool {
	for _, value := range values {
		if strings.EqualFold(normalizeWho(value), identity) {
			return true
		}
	}
	return false
}

func whoLabel(match WhoMatch) string {
	for _, value := range []string{match.DisplayName, match.Email, match.PhoneNumber, match.Address} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}

func whoMatchKey(match WhoMatch) string {
	return strings.Join([]string{match.DisplayName, match.Email, match.PhoneNumber}, "\x00")
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
