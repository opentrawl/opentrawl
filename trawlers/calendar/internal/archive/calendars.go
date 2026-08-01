package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) ListCalendarsWithActiveOrFutureEventCounts(
	ctx context.Context,
	activeOrFutureCalendarEventSelectionTime time.Time,
) ([]Calendar, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
       c.store_id, c.account_name, c.account_type, c.account_disabled,
       c.meaning, c.meaning_stated_at, count(e.event_uid),
       count(case when e.end_unix >= ? then e.event_uid end)
from calendars c
left join events e on e.calendar_id = c.calendar_id
group by c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
         c.store_id, c.account_name, c.account_type, c.account_disabled,
         c.meaning, c.meaning_stated_at
order by c.account_name, c.title, c.calendar_id`, activeOrFutureCalendarEventSelectionTime.Unix())
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	calendars := []Calendar{}
	for rows.Next() {
		calendar, err := scanCalendarWithActiveOrFutureEventCount(rows)
		if err != nil {
			return nil, err
		}
		calendars = append(calendars, calendar)
	}
	return calendars, rows.Err()
}

func (s *Store) SetCalendarOwnerOrPurposeAnnotation(
	ctx context.Context,
	calendarIdentifier CalendarIdentifier,
	annotation CalendarOwnerOrPurposeAnnotation,
) (Calendar, error) {
	calendarIdentifier = CalendarIdentifier(strings.TrimSpace(string(calendarIdentifier)))
	description := strings.TrimSpace(annotation.CalendarOwnerOrPurposeDescription)
	if calendarIdentifier == "" {
		return Calendar{}, fmt.Errorf("calendar identifier is required")
	}
	if description == "" {
		return Calendar{}, fmt.Errorf("calendar owner or purpose description cannot be empty")
	}
	if annotation.CalendarOwnerOrPurposeDescriptionStatedTime.IsZero() {
		return Calendar{}, fmt.Errorf("calendar owner or purpose description stated time is required")
	}
	descriptionStatedTime := annotation.CalendarOwnerOrPurposeDescriptionStatedTime.Format(time.RFC3339Nano)
	result, err := s.store.DB().ExecContext(ctx, `
update calendars
set meaning = ?, meaning_stated_at = ?
where calendar_id = ?`, description, descriptionStatedTime, string(calendarIdentifier))
	if err != nil {
		return Calendar{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Calendar{}, err
	}
	if changed == 0 {
		return Calendar{}, fmt.Errorf("calendar not found: %s", calendarIdentifier)
	}
	return s.Calendar(ctx, calendarIdentifier)
}

func (s *Store) Calendar(ctx context.Context, calendarIdentifier CalendarIdentifier) (Calendar, error) {
	row := s.store.DB().QueryRowContext(ctx, `
select c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
       c.store_id, c.account_name, c.account_type, c.account_disabled,
       c.meaning, c.meaning_stated_at, count(e.event_uid)
from calendars c
left join events e on e.calendar_id = c.calendar_id
where c.calendar_id = ?
group by c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
         c.store_id, c.account_name, c.account_type, c.account_disabled,
         c.meaning, c.meaning_stated_at`, strings.TrimSpace(string(calendarIdentifier)))
	calendar, err := scanCalendar(row)
	if err == sql.ErrNoRows {
		return Calendar{}, fmt.Errorf("calendar not found: %s", strings.TrimSpace(string(calendarIdentifier)))
	}
	return calendar, err
}

type calendarScanner interface {
	Scan(dest ...any) error
}

func scanCalendar(row calendarScanner) (Calendar, error) {
	var calendar Calendar
	var disabled int64
	var ownerOrPurposeDescription, ownerOrPurposeDescriptionStatedTime string
	if err := row.Scan(&calendar.ID, &calendar.SourceRowID, &calendar.Title, &calendar.Type, &calendar.ExternalID,
		&calendar.StoreID, &calendar.AccountName, &calendar.AccountType, &disabled, &ownerOrPurposeDescription,
		&ownerOrPurposeDescriptionStatedTime, &calendar.EventCount); err != nil {
		return Calendar{}, err
	}
	annotation, err := calendarOwnerOrPurposeAnnotationFromStoredValues(
		ownerOrPurposeDescription,
		ownerOrPurposeDescriptionStatedTime,
	)
	if err != nil {
		return Calendar{}, err
	}
	calendar.AccountDisabled = disabled != 0
	calendar.CalendarOwnerOrPurposeAnnotation = annotation
	return calendar, nil
}

func scanCalendarWithActiveOrFutureEventCount(row calendarScanner) (Calendar, error) {
	var calendar Calendar
	var disabled int64
	var ownerOrPurposeDescription, ownerOrPurposeDescriptionStatedTime string
	if err := row.Scan(&calendar.ID, &calendar.SourceRowID, &calendar.Title, &calendar.Type, &calendar.ExternalID,
		&calendar.StoreID, &calendar.AccountName, &calendar.AccountType, &disabled, &ownerOrPurposeDescription,
		&ownerOrPurposeDescriptionStatedTime, &calendar.EventCount, &calendar.ActiveOrFutureEventCount); err != nil {
		return Calendar{}, err
	}
	annotation, err := calendarOwnerOrPurposeAnnotationFromStoredValues(
		ownerOrPurposeDescription,
		ownerOrPurposeDescriptionStatedTime,
	)
	if err != nil {
		return Calendar{}, err
	}
	calendar.AccountDisabled = disabled != 0
	calendar.CalendarOwnerOrPurposeAnnotation = annotation
	return calendar, nil
}

func calendarOwnerOrPurposeAnnotationFromStoredValues(
	description string,
	descriptionStatedTime string,
) (*CalendarOwnerOrPurposeAnnotation, error) {
	description = strings.TrimSpace(description)
	descriptionStatedTime = strings.TrimSpace(descriptionStatedTime)
	if description == "" && descriptionStatedTime == "" {
		return nil, nil
	}
	if description == "" || descriptionStatedTime == "" {
		return nil, fmt.Errorf("calendar owner or purpose annotation is incomplete")
	}
	parsedDescriptionStatedTime, err := time.Parse(time.RFC3339Nano, descriptionStatedTime)
	if err != nil {
		return nil, fmt.Errorf("parse calendar owner or purpose annotation stated time: %w", err)
	}
	return &CalendarOwnerOrPurposeAnnotation{
		CalendarOwnerOrPurposeDescription:           description,
		CalendarOwnerOrPurposeDescriptionStatedTime: parsedDescriptionStatedTime,
	}, nil
}
