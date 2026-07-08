package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) Calendars(ctx context.Context) ([]Calendar, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
       c.store_id, c.account_name, c.account_type, c.account_disabled,
       c.meaning, c.meaning_stated_at, count(e.event_uid)
from calendars c
left join events e on e.calendar_id = c.calendar_id
group by c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
         c.store_id, c.account_name, c.account_type, c.account_disabled,
         c.meaning, c.meaning_stated_at
order by c.account_name, c.title, c.calendar_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	calendars := []Calendar{}
	for rows.Next() {
		calendar, err := scanCalendar(rows)
		if err != nil {
			return nil, err
		}
		calendars = append(calendars, calendar)
	}
	return calendars, rows.Err()
}

func (s *Store) SetCalendarMeaning(ctx context.Context, calendarID, meaning, statedAt string) (Calendar, error) {
	calendarID = strings.TrimSpace(calendarID)
	statedAt = strings.TrimSpace(statedAt)
	if calendarID == "" {
		return Calendar{}, fmt.Errorf("calendar id is required")
	}
	if meaning == "" {
		return Calendar{}, fmt.Errorf("calendar meaning cannot be empty")
	}
	if statedAt == "" {
		return Calendar{}, fmt.Errorf("calendar meaning stated date is required")
	}
	result, err := s.store.DB().ExecContext(ctx, `
update calendars
set meaning = ?, meaning_stated_at = ?
where calendar_id = ?`, meaning, statedAt, calendarID)
	if err != nil {
		return Calendar{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return Calendar{}, err
	}
	if changed == 0 {
		return Calendar{}, fmt.Errorf("calendar not found: %s", calendarID)
	}
	return s.Calendar(ctx, calendarID)
}

func (s *Store) Calendar(ctx context.Context, calendarID string) (Calendar, error) {
	row := s.store.DB().QueryRowContext(ctx, `
select c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
       c.store_id, c.account_name, c.account_type, c.account_disabled,
       c.meaning, c.meaning_stated_at, count(e.event_uid)
from calendars c
left join events e on e.calendar_id = c.calendar_id
where c.calendar_id = ?
group by c.calendar_id, c.source_row_id, c.title, c.type, c.external_id,
         c.store_id, c.account_name, c.account_type, c.account_disabled,
         c.meaning, c.meaning_stated_at`, strings.TrimSpace(calendarID))
	calendar, err := scanCalendar(row)
	if err == sql.ErrNoRows {
		return Calendar{}, fmt.Errorf("calendar not found: %s", strings.TrimSpace(calendarID))
	}
	return calendar, err
}

type calendarScanner interface {
	Scan(dest ...any) error
}

func scanCalendar(row calendarScanner) (Calendar, error) {
	var calendar Calendar
	var disabled int64
	if err := row.Scan(&calendar.ID, &calendar.SourceRowID, &calendar.Title, &calendar.Type, &calendar.ExternalID,
		&calendar.StoreID, &calendar.AccountName, &calendar.AccountType, &disabled, &calendar.Meaning,
		&calendar.MeaningStatedAt, &calendar.EventCount); err != nil {
		return Calendar{}, err
	}
	calendar.AccountDisabled = disabled != 0
	return calendar, nil
}
