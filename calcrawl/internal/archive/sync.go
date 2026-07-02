package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/openclaw/crawlkit/state"
)

func (s *Store) ApplySnapshot(ctx context.Context, calendars []Calendar, events []Event, runID string, syncedAt time.Time, sourcePath, sourceModifiedAt string) (SyncStats, error) {
	stats := SyncStats{
		Calendars:        len(calendars),
		Events:           len(events),
		SourcePath:       sourcePath,
		SourceModifiedAt: sourceModifiedAt,
		ArchivePath:      s.path,
		SyncedAt:         syncedAt.UTC().Format(time.RFC3339Nano),
	}
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, calendar := range calendars {
			if err := upsertCalendar(ctx, tx, calendar, runID); err != nil {
				return err
			}
		}
		for _, event := range events {
			result, err := upsertEvent(ctx, tx, event, runID)
			if err != nil {
				return err
			}
			switch result {
			case "new":
				stats.NewEvents++
			case "changed":
				stats.ChangedEvents++
			default:
				stats.UnchangedEvents++
			}
		}
		deleted, err := cleanupRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		stats.DeletedEvents = deleted
		if err := rebuildShortRefsTx(ctx, tx); err != nil {
			return err
		}
		stateStore := state.New(tx)
		if err := stateStore.Set(ctx, syncSource, syncEntity, syncStatus, completeState); err != nil {
			return err
		}
		if err := stateStore.Set(ctx, syncSource, syncEntity, syncRunID, runID); err != nil {
			return err
		}
		if err := stateStore.Set(ctx, syncSource, syncEntity, syncLastSync, stats.SyncedAt); err != nil {
			return err
		}
		return stateStore.Set(ctx, syncSource, syncEntity, syncSourceModified, sourceModifiedAt)
	})
	return stats, err
}

func upsertCalendar(ctx context.Context, tx *sql.Tx, calendar Calendar, runID string) error {
	_, err := tx.ExecContext(ctx, `
insert into calendars(
  calendar_id, source_row_id, title, type, external_id, store_id,
  account_name, account_type, account_disabled, sync_run_id
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(calendar_id) do update set
  source_row_id = excluded.source_row_id,
  title = excluded.title,
  type = excluded.type,
  external_id = excluded.external_id,
  store_id = excluded.store_id,
  account_name = excluded.account_name,
  account_type = excluded.account_type,
  account_disabled = excluded.account_disabled,
  sync_run_id = excluded.sync_run_id
`, calendar.ID, calendar.SourceRowID, calendar.Title, calendar.Type, calendar.ExternalID, calendar.StoreID,
		calendar.AccountName, calendar.AccountType, boolInt(calendar.AccountDisabled), runID)
	if err != nil {
		return fmt.Errorf("upsert calendar: %w", err)
	}
	return nil
}

func upsertEvent(ctx context.Context, tx *sql.Tx, event Event, runID string) (string, error) {
	if event.UID == "" {
		return "", fmt.Errorf("event is missing unique id")
	}
	fingerprint := event.Fingerprint()
	result := "unchanged"
	var existing string
	err := tx.QueryRowContext(ctx, `select fingerprint from events where event_uid = ?`, event.UID).Scan(&existing)
	if err == sql.ErrNoRows {
		result = "new"
	} else if err != nil {
		return "", err
	} else if existing != fingerprint {
		result = "changed"
	}
	attendeesJSON, err := json.Marshal(event.Attendees)
	if err != nil {
		return "", fmt.Errorf("marshal attendees: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
insert into events(
  event_uid, source_row_id, uuid, unique_identifier, calendar_id, calendar_title,
  calendar_type, calendar_external_id, account_name, account_type, start_time,
  end_time, start_unix, end_unix, all_day, summary, description, status, url,
  has_recurrences, organizer_name, organizer_email, organizer_phone,
  location_title, location_address, attendees_json, participants_text,
  fingerprint, sync_run_id
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(event_uid) do update set
  source_row_id = excluded.source_row_id,
  uuid = excluded.uuid,
  unique_identifier = excluded.unique_identifier,
  calendar_id = excluded.calendar_id,
  calendar_title = excluded.calendar_title,
  calendar_type = excluded.calendar_type,
  calendar_external_id = excluded.calendar_external_id,
  account_name = excluded.account_name,
  account_type = excluded.account_type,
  start_time = excluded.start_time,
  end_time = excluded.end_time,
  start_unix = excluded.start_unix,
  end_unix = excluded.end_unix,
  all_day = excluded.all_day,
  summary = excluded.summary,
  description = excluded.description,
  status = excluded.status,
  url = excluded.url,
  has_recurrences = excluded.has_recurrences,
  organizer_name = excluded.organizer_name,
  organizer_email = excluded.organizer_email,
  organizer_phone = excluded.organizer_phone,
  location_title = excluded.location_title,
  location_address = excluded.location_address,
  attendees_json = excluded.attendees_json,
  participants_text = excluded.participants_text,
  fingerprint = excluded.fingerprint,
  sync_run_id = excluded.sync_run_id
`, event.UID, event.SourceRowID, event.UUID, event.UniqueIdentifier, event.Calendar.ID, event.Calendar.Title,
		event.Calendar.Type, event.Calendar.ExternalID, event.Account.Name, event.Account.Type, event.Start,
		event.End, event.StartUnix, event.EndUnix, boolInt(event.AllDay), event.Summary, event.Description,
		event.Status, event.URL, boolInt(event.HasRecurrences), event.Organizer.DisplayName, event.Organizer.Email,
		event.Organizer.PhoneNumber, event.Location.Title, event.Location.Address, string(attendeesJSON),
		event.ParticipantsText, fingerprint, runID)
	if err != nil {
		return "", fmt.Errorf("upsert event: %w", err)
	}
	if err := replaceParticipants(ctx, tx, event, runID); err != nil {
		return "", err
	}
	if err := replaceLocation(ctx, tx, event, runID); err != nil {
		return "", err
	}
	if err := replaceFTS(ctx, tx, event); err != nil {
		return "", err
	}
	return result, nil
}

func replaceParticipants(ctx context.Context, tx *sql.Tx, event Event, runID string) error {
	if _, err := tx.ExecContext(ctx, `delete from participants where event_uid = ?`, event.UID); err != nil {
		return err
	}
	for i, attendee := range event.Attendees {
		_, err := tx.ExecContext(ctx, `
insert into participants(
  event_uid, position, display_name, email, phone_number, address,
  rsvp_status, role, is_self, comment, sync_run_id
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			event.UID, i, attendee.DisplayName, attendee.Email, attendee.PhoneNumber, attendee.Address,
			attendee.RSVPStatus, attendee.Role, boolInt(attendee.Self), attendee.Comment, runID)
		if err != nil {
			return fmt.Errorf("insert participant: %w", err)
		}
	}
	return nil
}

func replaceLocation(ctx context.Context, tx *sql.Tx, event Event, runID string) error {
	if _, err := tx.ExecContext(ctx, `delete from locations where event_uid = ?`, event.UID); err != nil {
		return err
	}
	if event.Location.Title == "" && event.Location.Address == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `insert into locations(event_uid, title, address, sync_run_id) values (?, ?, ?, ?)`,
		event.UID, event.Location.Title, event.Location.Address, runID)
	return err
}

func replaceFTS(ctx context.Context, tx *sql.Tx, event Event) error {
	if _, err := tx.ExecContext(ctx, `delete from events_fts where event_uid = ?`, event.UID); err != nil {
		return fmt.Errorf("delete event fts: %w", err)
	}
	_, err := tx.ExecContext(ctx, `
insert into events_fts(event_uid, summary, description, location, participants)
values (?, ?, ?, ?, ?)`,
		event.UID, event.Summary, event.Description, event.LocationSearchText, event.ParticipantsText)
	if err != nil {
		return fmt.Errorf("insert event fts: %w", err)
	}
	return nil
}

func cleanupRun(ctx context.Context, tx *sql.Tx, runID string) (int, error) {
	var deleted int
	if err := tx.QueryRowContext(ctx, `select count(*) from events where sync_run_id <> ?`, runID).Scan(&deleted); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `delete from events_fts where event_uid in (select event_uid from events where sync_run_id <> ?)`, runID); err != nil {
		return 0, err
	}
	for _, table := range []string{"participants", "locations", "events", "calendars"} {
		if _, err := tx.ExecContext(ctx, `delete from `+table+` where sync_run_id <> ?`, runID); err != nil {
			return 0, err
		}
	}
	return deleted, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
