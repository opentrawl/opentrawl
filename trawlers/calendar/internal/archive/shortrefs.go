package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
)

func (s *Store) EnsureShortRefs(ctx context.Context) (bool, error) {
	current, err := s.ShortRefsCurrent(ctx)
	if err != nil {
		return false, err
	}
	if current {
		return false, nil
	}
	if err := s.RebuildShortRefs(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) ShortRefsCurrent(ctx context.Context) (bool, error) {
	eventCount, err := countTable(ctx, s.store.DB(), "events")
	if err != nil {
		return false, err
	}
	var indexedRefs int64
	err = s.store.DB().QueryRowContext(ctx, `select count(distinct full_ref) from short_refs`).Scan(&indexedRefs)
	if err != nil {
		if isMissingShortRefTable(err) {
			return false, nil
		}
		return false, err
	}
	return indexedRefs == eventCount, nil
}

func (s *Store) RebuildShortRefs(ctx context.Context) error {
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return rebuildShortRefsTx(ctx, tx)
	})
}

func (s *Store) ResolveShortRef(ctx context.Context, alias string) ([]string, error) {
	return shortref.NewSQLiteIndex(s.store.DB()).Lookup(ctx, alias)
}

func (s *Store) ShortRefForFullRef(ctx context.Context, fullRef string) (string, error) {
	fullRef = strings.TrimSpace(fullRef)
	if fullRef == "" {
		return "", nil
	}
	var alias string
	err := s.store.DB().QueryRowContext(ctx, `
select alias
from short_refs
where full_ref = ?
order by length(alias) desc, alias
limit 1`, fullRef).Scan(&alias)
	if errors.Is(err, sql.ErrNoRows) {
		if legacy := legacyRef(fullRef); legacy != "" {
			return s.ShortRefForFullRef(ctx, legacy)
		}
		return "", nil
	}
	if err != nil {
		if isMissingShortRefTable(err) {
			return "", nil
		}
		return "", err
	}
	return alias, nil
}

func legacyRef(ref string) string {
	if !strings.HasPrefix(ref, AppID+":") {
		return ""
	}
	return LegacyAppID + ":" + strings.TrimPrefix(ref, AppID+":")
}

func rebuildShortRefsTx(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `select event_uid from events order by event_uid`)
	if err != nil {
		return fmt.Errorf("read event refs for short refs: %w", err)
	}
	refs := []string{}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan event ref for short refs: %w", err)
		}
		refs = append(refs, RefForUID(uid))
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close event refs for short refs: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read event refs for short refs: %w", err)
	}
	entries, err := shortref.BuildSlice(refs)
	if err != nil {
		return err
	}
	if err := shortref.EnsureSchema(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `delete from short_refs`); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `
insert into short_refs(alias, full_ref)
values (?, ?)
on conflict(alias, full_ref) do nothing
`)
	if err != nil {
		return fmt.Errorf("prepare short ref insert: %w", err)
	}
	defer func() { _ = insert.Close() }()
	for _, entry := range shortref.LookupEntries(entries) {
		if _, err := insert.ExecContext(ctx, entry.Alias, entry.FullRef); err != nil {
			return fmt.Errorf("insert short ref: %w", err)
		}
	}
	return nil
}

func isMissingShortRefTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: short_refs")
}
