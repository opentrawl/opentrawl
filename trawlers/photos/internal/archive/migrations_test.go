package archive

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestArchiveOpenMigratesOldObservationColumns(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        oldObservationSchema(t),
		SchemaVersion: SchemaVersion - 1,
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
	if exists, err := tableColumnExists(ctx, migrated.DB(), "model_observation", "generation_id"); err != nil {
		t.Fatal(err)
	} else if !exists {
		t.Fatal("missing migrated column model_observation.generation_id")
	}
	if exists, err := tableColumnExists(ctx, migrated.DB(), "place_observation", "generation_id"); err != nil {
		t.Fatal(err)
	} else if !exists {
		t.Fatal("missing migrated column place_observation.generation_id")
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

func TestPrepareArchiveMakesOldArchiveReadable(t *testing.T) {
	ctx := context.Background()
	paths := buildOldObservationArchive(t)
	if err := PrepareArchive(ctx, paths.Database); err != nil {
		t.Fatal(err)
	}
	status, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "ok" || !hasStatusCountValue(status.Counts, "photos", 1) {
		t.Fatalf("status after preparation = %#v", status)
	}
	search, err := Search(ctx, paths, SearchOptions{Query: "migrationterm", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].Ref != "photos:asset/old-schema" {
		t.Fatalf("search after preparation = %#v", search)
	}
	opened, err := Open(ctx, paths, search.Results[0].Ref)
	if err != nil || opened.Model.Summary != "Old migrationterm card." {
		t.Fatalf("open after preparation = %#v, %v", opened, err)
	}
	beforeRepeat, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareArchive(ctx, paths.Database); err != nil {
		t.Fatal(err)
	}
	afterRepeat, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if string(beforeRepeat) != string(afterRepeat) {
		t.Fatal("repeat preparation changed the current archive")
	}
}

func TestPrepareArchiveRollsBackAndRetries(t *testing.T) {
	ctx := context.Background()
	paths := buildOldObservationArchive(t)
	injected := errors.New("synthetic migration stop")
	alters := 0
	err := prepareArchive(ctx, paths.Database, func(archiveColumnMigration) error {
		alters++
		if alters == 2 {
			return injected
		}
		return nil
	})
	if !errors.Is(err, injected) {
		t.Fatalf("preparation error = %v, want injected error", err)
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exists, err := tableColumnExists(ctx, db.DB(), "model_observation", "stale_since")
	if err != nil {
		t.Fatal(err)
	}
	var records int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from asset where id = 'asset:old-schema'`).Scan(&records); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if version != SchemaVersion-1 || exists || records != 1 {
		t.Fatalf("rolled-back archive version=%d migrated_column=%t records=%d", version, exists, records)
	}
	if err := PrepareArchive(ctx, paths.Database); err != nil {
		t.Fatal(err)
	}
	search, err := Search(ctx, paths, SearchOptions{Query: "migrationterm", Limit: 5})
	if err != nil || len(search.Results) != 1 {
		t.Fatalf("search after retry = %#v, %v", search, err)
	}
}

func TestPrepareArchiveLeavesCurrentArchiveUnchanged(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	db, err := openArchive(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if err := PrepareArchive(ctx, paths.Database); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("current archive changed during preparation")
	}
}

func TestPrepareArchiveRejectsNewerArchiveUnchanged(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion + 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	err = PrepareArchive(ctx, paths.Database)
	var incompatible ArchiveIncompatibleError
	if !errors.As(err, &incompatible) {
		t.Fatalf("preparation error = %v, want ArchiveIncompatibleError", err)
	}
	after, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("newer archive changed during preparation")
	}
}

func TestPrepareArchiveRejectsInvalidArchiveUnchanged(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	before := []byte("not a SQLite archive")
	if err := os.WriteFile(paths.Database, before, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareArchive(ctx, paths.Database); err == nil {
		t.Fatal("preparation accepted an invalid archive")
	}
	after, err := os.ReadFile(paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("invalid archive changed during preparation")
	}
}

func buildOldObservationArchive(t *testing.T) Paths {
	t.Helper()
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        oldObservationSchema(t),
		SchemaVersion: SchemaVersion - 1,
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

func TestRunnerStoreReadsValidateOnceWithoutOpening(t *testing.T) {
	validation := functionSource(t, "store.go", "validateReadStore")
	if strings.Count(validation, "db.SchemaVersion(ctx)") != 1 {
		t.Fatalf("read store must read schema version once:\n%s", validation)
	}
	for _, check := range []struct {
		path string
		name string
	}{
		{path: "query.go", name: "SearchWithStore"},
		{path: "open_query.go", name: "OpenWithStoreFocused"},
	} {
		body := functionSource(t, check.path, check.name)
		if strings.Count(body, "validateReadStore(ctx, db)") != 1 {
			t.Fatalf("%s must validate the runner store once:\n%s", check.name, body)
		}
		for _, forbidden := range []string{"OpenReadOnly", "openExistingArchive", "openArchive", "archiveMigrationsRequired", "ensureArchiveMigrations"} {
			if strings.Contains(body, forbidden) {
				t.Fatalf("%s calls %s:\n%s", check.name, forbidden, body)
			}
		}
	}
}

func functionSource(t *testing.T, path, name string) string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range parsed.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Name.Name != name {
			continue
		}
		start := files.Position(function.Pos()).Offset
		end := files.Position(function.End()).Offset
		return string(source[start:end])
	}
	t.Fatalf("could not inspect %s in %s", name, path)
	return ""
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
	for _, table := range []string{"model_observation", "place_observation"} {
		exists, err := tableColumnExists(ctx, db.DB(), table, "generation_id")
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			t.Fatalf("missing migrated column %s.generation_id", table)
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
	old = strings.ReplaceAll(old, `,
  generation_id text references model_generation(id)`, "")
	if old == Schema {
		t.Fatal("old schema fixture did not remove observation columns")
	}
	return old
}
