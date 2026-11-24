package shortref

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
)

const ScopedSchema = `
create table if not exists trawler_short_references (
  trawler text not null,
  alias text not null,
  full_ref text not null,
  canonical_ref text not null,
  tombstoned integer not null default 0,
  primary key (trawler, alias, full_ref)
);
create index if not exists idx_trawler_short_refs_lookup on trawler_short_references(trawler, alias);
create index if not exists idx_trawler_short_refs_full on trawler_short_references(trawler, full_ref);
create index if not exists idx_trawler_short_refs_canonical on trawler_short_references(trawler, canonical_ref);
`

// ScopedSQLiteIndex stores and resolves short references inside the unified
// core index database, partitioned by registered trawler identity.
type ScopedSQLiteIndex struct {
	db      SQLiteDB
	trawler string
}

// ScopedShortReferenceCandidate pairs a stable assignment key with its current
// resolved canonical record reference.
type ScopedShortReferenceCandidate struct {
	StableFullRef string
	CanonicalRef  string
}

func EnsureScopedSchema(ctx context.Context, db SQLiteDB) error {
	if _, err := db.ExecContext(ctx, ScopedSchema); err != nil {
		return fmt.Errorf("ensure scoped short ref schema: %w", err)
	}
	return nil
}

func NewScopedSQLiteIndex(db SQLiteDB, trawler string) *ScopedSQLiteIndex {
	return &ScopedSQLiteIndex{
		db:      db,
		trawler: strings.TrimSpace(trawler),
	}
}

func (i *ScopedSQLiteIndex) Lookup(ctx context.Context, alias string) ([]string, error) {
	// DISTINCT or ORDER BY here makes SQLite scan the whole trawler through
	// the canonical_ref index instead of seeking the alias; dedupe in Go.
	rows, err := i.db.QueryContext(ctx, `
select canonical_ref
from trawler_short_references
where trawler = ?
  and alias = ?
  and tombstoned = 0
`, i.trawler, alias)
	if err != nil {
		return nil, fmt.Errorf("lookup scoped short ref: %w", err)
	}
	defer func() { _ = rows.Close() }()

	refs := make([]string, 0)
	for rows.Next() {
		var canonicalRef string
		if err := rows.Scan(&canonicalRef); err != nil {
			return nil, fmt.Errorf("scan scoped short ref lookup: %w", err)
		}
		refs = append(refs, canonicalRef)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scoped short ref lookup: %w", err)
	}
	slices.Sort(refs)
	return slices.Compact(refs), nil
}

func (i *ScopedSQLiteIndex) Aliases(ctx context.Context, canonicalRefs []string) (map[string]string, error) {
	if len(canonicalRefs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(canonicalRefs))
	for start := 0; start < len(canonicalRefs); start += sqliteParameterChunkSize {
		end := min(start+sqliteParameterChunkSize, len(canonicalRefs))
		chunk, err := i.aliasesChunk(ctx, canonicalRefs[start:end])
		if err != nil {
			return nil, err
		}
		for ref, alias := range chunk {
			out[ref] = alias
		}
	}
	return out, nil
}

func (i *ScopedSQLiteIndex) aliasesChunk(ctx context.Context, canonicalRefs []string) (map[string]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(canonicalRefs)), ",")
	args := make([]any, 0, len(canonicalRefs)+1)
	args = append(args, i.trawler)
	for _, ref := range canonicalRefs {
		args = append(args, ref)
	}
	rows, err := i.db.QueryContext(ctx, `
select canonical_ref, alias
from trawler_short_references
where trawler = ?
  and canonical_ref in (`+placeholders+`)
  and tombstoned = 0
order by canonical_ref, length(alias) desc
`, args...)
	if err != nil {
		return nil, fmt.Errorf("read scoped short ref aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	aliases := make(map[string]string, len(canonicalRefs))
	for rows.Next() {
		var ref, alias string
		if err := rows.Scan(&ref, &alias); err != nil {
			return nil, fmt.Errorf("scan scoped short ref alias: %w", err)
		}
		if aliases[ref] == "" {
			aliases[ref] = alias
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read scoped short ref aliases: %w", err)
	}
	return aliases, nil
}

func (i *ScopedSQLiteIndex) AllAliases(ctx context.Context) (map[string]struct{}, error) {
	rows, err := i.db.QueryContext(ctx, `
select distinct alias
from trawler_short_references
where trawler = ?
`, i.trawler)
	if err != nil {
		return nil, fmt.Errorf("read all scoped short ref aliases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	aliases := make(map[string]struct{})
	for rows.Next() {
		var alias string
		if err := rows.Scan(&alias); err != nil {
			return nil, fmt.Errorf("scan scoped short ref alias: %w", err)
		}
		aliases[alias] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read all scoped short ref aliases: %w", err)
	}
	return aliases, nil
}

func (i *ScopedSQLiteIndex) IndexedFullRefs(ctx context.Context, fullRefs []string) (map[string]struct{}, error) {
	if len(fullRefs) == 0 {
		return nil, nil
	}
	indexed := make(map[string]struct{}, len(fullRefs))
	for start := 0; start < len(fullRefs); start += sqliteParameterChunkSize {
		end := min(start+sqliteParameterChunkSize, len(fullRefs))
		if err := i.indexedFullRefsChunk(ctx, fullRefs[start:end], indexed); err != nil {
			return nil, err
		}
	}
	return indexed, nil
}

func (i *ScopedSQLiteIndex) indexedFullRefsChunk(ctx context.Context, fullRefs []string, out map[string]struct{}) error {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(fullRefs)), ",")
	args := make([]any, 0, len(fullRefs)+1)
	args = append(args, i.trawler)
	for _, ref := range fullRefs {
		args = append(args, ref)
	}
	rows, err := i.db.QueryContext(ctx, `
select distinct full_ref
from trawler_short_references
where trawler = ?
  and full_ref in (`+placeholders+`)
`, args...)
	if err != nil {
		return fmt.Errorf("read indexed scoped full refs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			return fmt.Errorf("scan indexed scoped full ref: %w", err)
		}
		out[ref] = struct{}{}
	}
	return rows.Err()
}

func (i *ScopedSQLiteIndex) UpsertCanonical(ctx context.Context, alias, fullRef, canonicalRef string) error {
	if strings.TrimSpace(canonicalRef) == "" {
		canonicalRef = fullRef
	}
	_, err := i.db.ExecContext(ctx, `
insert into trawler_short_references(trawler, alias, full_ref, canonical_ref, tombstoned)
values (?, ?, ?, ?, 0)
on conflict(trawler, alias, full_ref) do update set canonical_ref = excluded.canonical_ref, tombstoned = 0
`, i.trawler, alias, fullRef, canonicalRef)
	if err != nil {
		return fmt.Errorf("upsert scoped short ref: %w", err)
	}
	return nil
}

func (i *ScopedSQLiteIndex) UpdateCanonicalRefs(ctx context.Context, canonicalRefs map[string]string) error {
	fullRefs := make([]string, 0, len(canonicalRefs))
	for ref := range canonicalRefs {
		fullRefs = append(fullRefs, ref)
	}
	sort.Strings(fullRefs)
	for _, fullRef := range fullRefs {
		canonicalRef := strings.TrimSpace(canonicalRefs[fullRef])
		if canonicalRef == "" {
			canonicalRef = fullRef
		}
		if _, err := i.db.ExecContext(ctx, `
update trawler_short_references
set canonical_ref = ?, tombstoned = 0
where trawler = ?
  and full_ref = ?
  and (coalesce(canonical_ref, '') <> ? or tombstoned <> 0)
`, canonicalRef, i.trawler, fullRef, canonicalRef); err != nil {
			return fmt.Errorf("update scoped short ref canonical ref: %w", err)
		}
	}
	return nil
}

func (i *ScopedSQLiteIndex) TombstoneFullRefs(ctx context.Context, fullRefs []string) error {
	if len(fullRefs) == 0 {
		return nil
	}
	for start := 0; start < len(fullRefs); start += sqliteParameterChunkSize {
		end := min(start+sqliteParameterChunkSize, len(fullRefs))
		chunk := fullRefs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, i.trawler)
		for _, ref := range chunk {
			args = append(args, ref)
		}
		if _, err := i.db.ExecContext(ctx, `
update trawler_short_references
set tombstoned = 1
where trawler = ?
  and full_ref in (`+placeholders+`)
  and tombstoned = 0
`, args...); err != nil {
			return fmt.Errorf("tombstone scoped short refs: %w", err)
		}
	}
	return nil
}

func (i *ScopedSQLiteIndex) TombstoneAbsentFullRefs(ctx context.Context, presentFullRefs []string) error {
	present := make(map[string]struct{}, len(presentFullRefs))
	for _, ref := range presentFullRefs {
		present[ref] = struct{}{}
	}
	rows, err := i.db.QueryContext(ctx, `
select distinct full_ref
from trawler_short_references
where trawler = ?
  and tombstoned = 0
`, i.trawler)
	if err != nil {
		return fmt.Errorf("read active scoped short refs: %w", err)
	}
	var absent []string
	for rows.Next() {
		var ref string
		if err := rows.Scan(&ref); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan active scoped short ref: %w", err)
		}
		if _, ok := present[ref]; !ok {
			absent = append(absent, ref)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return i.TombstoneFullRefs(ctx, absent)
}

// AssignCandidates preserves existing aliases, tombstones absent or deleted
// references without recycling their aliases, and assigns new shortest
// collision-free aliases for newly observed references.
func (i *ScopedSQLiteIndex) AssignCandidates(
	ctx context.Context,
	candidates []ScopedShortReferenceCandidate,
	completeSnapshot bool,
	deletedFullRefs []string,
) error {
	canonicalByFull := make(map[string]string, len(candidates))
	stableRefs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		stable := strings.TrimSpace(candidate.StableFullRef)
		if stable == "" {
			continue
		}
		canonical := strings.TrimSpace(candidate.CanonicalRef)
		if canonical == "" {
			canonical = stable
		}
		if _, seen := canonicalByFull[stable]; !seen {
			stableRefs = append(stableRefs, stable)
		}
		canonicalByFull[stable] = canonical
	}
	if completeSnapshot {
		if err := i.TombstoneAbsentFullRefs(ctx, stableRefs); err != nil {
			return err
		}
	} else if len(deletedFullRefs) > 0 {
		if err := i.TombstoneFullRefs(ctx, deletedFullRefs); err != nil {
			return err
		}
	}
	if len(stableRefs) == 0 {
		return nil
	}
	if err := i.UpdateCanonicalRefs(ctx, canonicalByFull); err != nil {
		return err
	}
	indexed, err := i.IndexedFullRefs(ctx, stableRefs)
	if err != nil {
		return err
	}
	var unindexed []string
	for _, ref := range stableRefs {
		if _, exists := indexed[ref]; !exists {
			unindexed = append(unindexed, ref)
		}
	}
	if len(unindexed) == 0 {
		return nil
	}
	reservedAliases, err := i.AllAliases(ctx)
	if err != nil {
		return err
	}
	entries, err := BuildSliceAvoidingAliases(unindexed, reservedAliases)
	if err != nil {
		return fmt.Errorf("assign scoped short refs: %w", err)
	}
	for _, entry := range LookupEntries(entries) {
		if _, reserved := reservedAliases[entry.Alias]; reserved {
			continue
		}
		if err := i.UpsertCanonical(ctx, entry.Alias, entry.FullRef, canonicalByFull[entry.FullRef]); err != nil {
			return err
		}
	}
	return nil
}
