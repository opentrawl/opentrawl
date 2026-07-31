package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const CursorSchema = `
create table if not exists update_cursor_state (
  source text not null,
  entity_type text not null,
  entity_id text not null,
  cursor text not null,
  updated_at text not null,
  primary key (source, entity_type, entity_id)
);
create index if not exists idx_update_cursor_state_updated_at on update_cursor_state(updated_at desc);
`

type CursorStore struct {
	db      execQuerier
	now     func() time.Time
	mapping CursorMapping
}

type CursorMapping struct {
	Table      string
	Source     string
	EntityType string
	EntityID   string
	Cursor     string
	UpdatedAt  string
}

type CursorRecord struct {
	Source     string    `json:"source"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Cursor     string    `json:"cursor"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func EnsureCursorSchema(ctx context.Context, db execQuerier) error {
	if _, err := db.ExecContext(ctx, CursorSchema); err != nil {
		return fmt.Errorf("ensure cursor update state schema: %w", err)
	}
	return nil
}

func NewCursor(db execQuerier) *CursorStore {
	return NewCursorWithClock(db, nil)
}

func NewCursorWithClock(db execQuerier, now func() time.Time) *CursorStore {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &CursorStore{db: db, now: now, mapping: defaultCursorMapping()}
}

func NewCursorMapped(db execQuerier, mapping CursorMapping) (*CursorStore, error) {
	mapping, err := normalizeCursorMapping(mapping)
	if err != nil {
		return nil, err
	}
	return &CursorStore{db: db, now: func() time.Time { return time.Now().UTC() }, mapping: mapping}, nil
}

func (s *CursorStore) Set(ctx context.Context, source, entityType, entityID, cursor string) error {
	updatedAt := s.now().UTC()
	m := s.mapping
	query := fmt.Sprintf(`
insert into %s(%s, %s, %s, %s, %s)
values (?, ?, ?, ?, ?)
on conflict(%s, %s, %s) do update set
  %s = excluded.%s,
  %s = excluded.%s
`, quote(m.Table), quote(m.Source), quote(m.EntityType), quote(m.EntityID), quote(m.Cursor), quote(m.UpdatedAt), quote(m.Source), quote(m.EntityType), quote(m.EntityID), quote(m.Cursor), quote(m.Cursor), quote(m.UpdatedAt), quote(m.UpdatedAt))
	_, err := s.db.ExecContext(ctx, query, source, entityType, entityID, cursor, updatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set cursor update state: %w", err)
	}
	return nil
}

func (s *CursorStore) Get(ctx context.Context, source, entityType, entityID string) (CursorRecord, bool, error) {
	var rec CursorRecord
	var updatedAt string
	m := s.mapping
	query := fmt.Sprintf("select %s, %s, %s, %s, %s from %s where %s = ? and %s = ? and %s = ?", quote(m.Source), quote(m.EntityType), quote(m.EntityID), quote(m.Cursor), quote(m.UpdatedAt), quote(m.Table), quote(m.Source), quote(m.EntityType), quote(m.EntityID))
	err := s.db.QueryRowContext(ctx, query, source, entityType, entityID).Scan(&rec.Source, &rec.EntityType, &rec.EntityID, &rec.Cursor, &updatedAt)
	if err == sql.ErrNoRows {
		return CursorRecord{}, false, nil
	}
	if err != nil {
		return CursorRecord{}, false, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return CursorRecord{}, false, fmt.Errorf("parse cursor update state updated_at: %w", err)
	}
	rec.UpdatedAt = parsed
	return rec, true, nil
}

func (s *CursorStore) IsStale(ctx context.Context, source, entityType, entityID string, maxAge time.Duration) (bool, error) {
	rec, ok, err := s.Get(ctx, source, entityType, entityID)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	if maxAge <= 0 {
		return false, nil
	}
	return s.now().UTC().Sub(rec.UpdatedAt) > maxAge, nil
}

func defaultCursorMapping() CursorMapping {
	return CursorMapping{Table: "update_cursor_state", Source: "source", EntityType: "entity_type", EntityID: "entity_id", Cursor: "cursor", UpdatedAt: "updated_at"}
}

func normalizeCursorMapping(mapping CursorMapping) (CursorMapping, error) {
	if mapping == (CursorMapping{}) {
		mapping = defaultCursorMapping()
	}
	if err := validateIdentifiers(mapping.Table, mapping.Source, mapping.EntityType, mapping.EntityID, mapping.Cursor, mapping.UpdatedAt); err != nil {
		return CursorMapping{}, err
	}
	return mapping, nil
}

func validateIdentifiers(values ...string) error {
	for _, value := range values {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\"\x00") {
			return fmt.Errorf("invalid update state identifier %q", value)
		}
	}
	return nil
}

func quote(value string) string {
	return `"` + value + `"`
}
