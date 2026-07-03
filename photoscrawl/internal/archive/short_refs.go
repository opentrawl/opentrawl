package archive

import (
	"context"
	"database/sql"
	"os"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/store"
)

const (
	shortRefsSyncSource = "photoscrawl"
	shortRefsEntityType = "derived_index"
	shortRefsEntityID   = "short_refs"
)

type ShortRefResolution struct {
	FullRefs []string
	Rebuilt  bool
}

func ValidShortRef(alias string) bool {
	return shortref.ValidAlias(strings.TrimSpace(alias))
}

func EnsureShortRefs(ctx context.Context, paths Paths) (bool, error) {
	if _, err := os.Stat(paths.Database); err != nil {
		return false, err
	}
	readDB, err := store.OpenReadOnly(ctx, paths.Database)
	if err == nil {
		current, currentErr := shortRefsCurrent(ctx, readDB.DB())
		closeErr := readDB.Close()
		if currentErr != nil {
			return false, currentErr
		}
		if closeErr != nil {
			return false, closeErr
		}
		if current {
			return false, nil
		}
	}
	db, err := store.Open(ctx, store.Options{Path: paths.Database})
	if err != nil {
		return false, err
	}
	defer db.Close()
	return ensureShortRefs(ctx, db.DB())
}

func ResolveShortRef(ctx context.Context, paths Paths, alias string) (ShortRefResolution, error) {
	if _, err := os.Stat(paths.Database); err != nil {
		return ShortRefResolution{}, err
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return ShortRefResolution{}, err
	}
	defer db.Close()
	current, err := shortRefsCurrent(ctx, db.DB())
	if err != nil {
		return ShortRefResolution{}, err
	}
	if !current {
		return ShortRefResolution{}, nil
	}
	refs, err := shortref.NewSQLiteIndex(db.DB()).Lookup(ctx, strings.TrimSpace(alias))
	if err != nil {
		return ShortRefResolution{}, err
	}
	return ShortRefResolution{FullRefs: refs}, nil
}

func shortRefForFullRef(ctx context.Context, db *sql.DB, fullRef string) (string, error) {
	exists, err := tableExists(ctx, db, "short_refs")
	if err != nil || !exists {
		return "", err
	}
	rows, err := db.QueryContext(ctx, `
select sr.alias
from short_refs sr
where sr.full_ref = ?
  and not exists (
    select 1
    from short_refs other
    where other.alias = sr.alias
      and other.full_ref <> sr.full_ref
  )
order by length(sr.alias), sr.alias
limit 1
`, fullRef)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", rows.Err()
	}
	var alias string
	if err := rows.Scan(&alias); err != nil {
		return "", err
	}
	return alias, rows.Err()
}

func ensureShortRefs(ctx context.Context, db *sql.DB) (bool, error) {
	current, err := shortRefsCurrent(ctx, db)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	if err := rebuildShortRefs(ctx, db); err != nil {
		return false, err
	}
	return true, nil
}

func shortRefsCurrent(ctx context.Context, db *sql.DB) (bool, error) {
	exists, err := tableExists(ctx, db, "short_refs")
	if err != nil || !exists {
		return false, err
	}
	lastSync, err := latestSnapshotCompletedAt(ctx, db)
	if err != nil {
		return false, err
	}
	if lastSync == "" {
		return false, nil
	}
	builtFor, err := shortRefsBuiltFor(ctx, db)
	if err != nil {
		return false, err
	}
	if builtFor != lastSync {
		return false, nil
	}
	var assetCount int64
	if err := db.QueryRowContext(ctx, `select count(*) from asset`).Scan(&assetCount); err != nil {
		return false, err
	}
	var refCount int64
	if err := db.QueryRowContext(ctx, `select count(distinct full_ref) from short_refs`).Scan(&refCount); err != nil {
		return false, err
	}
	return assetCount == refCount, nil
}

func rebuildShortRefs(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := rebuildShortRefsInTx(ctx, tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func rebuildShortRefsInTx(ctx context.Context, tx *sql.Tx) error {
	if err := shortref.EnsureSchema(ctx, tx); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `select id from asset order by id`)
	if err != nil {
		return err
	}
	refs := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		refs = append(refs, assetRef(id))
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	displayEntries, err := shortref.BuildSlice(refs)
	if err != nil {
		return err
	}
	index := shortref.NewSQLiteIndex(tx)
	if err := index.Clear(ctx); err != nil {
		return err
	}
	if err := index.UpsertEntries(ctx, shortref.LookupEntries(displayEntries)); err != nil {
		return err
	}
	lastSync, err := latestSnapshotCompletedAtTx(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
insert into sync_state(source, entity_type, entity_id, cursor, synced_at)
values (?, ?, ?, ?, ?)
on conflict(source, entity_type, entity_id) do update set
  cursor = excluded.cursor,
  synced_at = excluded.synced_at
`, shortRefsSyncSource, shortRefsEntityType, shortRefsEntityID, lastSync, lastSync)
	return err
}

func shortRefsBuiltFor(ctx context.Context, db *sql.DB) (string, error) {
	var value sql.NullString
	err := db.QueryRowContext(ctx, `
select cursor
from sync_state
where source = ? and entity_type = ? and entity_id = ?
`, shortRefsSyncSource, shortRefsEntityType, shortRefsEntityID).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if value.Valid {
		return value.String, nil
	}
	return "", nil
}

func latestSnapshotCompletedAt(ctx context.Context, db *sql.DB) (string, error) {
	var value sql.NullString
	if err := db.QueryRowContext(ctx, `select max(completed_at) from crawl_snapshot`).Scan(&value); err != nil {
		return "", err
	}
	if value.Valid {
		return value.String, nil
	}
	return "", nil
}

func latestSnapshotCompletedAtTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var value sql.NullString
	if err := tx.QueryRowContext(ctx, `select max(completed_at) from crawl_snapshot`).Scan(&value); err != nil {
		return "", err
	}
	if value.Valid {
		return value.String, nil
	}
	return "", nil
}
