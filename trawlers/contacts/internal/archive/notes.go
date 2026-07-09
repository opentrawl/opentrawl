package archive

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func (s *Store) Notes(ctx context.Context, personID string) ([]model.Note, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select id, person_id, occurred_at, captured_at, kind, source, account,
       external_id, direction, confidence, topics_json, follow_up_at,
       privacy, body
from notes
where person_id = ?
order by occurred_at, id`, strings.TrimSpace(personID))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	notes := []model.Note{}
	for rows.Next() {
		note, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

func (s *Store) UpsertNote(ctx context.Context, note model.Note) (string, error) {
	note = canonicalNote(note)
	existing, err := s.note(ctx, note.ID)
	if err == sql.ErrNoRows {
		if err := s.saveNote(ctx, note); err != nil {
			return "", err
		}
		return "created", nil
	}
	if err != nil {
		return "", err
	}
	if reflect.DeepEqual(canonicalNote(existing), note) {
		return "unchanged", nil
	}
	if err := s.saveNote(ctx, note); err != nil {
		return "", err
	}
	return "updated", nil
}

func (s *Store) SaveNote(ctx context.Context, note model.Note) error {
	return s.saveNote(ctx, canonicalNote(note))
}

func (s *Store) note(ctx context.Context, id string) (model.Note, error) {
	row := s.store.DB().QueryRowContext(ctx, `
select id, person_id, occurred_at, captured_at, kind, source, account,
       external_id, direction, confidence, topics_json, follow_up_at,
       privacy, body
from notes
where id = ?`, strings.TrimSpace(id))
	return scanNote(row)
}

func (s *Store) saveNote(ctx context.Context, note model.Note) error {
	if strings.TrimSpace(note.ID) == "" {
		return fmt.Errorf("note id is required")
	}
	if strings.TrimSpace(note.PersonID) == "" {
		return fmt.Errorf("note person id is required")
	}
	_, err := s.store.DB().ExecContext(ctx, `
insert into notes(
  id, person_id, occurred_at, captured_at, kind, source, account,
  external_id, direction, confidence, topics_json, follow_up_at, privacy, body
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  person_id = excluded.person_id,
  occurred_at = excluded.occurred_at,
  captured_at = excluded.captured_at,
  kind = excluded.kind,
  source = excluded.source,
  account = excluded.account,
  external_id = excluded.external_id,
  direction = excluded.direction,
  confidence = excluded.confidence,
  topics_json = excluded.topics_json,
  follow_up_at = excluded.follow_up_at,
  privacy = excluded.privacy,
  body = excluded.body`,
		note.ID, note.PersonID, timeText(note.OccurredAt), timeText(note.CapturedAt), note.Kind,
		note.Source, note.Account, note.ExternalID, note.Direction, note.Confidence,
		mustJSONList(note.Topics), timeText(note.FollowUpAt), note.Privacy, note.Body)
	return err
}

func scanNote(row interface{ Scan(dest ...any) error }) (model.Note, error) {
	var note model.Note
	var occurredAt, capturedAt, topicsJSON, followUpAt string
	if err := row.Scan(&note.ID, &note.PersonID, &occurredAt, &capturedAt, &note.Kind, &note.Source,
		&note.Account, &note.ExternalID, &note.Direction, &note.Confidence, &topicsJSON,
		&followUpAt, &note.Privacy, &note.Body); err != nil {
		return model.Note{}, err
	}
	if err := decodeJSONList(topicsJSON, &note.Topics); err != nil {
		return model.Note{}, err
	}
	note.OccurredAt = parseTime(occurredAt)
	note.CapturedAt = parseTime(capturedAt)
	note.FollowUpAt = parseTime(followUpAt)
	return note, nil
}

func canonicalNote(note model.Note) model.Note {
	note.ID = strings.TrimSpace(note.ID)
	note.PersonID = strings.TrimSpace(note.PersonID)
	note.Kind = strings.TrimSpace(note.Kind)
	note.Source = strings.TrimSpace(note.Source)
	note.Account = strings.TrimSpace(note.Account)
	note.ExternalID = strings.TrimSpace(note.ExternalID)
	note.Direction = strings.TrimSpace(note.Direction)
	note.Confidence = strings.TrimSpace(note.Confidence)
	note.Privacy = strings.TrimSpace(note.Privacy)
	note.Path = ""
	note.Topics = cleanStrings(note.Topics)
	note.OccurredAt = note.OccurredAt.UTC()
	note.CapturedAt = note.CapturedAt.UTC()
	note.FollowUpAt = note.FollowUpAt.UTC()
	return note
}
