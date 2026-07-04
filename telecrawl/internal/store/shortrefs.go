package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
)

const MessageRefPrefix = "telecrawl:msg/"

var (
	ErrUnknownShortRef   = errors.New("unknown short ref")
	ErrAmbiguousShortRef = errors.New("ambiguous short ref")
)

func MessageRef(sourcePK int64) string {
	return MessageRefPrefix + strconv.FormatInt(sourcePK, 10)
}

func (s *Store) RebuildShortRefs(ctx context.Context) error {
	if err := shortref.EnsureSchema(ctx, s.db); err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx, `select source_pk from messages order by source_pk`)
	if err != nil {
		return fmt.Errorf("list message refs for short refs: %w", err)
	}
	defer rows.Close()

	var refs []string
	for rows.Next() {
		var sourcePK int64
		if err := rows.Scan(&sourcePK); err != nil {
			return fmt.Errorf("scan message ref for short refs: %w", err)
		}
		refs = append(refs, MessageRef(sourcePK))
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read message refs for short refs: %w", err)
	}
	displayEntries, err := shortref.BuildSlice(refs)
	if err != nil {
		return fmt.Errorf("build short refs: %w", err)
	}
	lookupEntries := shortref.LookupEntries(displayEntries)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if err := shortref.EnsureSchema(ctx, tx); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `create index if not exists idx_short_refs_full_ref on short_refs(full_ref)`); err != nil {
		return err
	}
	index := shortref.NewSQLiteIndex(tx)
	if err := index.Clear(ctx); err != nil {
		return err
	}
	if err := index.UpsertEntries(ctx, lookupEntries); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ResolveShortRef(ctx context.Context, alias string) ([]string, error) {
	alias = strings.TrimSpace(alias)
	if !shortref.ValidAlias(alias) {
		return nil, ErrUnknownShortRef
	}
	fresh, err := s.shortRefsFresh(ctx)
	if err != nil {
		return nil, err
	}
	if !fresh {
		if err := s.RebuildShortRefs(ctx); err != nil {
			return nil, err
		}
	}
	matches, err := s.lookupShortRef(ctx, alias)
	if err != nil {
		return nil, err
	}
	if len(matches) == 0 {
		if err := s.RebuildShortRefs(ctx); err != nil {
			return nil, err
		}
		matches, err = s.lookupShortRef(ctx, alias)
		if err != nil {
			return nil, err
		}
	}
	switch len(matches) {
	case 0:
		return nil, ErrUnknownShortRef
	case 1:
		return matches, nil
	default:
		return matches, ErrAmbiguousShortRef
	}
}

func (s *Store) ShortRefsFor(ctx context.Context, fullRefs []string) (map[string]string, error) {
	refs := uniqueRefs(fullRefs)
	fresh, err := s.shortRefsFresh(ctx)
	if err != nil {
		return nil, err
	}
	if !fresh {
		if err := s.RebuildShortRefs(ctx); err != nil {
			return nil, err
		}
	}
	aliases, err := shortref.NewSQLiteIndex(s.db).Aliases(ctx, refs)
	if err != nil {
		return nil, err
	}
	if len(aliases) != len(refs) {
		if err := s.RebuildShortRefs(ctx); err != nil {
			return nil, err
		}
		aliases, err = shortref.NewSQLiteIndex(s.db).Aliases(ctx, refs)
		if err != nil {
			return nil, err
		}
	}
	return aliases, nil
}

func (s *Store) lookupShortRef(ctx context.Context, alias string) ([]string, error) {
	if err := shortref.EnsureSchema(ctx, s.db); err != nil {
		return nil, err
	}
	return shortref.NewSQLiteIndex(s.db).Lookup(ctx, alias)
}

func (s *Store) shortRefsFresh(ctx context.Context) (bool, error) {
	if err := shortref.EnsureSchema(ctx, s.db); err != nil {
		return false, err
	}
	var messages int
	if err := s.db.QueryRowContext(ctx, `select count(*) from messages`).Scan(&messages); err != nil {
		return false, err
	}
	var refs int
	if err := s.db.QueryRowContext(ctx, `select count(distinct full_ref) from short_refs`).Scan(&refs); err != nil {
		return false, err
	}
	return messages == refs, nil
}

func uniqueRefs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}
