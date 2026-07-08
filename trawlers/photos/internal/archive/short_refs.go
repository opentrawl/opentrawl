package archive

import (
	"context"
	"database/sql"
	"os"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/crawlkit/store"
)

const (
	shortRefsSyncSource       = "photos"
	legacyShortRefsSyncSource = "photoscrawl"
	shortRefsEntityType       = "derived_index"
	shortRefsEntityID         = "short_refs"
)

type ShortRefResolution struct {
	FullRefs []string
	Rebuilt  bool
}

func ValidShortRef(alias string) bool {
	return shortref.ValidAlias(strings.TrimSpace(alias))
}

func ResolveShortRef(ctx context.Context, paths Paths, alias string) (ShortRefResolution, error) {
	if _, err := os.Stat(paths.Database); err != nil {
		return ShortRefResolution{}, err
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return ShortRefResolution{}, err
	}
	defer func() { _ = db.Close() }()
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
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		legacy := legacyAssetRef(fullRef)
		if legacy == "" {
			return "", rows.Err()
		}
		return shortRefForFullRef(ctx, db, legacy)
	}
	var alias string
	if err := rows.Scan(&alias); err != nil {
		return "", err
	}
	return alias, rows.Err()
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
	// The cursor records the snapshot this alias index was built for, so a
	// later sync knows whether the index is current (shortRefsBuiltFor).
	return state.NewCursor(tx).Set(ctx, shortRefsSyncSource, shortRefsEntityType, shortRefsEntityID, lastSync)
}

func shortRefsBuiltFor(ctx context.Context, db *sql.DB) (string, error) {
	// A read-only open of an archive synced before this table existed has no
	// sync_cursor_state; treat that as "not built" so reads degrade to long
	// refs and self-heal on the next sync, rather than erroring.
	exists, err := tableExists(ctx, db, "sync_cursor_state")
	if err != nil || !exists {
		return "", err
	}
	rec, ok, err := getCursorAnySource(ctx, state.NewCursor(db), shortRefsEntityType, shortRefsEntityID)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	return rec.Cursor, nil
}

func getCursorAnySource(ctx context.Context, cursors *state.CursorStore, entityType, entityID string) (state.CursorRecord, bool, error) {
	for _, source := range []string{shortRefsSyncSource, legacyShortRefsSyncSource} {
		rec, ok, err := cursors.Get(ctx, source, entityType, entityID)
		if err != nil || ok {
			return rec, ok, err
		}
	}
	return state.CursorRecord{}, false, nil
}

func legacyAssetRef(ref string) string {
	if !strings.HasPrefix(ref, "photos:") {
		return ""
	}
	return "photoscrawl:" + strings.TrimPrefix(ref, "photos:")
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
