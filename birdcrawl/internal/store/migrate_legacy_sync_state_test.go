package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMigrateLegacySyncStatePreservesValuesAndIsIdempotent pins the
// TRAWL-82 birdcrawl migration: the old sync_state(kind, cursor,
// last_sync_at, last_result, coverage_note) table holds a real financial
// ledger (spend:<month>) and pagination cursors that cost real X API
// dollars to re-derive, so nothing may be lost or reset when an archive
// upgrades onto crawlkit's canonical state.Schema.
func TestMigrateLegacySyncStatePreservesValuesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "birdcrawl.db")

	// Seed a v1 archive: open fresh (this creates the current schema,
	// already at the canonical shape), then rewrite sync_state back into
	// the pre-migration shape and roll user_version back to 1, so the
	// next Open sees exactly what a real pre-existing archive looks like.
	seed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `drop table sync_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `create table sync_state (
		kind text primary key,
		cursor text,
		last_sync_at text,
		last_result text,
		coverage_note text
	)`); err != nil {
		t.Fatal(err)
	}
	legacyRows := []struct{ kind, cursor, lastSyncAt, lastResult, coverageNote string }{
		{"spend:2026-07", "10360000", "2026-07-05T09:00:00Z", "ok", ""},
		{"spend:2026-06", "9990000", "2026-06-30T23:59:00Z", "ok", ""},
		{"cursor:authored", "1234567890123456789", "2026-07-05T09:00:00Z", "ok", ""},
		// A real historical value found on the live archive: old code
		// never overwrote coverage_note after the first insert, so an
		// old, dead-but-real text value can still be sitting there.
		{"archive_import", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "ok", "archive dump imported through 2024-01-01T00:00:00Z"},
		{"live_sync", "", "2026-07-05T09:00:00Z", "partial", ""},
		{"auth:token_valid", "true", "2026-07-05T09:00:00Z", "true", ""},
		{"bookmark_pass", "2026-07-01T00:00:00Z", "2026-07-01T00:00:00Z", "ok", ""},
	}
	for _, row := range legacyRows {
		if _, err := seed.db.ExecContext(ctx, `insert into sync_state(kind,cursor,last_sync_at,last_result,coverage_note) values (?,?,?,?,?)`,
			row.kind, row.cursor, row.lastSyncAt, row.lastResult, row.coverageNote); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := seed.db.ExecContext(ctx, `pragma user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	// First open after the seed: this is the migration.
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	const wantSpendJuly = int64(10_360_000)
	const wantSpendJune = int64(9_990_000)
	spendJuly, err := st.SpendMicros(ctx, "2026-07")
	if err != nil {
		t.Fatal(err)
	}
	if spendJuly != wantSpendJuly {
		t.Fatalf("spend micros (2026-07) = %d, want %d — the paid X API ledger must survive byte-for-byte", spendJuly, wantSpendJuly)
	}
	spendJune, err := st.SpendMicros(ctx, "2026-06")
	if err != nil {
		t.Fatal(err)
	}
	if spendJune != wantSpendJune {
		t.Fatalf("spend micros (2026-06) = %d, want %d", spendJune, wantSpendJune)
	}

	cursorState, err := st.SyncState(ctx, "cursor:authored")
	if err != nil {
		t.Fatal(err)
	}
	if cursorState.Cursor != "1234567890123456789" {
		t.Fatalf("cursor:authored = %q, want the original pagination cursor (a lost cursor forces a paid full re-crawl)", cursorState.Cursor)
	}

	liveSync, err := st.SyncState(ctx, "live_sync")
	if err != nil {
		t.Fatal(err)
	}
	if liveSync.LastResult != "partial" {
		t.Fatalf("live_sync last result = %q, want %q", liveSync.LastResult, "partial")
	}

	tokenState, err := st.SyncState(ctx, "auth:token_valid")
	if err != nil {
		t.Fatal(err)
	}
	if tokenState.Cursor != "true" {
		t.Fatalf("auth:token_valid = %q, want %q", tokenState.Cursor, "true")
	}

	bookmarkPass, err := st.SyncState(ctx, "bookmark_pass")
	if err != nil {
		t.Fatal(err)
	}
	if bookmarkPass.Cursor != "2026-07-01T00:00:00Z" {
		t.Fatalf("bookmark_pass cursor = %q, want the original timestamp", bookmarkPass.Cursor)
	}

	archiveImport, err := st.SyncState(ctx, "archive_import")
	if err != nil {
		t.Fatal(err)
	}
	const wantCoverageNote = "archive dump imported through 2024-01-01T00:00:00Z"
	if archiveImport.CoverageNote != wantCoverageNote {
		t.Fatalf("archive_import coverage_note = %q, want %q", archiveImport.CoverageNote, wantCoverageNote)
	}

	version, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}

	// The table must now carry the canonical crawlkit shape, not the
	// legacy one.
	var hasSourceName int
	if err := st.db.QueryRowContext(ctx, `select count(*) from pragma_table_info('sync_state') where name = 'source_name'`).Scan(&hasSourceName); err != nil {
		t.Fatal(err)
	}
	if hasSourceName == 0 {
		t.Fatal("sync_state is still the legacy shape after migration")
	}
	var hasKindColumn int
	if err := st.db.QueryRowContext(ctx, `select count(*) from pragma_table_info('sync_state') where name = 'kind'`).Scan(&hasKindColumn); err != nil {
		t.Fatal(err)
	}
	if hasKindColumn != 0 {
		t.Fatal("sync_state still has the legacy kind column after migration")
	}

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	beforeSecondOpen, err := dumpSyncState(ctx, path)
	if err != nil {
		t.Fatal(err)
	}

	// Idempotency: the migration only ever runs once (guarded by
	// user_version), so reopening an already-migrated archive must leave
	// the whole table byte-identical to how the first open left it, not
	// just the couple of values already asserted above.
	st2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st2.Close(); err != nil {
		t.Fatal(err)
	}

	afterSecondOpen, err := dumpSyncState(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if beforeSecondOpen != afterSecondOpen {
		t.Fatalf("second open changed sync_state (migration is not idempotent):\nbefore:\n%s\nafter:\n%s", beforeSecondOpen, afterSecondOpen)
	}
}

// dumpSyncState reads every row of the canonical sync_state table, sorted
// for a stable diff, as one string.
func dumpSyncState(ctx context.Context, path string) (string, error) {
	st, err := OpenReadOnly(ctx, path)
	if err != nil {
		return "", err
	}
	defer func() { _ = st.Close() }()
	rows, err := st.db.QueryContext(ctx, `select source_name, entity_type, entity_id, value, updated_at from sync_state order by source_name, entity_type, entity_id`)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var out strings.Builder
	for rows.Next() {
		var sourceName, entityType, entityID, value, updatedAt string
		if err := rows.Scan(&sourceName, &entityType, &entityID, &value, &updatedAt); err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "%s|%s|%s|%s|%s\n", sourceName, entityType, entityID, value, updatedAt)
	}
	return out.String(), rows.Err()
}

// TestMigrateLegacySyncStateUnparseableLastSyncAt pins the one place the
// migration cannot be literally byte-identical: crawlkit's canonical
// state.Store.Get parses updated_at as RFC3339Nano and hard-errors on
// anything else, unlike the old parseStoredTime, which silently treated
// ANY parse failure — blank or corrupt — as zero time. So a legacy row
// whose last_sync_at is blank, or is simply not a valid timestamp, is
// written with birdcrawl's own existing "no timestamp" sentinel
// (UnknownTimeRFC3339) instead of the raw value. That sentinel parses
// back to the same zero time such a value always decoded to via the old
// code, so this asserts the semantic result — a zero LastSyncAt — is
// unchanged, and, more importantly, that the migration does not error or
// drop the row either way.
func TestMigrateLegacySyncStateUnparseableLastSyncAt(t *testing.T) {
	for _, lastSyncAt := range []string{"", "not-a-timestamp", "2026-13-99"} {
		t.Run(lastSyncAt, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "birdcrawl.db")

			seed, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := seed.db.ExecContext(ctx, `drop table sync_state`); err != nil {
				t.Fatal(err)
			}
			if _, err := seed.db.ExecContext(ctx, `create table sync_state (
				kind text primary key,
				cursor text,
				last_sync_at text,
				last_result text,
				coverage_note text
			)`); err != nil {
				t.Fatal(err)
			}
			if _, err := seed.db.ExecContext(ctx, `insert into sync_state(kind,cursor,last_sync_at,last_result,coverage_note) values ('spend:2026-07','10360000',?,'ok','')`, lastSyncAt); err != nil {
				t.Fatal(err)
			}
			if _, err := seed.db.ExecContext(ctx, `pragma user_version = 1`); err != nil {
				t.Fatal(err)
			}
			if err := seed.Close(); err != nil {
				t.Fatal(err)
			}

			st, err := Open(ctx, path)
			if err != nil {
				t.Fatalf("migration on last_sync_at=%q must not error: %v", lastSyncAt, err)
			}
			defer func() { _ = st.Close() }()

			spend, err := st.SpendMicros(ctx, "2026-07")
			if err != nil {
				t.Fatal(err)
			}
			if spend != 10_360_000 {
				t.Fatalf("spend micros = %d, want 10360000 — an unparseable last_sync_at must not lose the row it belongs to", spend)
			}
			state, err := st.SyncState(ctx, "spend:2026-07")
			if err != nil {
				t.Fatalf("SyncState after migrating last_sync_at=%q: %v", lastSyncAt, err)
			}
			if !state.LastSyncAt.IsZero() {
				t.Fatalf("LastSyncAt = %v, want the zero time (matching what this value always decoded to)", state.LastSyncAt)
			}
		})
	}
}

// TestMigrateLegacySyncStateOnFreshDatabase pins the other branch: a
// brand-new archive (never had the legacy table) must open straight onto
// the canonical schema with no error and no phantom migrated rows.
func TestMigrateLegacySyncStateOnFreshDatabase(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "birdcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	version, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	spend, err := st.SpendMicros(ctx, "2026-07")
	if err != nil {
		t.Fatal(err)
	}
	if spend != 0 {
		t.Fatalf("spend micros on a fresh database = %d, want 0", spend)
	}
}

// TestOpenReadOnlyReportsOutdatedSchemaHonestly pins the fix for a real
// regression this migration introduced: migrate() only runs on a writable
// Open, so status/doctor (which deliberately use OpenReadOnly to avoid
// contending with a running sync) hit the new canonical-shape queries
// against a database still in the old shape and got a generic SQL error.
// OpenReadOnly must instead detect the old shape — a cheap read-only
// structural check, no write — and fail with a named, actionable error
// instead of surfacing a raw driver error to the CLI layer.
func TestOpenReadOnlyReportsOutdatedSchemaHonestly(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "birdcrawl.db")

	seed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `drop table sync_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `create table sync_state (
		kind text primary key,
		cursor text,
		last_sync_at text,
		last_result text,
		coverage_note text
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `pragma user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = OpenReadOnly(ctx, path)
	if !errors.Is(err, ErrSchemaOutdated) {
		t.Fatalf("OpenReadOnly on a not-yet-migrated archive: err = %v, want ErrSchemaOutdated", err)
	}

	// A writable Open migrates it; OpenReadOnly must then succeed.
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(ctx, path)
	if err != nil {
		t.Fatalf("OpenReadOnly after migration: %v", err)
	}
	_ = ro.Close()
}

// TestImportArchiveAfterMigrationPreservesCoverageNote pins a real
// regression a reviewer caught: the migration correctly copies a legacy
// archive_import row's coverage_note across, but upsertSyncState writes
// all three canonical rows for a kind unconditionally, so a later,
// ordinary ImportArchive call (which never sets CoverageNote itself)
// would otherwise blank it out. ImportArchive must read the existing
// value forward, matching the old hand-rolled SQL's ON CONFLICT clause,
// which never touched coverage_note after the first insert.
func TestImportArchiveAfterMigrationPreservesCoverageNote(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "birdcrawl.db")

	seed, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `drop table sync_state`); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `create table sync_state (
		kind text primary key,
		cursor text,
		last_sync_at text,
		last_result text,
		coverage_note text
	)`); err != nil {
		t.Fatal(err)
	}
	const wantCoverageNote = "archive dump imported through 2024-01-01T00:00:00Z"
	if _, err := seed.db.ExecContext(ctx, `insert into sync_state(kind,cursor,last_sync_at,last_result,coverage_note) values ('archive_import','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','ok',?)`, wantCoverageNote); err != nil {
		t.Fatal(err)
	}
	if _, err := seed.db.ExecContext(ctx, `pragma user_version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	// A second, ordinary archive import — the exact call that used to
	// silently reset coverage_note under the naive rewrite.
	importedAt := time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC)
	if _, err := st.ImportArchive(ctx, ImportBatch{
		Tweets:          []Tweet{testTweet("1", importedAt, "1", "example", "Example", "hi")},
		CoverageThrough: importedAt,
	}); err != nil {
		t.Fatal(err)
	}

	archiveImport, err := st.SyncState(ctx, "archive_import")
	if err != nil {
		t.Fatal(err)
	}
	if archiveImport.CoverageNote != wantCoverageNote {
		t.Fatalf("coverage_note after a later ImportArchive = %q, want %q (must not be reset)", archiveImport.CoverageNote, wantCoverageNote)
	}
}
