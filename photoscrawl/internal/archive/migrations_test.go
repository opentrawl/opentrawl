package archive

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/store"
)

func TestArchiveOpenMigratesOldObservationColumns(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        oldObservationSchema(t),
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values ('source:fixture', '/tmp/fixture.photoslibrary', 'sqlite:crawl_snapshot/fixture', '2026-05-28T10:00:00Z', 'fixture', '{}')
`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values ('asset:old-schema', 'old-schema', 'image', '0', '2026-05-27T10:00:00Z', '2026-05-27T10:00:00Z',
  '2026-05-27T10:00:00Z', 'UTC', 100, 80, 0, 0, 0, '', 0, '', '', '', 'source:fixture', '{}')
`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values ('old-card-summary', 'asset:old-schema', 'card_summary', 'Old migrationterm card.', '{}', 1.0, 'photo_card', 'fixture-model', 'v1', '')
`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('old-card-summary', 'asset:old-schema', '', 'Old migrationterm card.')
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	migrated, err := openArchive(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = migrated.Close() }()
	for _, table := range []string{"model_observation", "place_observation"} {
		for _, column := range []string{"stale_since", "stale_reason", "superseded_at"} {
			exists, err := tableColumnExists(ctx, migrated.DB(), table, column)
			if err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("missing migrated column %s.%s", table, column)
			}
		}
	}

	opened, err := Open(ctx, paths, "asset:old-schema")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Model.Summary != "Old migrationterm card." || opened.Stale != nil {
		t.Fatalf("opened old-schema card = %#v", opened)
	}
	search, err := Search(ctx, paths, SearchOptions{Query: "migrationterm", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].Stale {
		t.Fatalf("old-schema search = %#v", search.Results)
	}
}

func TestOpenAndSearchMigrateOldObservationSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	buildArchive := func(t *testing.T) Paths {
		t.Helper()
		paths := testPaths(t)
		db, err := store.Open(ctx, store.Options{
			Path:          paths.Database,
			Schema:        oldObservationSchema(t),
			SchemaVersion: SchemaVersion,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values ('source:fixture', '/tmp/fixture.photoslibrary', 'sqlite:crawl_snapshot/fixture', '2026-05-28T10:00:00Z', 'fixture', '{}')
`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().ExecContext(ctx, `
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values ('asset:old-schema', 'old-schema', 'image', '0', '2026-05-27T10:00:00Z', '2026-05-27T10:00:00Z',
  '2026-05-27T10:00:00Z', 'UTC', 100, 80, 0, 0, 0, '', 0, '', '', '', 'source:fixture', '{}')
`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values ('old-card-summary', 'asset:old-schema', 'card_summary', 'Old migrationterm card.', '{}', 1.0, 'photo_card', 'fixture-model', 'v1', '')
`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.DB().ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('old-card-summary', 'asset:old-schema', '', 'Old migrationterm card.')
`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return paths
	}
	assertMigrated := func(t *testing.T, paths Paths) {
		t.Helper()
		db, err := store.OpenReadOnly(ctx, paths.Database)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		for _, table := range []string{"model_observation", "place_observation"} {
			for _, column := range []string{"stale_since", "stale_reason", "superseded_at"} {
				exists, err := tableColumnExists(ctx, db.DB(), table, column)
				if err != nil {
					t.Fatal(err)
				}
				if !exists {
					t.Fatalf("missing migrated column %s.%s", table, column)
				}
			}
		}
	}

	t.Run("open", func(t *testing.T) {
		paths := buildArchive(t)
		opened, err := Open(ctx, paths, "asset:old-schema")
		if err != nil {
			t.Fatal(err)
		}
		if opened.Ref != "photoscrawl:asset/old-schema" || opened.Model.Summary != "Old migrationterm card." || opened.Stale != nil {
			t.Fatalf("opened old-schema card = %#v", opened)
		}
		assertMigrated(t, paths)
	})
	t.Run("search", func(t *testing.T) {
		paths := buildArchive(t)
		search, err := Search(ctx, paths, SearchOptions{Query: "migrationterm", Limit: 5})
		if err != nil {
			t.Fatal(err)
		}
		if len(search.Results) != 1 || search.Results[0].Ref != "photoscrawl:asset/old-schema" || search.Results[0].Stale {
			t.Fatalf("old-schema search = %#v", search.Results)
		}
		assertMigrated(t, paths)
	})
}

func TestEnsureArchiveMigrationsTreatsDuplicateColumnRaceAsSuccess(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        oldObservationSchema(t),
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	preAdded := false
	err = ensureArchiveMigrationsBeforeAlter(ctx, db.DB(), func(migration archiveColumnMigration) error {
		if preAdded || migration.table != "model_observation" || migration.column != "stale_since" {
			return nil
		}
		preAdded = true
		_, err := db.DB().ExecContext(ctx, `
alter table "model_observation" add column "stale_since" text
`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !preAdded {
		t.Fatal("race fixture did not pre-add the migration column")
	}
	for _, table := range []string{"model_observation", "place_observation"} {
		for _, column := range []string{"stale_since", "stale_reason", "superseded_at"} {
			exists, err := tableColumnExists(ctx, db.DB(), table, column)
			if err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("missing migrated column %s.%s", table, column)
			}
		}
	}
}

func TestOpenAndSearchUseReadOnlyPathWhenArchiveIsMigrated(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: fakeSnapshot(false, false)},
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	readOnlyPaths := paths
	readOnlyPaths.Database = sqliteReadOnlyURI(paths.Database)
	search, err := Search(ctx, readOnlyPaths, SearchOptions{Query: "beach", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("read-only search results = %#v", search.Results)
	}
	opened, err := Open(ctx, readOnlyPaths, search.Results[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Ref != search.Results[0].Ref {
		t.Fatalf("read-only open ref = %q, want %q", opened.Ref, search.Results[0].Ref)
	}
}

func sqliteReadOnlyURI(path string) string {
	u := url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	return u.String()
}

func oldObservationSchema(t *testing.T) string {
	t.Helper()
	old := strings.ReplaceAll(Schema, `,
  stale_since text,
  stale_reason text,
  superseded_at text`, "")
	if old == Schema {
		t.Fatal("old schema fixture did not remove observation columns")
	}
	return old
}
