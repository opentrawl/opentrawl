package shortref

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const Schema = `
create table if not exists short_refs (
  alias text not null,
  full_ref text not null,
  primary key (alias, full_ref)
);
create index if not exists idx_short_refs_alias on short_refs(alias);
create index if not exists idx_short_refs_full_ref on short_refs(full_ref);
`

type SQLiteDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type SQLiteIndex struct {
	db SQLiteDB
}

func EnsureSchema(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("ensure short ref schema: %w", err)
	}
	return nil
}

func NewSQLiteIndex(db SQLiteDB) *SQLiteIndex {
	return &SQLiteIndex{db: db}
}

func (i *SQLiteIndex) Upsert(ctx context.Context, alias, fullRef string) error {
	_, err := i.db.ExecContext(ctx, `
insert into short_refs(alias, full_ref)
values (?, ?)
on conflict(alias, full_ref) do nothing
`, alias, fullRef)
	if err != nil {
		return fmt.Errorf("upsert short ref: %w", err)
	}
	return nil
}

func (i *SQLiteIndex) UpsertEntry(ctx context.Context, entry Entry) error {
	return i.Upsert(ctx, entry.Alias, entry.FullRef)
}

func (i *SQLiteIndex) UpsertEntries(ctx context.Context, entries []Entry) error {
	for _, entry := range entries {
		if err := i.UpsertEntry(ctx, entry); err != nil {
			return err
		}
	}
	return nil
}

func (i *SQLiteIndex) Lookup(ctx context.Context, alias string) ([]string, error) {
	rows, err := i.db.QueryContext(ctx, `
select full_ref
from short_refs
where alias = ?
order by full_ref
`, alias)
	if err != nil {
		return nil, fmt.Errorf("lookup short ref: %w", err)
	}
	defer func() { _ = rows.Close() }()

	fullRefs := make([]string, 0)
	for rows.Next() {
		var fullRef string
		if err := rows.Scan(&fullRef); err != nil {
			return nil, fmt.Errorf("scan short ref lookup: %w", err)
		}
		fullRefs = append(fullRefs, fullRef)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read short ref lookup: %w", err)
	}
	return fullRefs, nil
}

func (i *SQLiteIndex) Clear(ctx context.Context) error {
	if _, err := i.db.ExecContext(ctx, `delete from short_refs`); err != nil {
		return fmt.Errorf("clear short refs: %w", err)
	}
	return nil
}

// Aliases returns the display alias for each of fullRefs that has index
// entries. A ref can hold several rows (a shorter prefix plus collision
// extensions); the longest stored alias is the unambiguous display form.
func (i *SQLiteIndex) Aliases(ctx context.Context, fullRefs []string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(fullRefs)), ",")
	args := make([]any, 0, len(fullRefs))
	for _, ref := range fullRefs {
		args = append(args, ref)
	}
	rows, err := i.db.QueryContext(ctx, `
select full_ref, alias
from short_refs
where full_ref in (`+placeholders+`)
order by full_ref, length(alias) desc
`, args...)
	if err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	aliases := make(map[string]string, len(fullRefs))
	for rows.Next() {
		var fullRef, alias string
		if err := rows.Scan(&fullRef, &alias); err != nil {
			return nil, fmt.Errorf("scan short ref alias: %w", err)
		}
		if aliases[fullRef] == "" {
			aliases[fullRef] = alias
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	return aliases, nil
}
