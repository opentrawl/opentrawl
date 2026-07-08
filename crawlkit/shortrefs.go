package crawlkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/shortref"
)

var (
	ErrUnknownShortRef   = errors.New("unknown short ref")
	ErrAmbiguousShortRef = errors.New("ambiguous short ref")
)

func ValidShortRef(alias string) bool {
	return shortref.ValidAlias(strings.TrimSpace(alias))
}

func (r *Request) RebuildShortRefs(ctx context.Context, records []ShortRefRecord) (int, error) {
	if r == nil || r.Store == nil {
		return 0, errors.New("archive store is not open")
	}
	refs := shortRefIndexRefs(records)
	entries, err := shortref.BuildSlice(refs)
	if err != nil {
		return 0, err
	}
	err = r.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := shortref.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		index := shortref.NewSQLiteIndex(tx)
		if err := index.Clear(ctx); err != nil {
			return err
		}
		return index.UpsertEntries(ctx, shortref.LookupEntries(entries))
	})
	if err != nil {
		return 0, fmt.Errorf("rebuild short refs: %w", err)
	}
	return len(refs), nil
}

func (r *Request) ResolveShortRef(ctx context.Context, alias string) ([]string, error) {
	alias = strings.TrimSpace(alias)
	if !ValidShortRef(alias) {
		return nil, ErrUnknownShortRef
	}
	matches, err := r.lookupShortRef(ctx, alias)
	if err != nil {
		if isMissingShortRefTable(err) {
			return nil, ErrUnknownShortRef
		}
		return nil, err
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

func (r *Request) ShortRefAliases(ctx context.Context, refs []string) (map[string]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if r == nil || r.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	index := shortref.NewSQLiteIndex(r.Store.DB())
	canonical := make([]string, 0, len(refs))
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		canonical = append(canonical, ref)
	}
	aliases, err := shortRefAliases(ctx, index, canonical)
	if err != nil {
		if isMissingShortRefTable(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return aliases, nil
}

func (r *Request) lookupShortRef(ctx context.Context, alias string) ([]string, error) {
	if r == nil || r.Store == nil {
		return nil, errors.New("archive store is not open")
	}
	return shortref.NewSQLiteIndex(r.Store.DB()).Lookup(ctx, alias)
}

func shortRefAliases(ctx context.Context, index *shortref.SQLiteIndex, refs []string) (map[string]string, error) {
	refs = uniqueStrings(refs)
	if len(refs) == 0 {
		return nil, nil
	}
	const chunkSize = 900
	out := make(map[string]string, len(refs))
	for start := 0; start < len(refs); start += chunkSize {
		end := start + chunkSize
		if end > len(refs) {
			end = len(refs)
		}
		aliases, err := index.Aliases(ctx, refs[start:end])
		if err != nil {
			return nil, err
		}
		for ref, alias := range aliases {
			out[ref] = alias
		}
	}
	return out, nil
}

func shortRefIndexRefs(records []ShortRefRecord) []string {
	refs := make([]string, 0, len(records))
	for _, record := range records {
		if ref := strings.TrimSpace(record.Ref); ref != "" {
			refs = append(refs, ref)
		}
	}
	return uniqueStrings(refs)
}

func isMissingShortRefTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: short_refs")
}
