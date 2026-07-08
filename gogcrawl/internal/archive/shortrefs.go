package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/state"
)

const shortRefCountEntityID = "short_refs_message_count"

var (
	ErrUnknownShortRef   = errors.New("unknown short ref")
	ErrAmbiguousShortRef = errors.New("ambiguous short ref")
)

func (s *Store) EnsureShortRefs(ctx context.Context) (bool, int64, error) {
	messageCount, err := s.CountMessages(ctx)
	if err != nil {
		return false, 0, err
	}
	rec, ok, err := getStateAnySource(ctx, state.New(s.store.DB()), derivedEntityType, shortRefCountEntityID)
	if err != nil {
		return false, 0, err
	}
	if ok && strings.TrimSpace(rec.Value) == fmt.Sprintf("%d", messageCount) {
		return false, messageCount, nil
	}
	if _, err := s.RebuildShortRefs(ctx); err != nil {
		return false, 0, err
	}
	return true, messageCount, nil
}

func (s *Store) RebuildShortRefs(ctx context.Context) (int, error) {
	refs, err := s.messageRefs(ctx)
	if err != nil {
		return 0, err
	}
	displayEntries, err := shortref.BuildSlice(refs)
	if err != nil {
		return 0, err
	}
	lookupEntries := shortref.LookupEntries(displayEntries)
	if err := shortref.EnsureSchema(ctx, s.store.DB()); err != nil {
		return 0, err
	}
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		index := shortref.NewSQLiteIndex(tx)
		if err := index.Clear(ctx); err != nil {
			return err
		}
		if err := index.UpsertEntries(ctx, lookupEntries); err != nil {
			return err
		}
		if err := state.New(tx).Set(ctx, sourceName, derivedEntityType, shortRefCountEntityID, fmt.Sprintf("%d", len(refs))); err != nil {
			return fmt.Errorf("mark short refs rebuilt: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(refs), nil
}

func (s *Store) ResolveRef(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		if _, err := parseRef(ref); err != nil {
			return "", err
		}
		return ref, nil
	}
	if !shortref.ValidAlias(ref) {
		return "", ErrUnknownShortRef
	}
	if _, _, err := s.EnsureShortRefs(ctx); err != nil {
		return "", err
	}
	matches, err := shortref.NewSQLiteIndex(s.store.DB()).Lookup(ctx, ref)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", ErrUnknownShortRef
	case 1:
		return matches[0], nil
	default:
		return "", ErrAmbiguousShortRef
	}
}

func (s *Store) ShortRefs(ctx context.Context, fullRefs []string) (map[string]string, error) {
	if len(fullRefs) == 0 {
		return map[string]string{}, nil
	}
	if _, _, err := s.EnsureShortRefs(ctx); err != nil {
		return nil, err
	}
	index := shortref.NewSQLiteIndex(s.store.DB())
	out := map[string]string{}
	// crawlkit's Aliases issues one IN clause, and SQLite caps host
	// parameters per statement, so chunk it here: --all can resolve tens
	// of thousands of refs in one search. Each ref is unique and lands in
	// exactly one chunk, so merging the per-chunk alias maps is exact.
	const chunkSize = 900
	for start := 0; start < len(fullRefs); start += chunkSize {
		end := start + chunkSize
		if end > len(fullRefs) {
			end = len(fullRefs)
		}
		aliases, err := index.Aliases(ctx, fullRefs[start:end])
		if err != nil {
			return nil, err
		}
		for ref, alias := range aliases {
			out[ref] = alias
		}
	}
	if len(out) != len(fullRefs) {
		legacy := legacyRefs(fullRefs)
		for start := 0; start < len(legacy); start += chunkSize {
			end := start + chunkSize
			if end > len(legacy) {
				end = len(legacy)
			}
			aliases, err := index.Aliases(ctx, legacy[start:end])
			if err != nil {
				return nil, err
			}
			for ref, alias := range aliases {
				if canonical := canonicalRef(ref); canonical != "" && out[canonical] == "" {
					out[canonical] = alias
				}
			}
		}
	}
	return out, nil
}

func legacyRefs(fullRefs []string) []string {
	out := make([]string, 0, len(fullRefs))
	for _, ref := range fullRefs {
		if strings.HasPrefix(ref, RefPrefix) {
			out = append(out, LegacyRefPrefix+strings.TrimPrefix(ref, RefPrefix))
		}
	}
	return out
}

func canonicalRef(ref string) string {
	if !strings.HasPrefix(ref, LegacyRefPrefix) {
		return ""
	}
	return RefPrefix + strings.TrimPrefix(ref, LegacyRefPrefix)
}

func (s *Store) messageRefs(ctx context.Context) ([]string, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select id
from messages
order by id
`)
	if err != nil {
		return nil, fmt.Errorf("read message refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var refs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		refs = append(refs, RefPrefix+id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}
