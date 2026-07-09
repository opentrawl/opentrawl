package trawlkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
)

var (
	ErrUnknownShortRef   = errors.New("unknown short ref")
	ErrAmbiguousShortRef = errors.New("ambiguous short ref")
	// ErrShortRefNotChat is returned by ResolveChatArg when a short ref
	// resolves to a ref that is not a chat (a message ref, say). The alias
	// space is shared, so a reader can paste a message short ref by mistake;
	// the caller turns this into a clean "that is not a chat" usage error.
	ErrShortRefNotChat = errors.New("short ref is not a chat")
)

// ResolveChatArg turns whatever a reader pasted into messages --chat into the
// raw source chat id the store queries. It accepts three shapes, so the chats
// table and an agent's --json both feed the one flag:
//   - a short ref from the chats table, resolved through the same index open
//     and search use (the value carries no ":"),
//   - a full source ref like "telegram:chat/42139272", and
//   - a raw source id (a rowid, a JID).
//
// chatPrefix is the source's chat ref prefix, e.g. "imessage:chat/". A short
// ref that resolves to a non-chat ref returns ErrShortRefNotChat; one that is
// not in the index falls through to the raw-id reading, so a raw id that is not
// an indexed alias still reaches the store. The alias space is shared, so a raw
// id that both looks like an alias (5+ chars, no 0/1/l/i/o) and equals a live
// alias resolves as that alias first; real source ids sidestep this (an iMessage
// rowid carries 0/1, a JID carries ":" or "@"), so it is a corner a reader hits
// only by pasting a bare token that is not a real id.
func (r *Request) ResolveChatArg(ctx context.Context, value, chatPrefix string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.Contains(value, ":") {
		return strings.TrimPrefix(value, chatPrefix), nil
	}
	if ValidShortRef(value) {
		refs, err := r.ResolveShortRef(ctx, value)
		switch {
		case errors.Is(err, ErrUnknownShortRef):
			// Not an alias in this archive; read it as a raw id below.
		case err != nil:
			return "", err
		default:
			ref := refs[0]
			if !strings.HasPrefix(ref, chatPrefix) {
				return "", ErrShortRefNotChat
			}
			return strings.TrimPrefix(ref, chatPrefix), nil
		}
	}
	return value, nil
}

func ValidShortRef(alias string) bool {
	return shortref.ValidAlias(strings.TrimSpace(alias))
}

// RebuildShortRefs deletes and rebuilds exactly the kinds named by records.
//
// Each record Ref implies its <source>:<kind>/ prefix, and Kind records declare
// kinds that must be cleared even when the crawler emits no refs for that kind.
// This protects archives when the running binary knows fewer kinds than a
// previous binary wrote. It cannot protect binaries built before kind-scoped
// clearing existed: their compiled code still clears the whole table.
func (r *Request) RebuildShortRefs(ctx context.Context, records []ShortRefRecord) (int, error) {
	if r == nil || r.Store == nil {
		return 0, errors.New("archive store is not open")
	}
	refs := shortRefIndexRefs(records)
	kinds, err := shortRefIndexKinds(records)
	if err != nil {
		return 0, err
	}
	err = r.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := shortref.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		index := shortref.NewSQLiteIndex(tx)
		survivingAliases, err := index.SurvivingAliases(ctx, kinds)
		if err != nil {
			return err
		}
		entries, err := shortref.BuildSliceAvoidingAliases(refs, survivingAliases)
		if err != nil {
			return err
		}
		if err := index.ClearKinds(ctx, kinds); err != nil {
			return err
		}
		return index.UpsertEntries(ctx, shortRefLookupEntries(entries, survivingAliases))
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

func shortRefLookupEntries(entries []shortref.Entry, reservedAliases map[string]struct{}) []shortref.Entry {
	lookupEntries := shortref.LookupEntries(entries)
	if len(reservedAliases) == 0 {
		return lookupEntries
	}
	filtered := lookupEntries[:0]
	for _, entry := range lookupEntries {
		if _, reserved := reservedAliases[entry.Alias]; reserved {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
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

func shortRefIndexKinds(records []ShortRefRecord) ([]string, error) {
	kinds := make([]string, 0, len(records))
	for _, record := range records {
		if kind := strings.TrimSpace(record.Kind); kind != "" {
			normalized, err := normalizeShortRefKind(kind)
			if err != nil {
				return nil, err
			}
			kinds = append(kinds, normalized)
		}
		if ref := strings.TrimSpace(record.Ref); ref != "" {
			kind, ok := parseShortRefKind(ref)
			if !ok || len(ref) == len(kind) {
				return nil, fmt.Errorf("short ref %q must look like <source>:<kind>/<id>", ref)
			}
			kinds = append(kinds, kind)
		}
	}
	return uniqueStrings(kinds), nil
}

func normalizeShortRefKind(kind string) (string, error) {
	parsed, ok := parseShortRefKind(kind)
	if !ok || parsed != kind {
		return "", fmt.Errorf("short ref kind %q must look like <source>:<kind>/", kind)
	}
	return parsed, nil
}

func parseShortRefKind(value string) (string, bool) {
	colon := strings.IndexByte(value, ':')
	slash := strings.IndexByte(value, '/')
	if colon <= 0 || slash <= colon+1 {
		return "", false
	}
	kind := value[:slash+1]
	if strings.ContainsAny(kind, " \t\r\n") {
		return "", false
	}
	return kind, true
}

func isMissingShortRefTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: short_refs")
}
