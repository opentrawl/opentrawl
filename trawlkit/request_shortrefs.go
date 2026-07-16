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
// table and a script's --json both feed the one flag:
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

// AssignShortRefs extends the short-ref index without deleting rows.
//
// Existing aliases are permanent: sync updates canonical refs for matching full
// refs and assigns aliases only to refs not already indexed. Rows for deleted
// source items intentionally persist, so opening one reports not found, and
// aliases never re-shorten after a collision has made them longer.
//
// This guarantee holds once every binary writing the archive runs this code.
// Older binaries can still clear aliases during sync: kind-scoped in the
// immediately prior generation, and whole-table before that. A later sync
// preserves whatever state the old binary left; it cannot recover deleted
// aliases for refs no longer emitted.
func (r *Request) AssignShortRefs(ctx context.Context, records []ShortRefRecord) (int, error) {
	if r == nil || r.Store == nil {
		return 0, errors.New("archive store is not open")
	}
	indexRecords, err := shortRefIndexRecords(records)
	if err != nil {
		return 0, err
	}
	fullRefs := shortRefRecordFullRefs(indexRecords)
	canonicalRefs := shortRefRecordCanonicalRefs(indexRecords)
	err = r.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := shortref.EnsureSchema(ctx, tx); err != nil {
			return err
		}
		index := shortref.NewSQLiteIndex(tx)
		if err := index.UpdateCanonicalRefs(ctx, canonicalRefs); err != nil {
			return err
		}
		indexedRefs, err := index.IndexedFullRefs(ctx, fullRefs)
		if err != nil {
			return err
		}
		newRefs := shortRefNewFullRefs(indexRecords, indexedRefs)
		aliases, err := index.AllAliases(ctx)
		if err != nil {
			return err
		}
		entries, err := shortref.BuildSliceAvoidingAliases(newRefs, aliases)
		if err != nil {
			return err
		}
		return index.UpsertCanonicalEntries(ctx, shortRefLookupEntries(entries, aliases), canonicalRefs)
	})
	if err != nil {
		return 0, fmt.Errorf("assign short refs: %w", err)
	}
	return len(indexRecords), nil
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
	return index.Aliases(ctx, refs)
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

type shortRefIndexRecord struct {
	fullRef      string
	canonicalRef string
}

func shortRefIndexRecords(records []ShortRefRecord) ([]shortRefIndexRecord, error) {
	out := make([]shortRefIndexRecord, 0, len(records))
	seen := make(map[string]int, len(records))
	for _, record := range records {
		// The alias index is ref-shape-agnostic; crawlers own their ref grammar.
		fullRef := strings.TrimSpace(record.Ref)
		if fullRef == "" {
			continue
		}
		canonicalRef := strings.TrimSpace(record.CanonicalRef)
		if canonicalRef == "" {
			canonicalRef = fullRef
		}
		if existing, ok := seen[fullRef]; ok {
			if out[existing].canonicalRef != canonicalRef {
				return nil, fmt.Errorf("short ref %q has conflicting canonical refs", fullRef)
			}
			continue
		}
		seen[fullRef] = len(out)
		out = append(out, shortRefIndexRecord{fullRef: fullRef, canonicalRef: canonicalRef})
	}
	return out, nil
}

func shortRefRecordFullRefs(records []shortRefIndexRecord) []string {
	refs := make([]string, 0, len(records))
	for _, record := range records {
		refs = append(refs, record.fullRef)
	}
	return refs
}

func shortRefRecordCanonicalRefs(records []shortRefIndexRecord) map[string]string {
	refs := make(map[string]string, len(records))
	for _, record := range records {
		refs[record.fullRef] = record.canonicalRef
	}
	return refs
}

func shortRefNewFullRefs(records []shortRefIndexRecord, indexedRefs map[string]struct{}) []string {
	refs := make([]string, 0, len(records))
	for _, record := range records {
		if _, indexed := indexedRefs[record.fullRef]; indexed {
			continue
		}
		refs = append(refs, record.fullRef)
	}
	return refs
}

func isMissingShortRefTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table: short_refs")
}
