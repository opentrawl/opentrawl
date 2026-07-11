package archive

import (
	"context"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestArchiveOpenMigratesLegacySourceStateColumns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	legacySchema := legacySourceStateSchema(t)
	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        legacySchema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values ('source:fixture', '/tmp/fixture.photoslibrary', 'sqlite:crawl_snapshot/fixture', '2026-07-11T10:00:00Z', 'fixture', '{}');
insert into crawl_snapshot(id, source_library_id, started_at, completed_at, provider, asset_count, resource_count, album_membership_count, location_count, metadata_json)
values ('snapshot:fixture', 'source:fixture', '2026-07-11T10:00:00Z', '2026-07-11T10:00:00Z', 'fixture', 1, 0, 0, 0, '{}');
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values ('asset:fixture', 'fixture', 'image', '0', '2026-07-11T10:00:00Z', '2026-07-11T10:00:00Z',
  '2026-07-11T10:00:00Z', 'UTC', 100, 80, 0, 0, 0, '', 0, '', '', '', 'source:fixture', '{}');
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
	var sourceState, stateSnapshotID, completenessState, completenessEvidence string
	if err := migrated.DB().QueryRowContext(ctx, `
select a.source_state, a.source_state_snapshot_id, s.completeness_state, s.completeness_evidence_json
from asset a
join crawl_snapshot s on s.id = 'snapshot:fixture'
where a.id = 'asset:fixture'
`).Scan(&sourceState, &stateSnapshotID, &completenessState, &completenessEvidence); err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=archive_migration output={\"source_state\":%q,\"state_snapshot_id\":%q,\"completeness_state\":%q,\"completeness_evidence\":%s}", sourceState, stateSnapshotID, completenessState, completenessEvidence)
	if sourceState != sourceStateCurrent || stateSnapshotID != "" || completenessState != "legacy_unknown" || completenessEvidence != "{}" {
		t.Fatalf("migrated state = %q %q %q %q", sourceState, stateSnapshotID, completenessState, completenessEvidence)
	}
	status, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=archive_migration_status output={\"last_import_at\":%q}", status.LastImportAt)
	if status.LastImportAt != "" {
		t.Fatalf("legacy completeness must not look like a successful sync: last_import_at=%q", status.LastImportAt)
	}
}

func legacySourceStateSchema(t *testing.T) string {
	t.Helper()
	legacy := Schema
	for _, line := range []string{
		"  completeness_state text not null,\n",
		"  completeness_evidence_json text not null,\n",
		"  source_state text not null default 'current',\n",
		"  first_missing_at text,\n",
		"  source_deleted_at text,\n",
		"  source_state_snapshot_id text not null default '',\n",
	} {
		legacy = strings.Replace(legacy, line, "", 1)
	}
	if legacy == Schema {
		t.Fatal("legacy schema fixture did not remove source-state columns")
	}
	return legacy
}
