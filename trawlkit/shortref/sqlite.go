package shortref

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

const Schema = `
create table if not exists short_refs (
  alias text not null,
  full_ref text not null,
  canonical_ref text not null,
  primary key (alias, full_ref)
);
`

const indexSchema = `
create index if not exists idx_short_refs_alias on short_refs(alias);
create index if not exists idx_short_refs_full_ref on short_refs(full_ref);
create index if not exists idx_short_refs_canonical_ref on short_refs(canonical_ref);
`

const sqliteParameterChunkSize = 900

type SQLiteDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type SQLiteIndex struct {
	db SQLiteDB
}

func EnsureSchema(ctx context.Context, db SQLiteDB) error {
	if _, err := db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("ensure short ref schema: %w", err)
	}
	if err := ensureCanonicalColumn(ctx, db); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, indexSchema); err != nil {
		return fmt.Errorf("ensure short ref indexes: %w", err)
	}
	return nil
}

func NewSQLiteIndex(db SQLiteDB) *SQLiteIndex {
	return &SQLiteIndex{db: db}
}

func (i *SQLiteIndex) Upsert(ctx context.Context, alias, fullRef string) error {
	return i.UpsertCanonical(ctx, alias, fullRef, fullRef)
}

func (i *SQLiteIndex) UpsertCanonical(ctx context.Context, alias, fullRef, canonicalRef string) error {
	if strings.TrimSpace(canonicalRef) == "" {
		canonicalRef = fullRef
	}
	_, err := i.db.ExecContext(ctx, `
insert into short_refs(alias, full_ref, canonical_ref)
values (?, ?, ?)
on conflict(alias, full_ref) do update set canonical_ref = excluded.canonical_ref
`, alias, fullRef, canonicalRef)
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

func (i *SQLiteIndex) UpsertCanonicalEntries(ctx context.Context, entries []Entry, canonicalRefs map[string]string) error {
	for _, entry := range entries {
		canonicalRef := strings.TrimSpace(canonicalRefs[entry.FullRef])
		if canonicalRef == "" {
			canonicalRef = entry.FullRef
		}
		if err := i.UpsertCanonical(ctx, entry.Alias, entry.FullRef, canonicalRef); err != nil {
			return err
		}
	}
	return nil
}

func (i *SQLiteIndex) Lookup(ctx context.Context, alias string) ([]string, error) {
	fullRefs, err := i.lookup(ctx, alias, "coalesce(nullif(canonical_ref, ''), full_ref)")
	if err != nil && isMissingCanonicalColumn(err) {
		return i.lookup(ctx, alias, "full_ref")
	}
	return fullRefs, err
}

func (i *SQLiteIndex) lookup(ctx context.Context, alias, refExpr string) ([]string, error) {
	rows, err := i.db.QueryContext(ctx, `
select distinct `+refExpr+` as full_ref
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

func (i *SQLiteIndex) AllAliases(ctx context.Context) (map[string]struct{}, error) {
	rows, err := i.db.QueryContext(ctx, `
select distinct alias
from short_refs
`)
	if err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	aliases := make(map[string]struct{})
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("scan short ref alias: %w", err)
		}
		aliases[alias] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read short ref aliases: %w", err)
	}
	return aliases, nil
}

func (i *SQLiteIndex) IndexedFullRefs(ctx context.Context, fullRefs []string) (map[string]struct{}, error) {
	indexed := make(map[string]struct{})
	for start := 0; start < len(fullRefs); start += sqliteParameterChunkSize {
		end := start + sqliteParameterChunkSize
		if end > len(fullRefs) {
			end = len(fullRefs)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", end-start), ",")
		args := make([]any, 0, end-start)
		for _, ref := range fullRefs[start:end] {
			args = append(args, ref)
		}
		rows, err := i.db.QueryContext(ctx, `
select distinct full_ref
from short_refs
where full_ref in (`+placeholders+`)
`, args...)
		if err != nil {
			return nil, fmt.Errorf("read indexed short refs: %w", err)
		}
		for rows.Next() {
			var fullRef string
			if err := rows.Scan(&fullRef); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan indexed short ref: %w", err)
			}
			indexed[fullRef] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("read indexed short refs: %w", err)
		}
		_ = rows.Close()
	}
	return indexed, nil
}

func (i *SQLiteIndex) UpdateCanonicalRefs(ctx context.Context, canonicalRefs map[string]string) error {
	fullRefs := make([]string, 0, len(canonicalRefs))
	for fullRef := range canonicalRefs {
		fullRefs = append(fullRefs, fullRef)
	}
	sort.Strings(fullRefs)
	for _, fullRef := range fullRefs {
		canonicalRef := strings.TrimSpace(canonicalRefs[fullRef])
		if canonicalRef == "" {
			canonicalRef = fullRef
		}
		if _, err := i.db.ExecContext(ctx, `
update short_refs
set canonical_ref = ?
where full_ref = ?
  and coalesce(canonical_ref, '') <> ?
`, canonicalRef, fullRef, canonicalRef); err != nil {
			return fmt.Errorf("update short ref canonical ref: %w", err)
		}
	}
	return nil
}

// Aliases returns the display alias for each of fullRefs that has index
// entries. A ref can hold several rows (a shorter prefix plus collision
// extensions); the longest stored alias is the unambiguous display form.
func (i *SQLiteIndex) Aliases(ctx context.Context, fullRefs []string) (map[string]string, error) {
	aliases, err := i.aliasesChunked(ctx, fullRefs, "coalesce(nullif(canonical_ref, ''), full_ref)")
	if err != nil && isMissingCanonicalColumn(err) {
		return i.aliasesChunked(ctx, fullRefs, "full_ref")
	}
	return aliases, err
}

func (i *SQLiteIndex) aliasesChunked(ctx context.Context, fullRefs []string, refExpr string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(fullRefs))
	for start := 0; start < len(fullRefs); start += sqliteParameterChunkSize {
		end := start + sqliteParameterChunkSize
		if end > len(fullRefs) {
			end = len(fullRefs)
		}
		aliases, err := i.aliases(ctx, fullRefs[start:end], refExpr)
		if err != nil {
			return nil, err
		}
		for ref, alias := range aliases {
			out[ref] = alias
		}
	}
	return out, nil
}

func (i *SQLiteIndex) aliases(ctx context.Context, fullRefs []string, refExpr string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(fullRefs)), ",")
	args := make([]any, 0, len(fullRefs))
	for _, ref := range fullRefs {
		args = append(args, ref)
	}
	rows, err := i.db.QueryContext(ctx, `
select `+refExpr+` as full_ref, alias
from short_refs
where `+refExpr+` in (`+placeholders+`)
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

func ensureCanonicalColumn(ctx context.Context, db SQLiteDB) error {
	hasColumn, err := hasShortRefColumn(ctx, db, "canonical_ref")
	if err != nil {
		return err
	}
	if !hasColumn {
		if _, err := db.ExecContext(ctx, `alter table short_refs add column canonical_ref text`); err != nil {
			return fmt.Errorf("add short ref canonical column: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `update short_refs set canonical_ref = full_ref where canonical_ref is null or trim(canonical_ref) = ''`); err != nil {
		return fmt.Errorf("backfill short ref canonical column: %w", err)
	}
	return nil
}

func hasShortRefColumn(ctx context.Context, db SQLiteDB, name string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(short_refs)`)
	if err != nil {
		return false, fmt.Errorf("read short ref schema: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var columnName, columnType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, fmt.Errorf("scan short ref schema: %w", err)
		}
		if columnName == name {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read short ref schema: %w", err)
	}
	return false, nil
}

func isMissingCanonicalColumn(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such column: canonical_ref")
}
