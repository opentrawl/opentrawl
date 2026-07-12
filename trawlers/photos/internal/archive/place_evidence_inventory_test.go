package archive

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestPlaceEvidenceInventoryUsesLatestCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	db, path := newPlaceEvidenceInventoryFixture(t, SchemaVersion)
	seedPlaceEvidenceSource(t, ctx, db, "source:fixture")
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:older", "source:fixture", "2026-07-12T08:00:00Z", "complete", `{"receipt":"older"}`)
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:a", "source:fixture", "2026-07-12T09:00:00Z", "complete", `{"receipt":"same-time-a"}`)
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:z", "source:fixture", "2026-07-12T09:00:00Z", "complete", `{"receipt":"same-time-z"}`)
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:newer-partial", "source:fixture", "2026-07-12T10:00:00Z", "partial", `{"receipt":"partial"}`)
	closePlaceEvidenceInventoryFixture(t, db)

	inventory, err := ReadPlaceEvidenceInventory(ctx, path, "source:fixture")
	if err != nil {
		t.Fatal(err)
	}
	want := PlaceEvidenceSnapshotReceipt{
		ID:                       "snapshot:z",
		CompletedAt:              "2026-07-12T09:00:00Z",
		CompletenessState:        "complete",
		CompletenessEvidenceJSON: `{"receipt":"same-time-z"}`,
	}
	if inventory.SourceLibraryID != "source:fixture" || inventory.Snapshot != want {
		t.Fatalf("inventory receipt = %#v, want source %q and receipt %#v", inventory, "source:fixture", want)
	}
}

func TestPlaceEvidenceInventoryFiltersExactCurrentImagesLastSeenInSnapshot(t *testing.T) {
	ctx := context.Background()
	db, path := newPlaceEvidenceInventoryFixture(t, SchemaVersion)
	seedPlaceEvidenceSource(t, ctx, db, "source:selected")
	seedPlaceEvidenceSource(t, ctx, db, "source:other")
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:selected", "source:selected", "2026-07-12T09:00:00Z", "complete", `{}`)
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:stale", "source:selected", "2026-07-12T08:00:00Z", "complete", `{}`)
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:other", "source:other", "2026-07-12T10:00:00Z", "complete", `{}`)

	seedPlaceEvidenceAsset(t, ctx, db, "asset:included", "source:selected", "current", "image", "2026-07-12T07:00:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:selected", "asset:included", "snapshot:selected")
	seedPlaceEvidenceAsset(t, ctx, db, "asset:deleted", "source:selected", "deleted", "image", "2026-07-12T07:01:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:selected", "asset:deleted", "snapshot:selected")
	seedPlaceEvidenceAsset(t, ctx, db, "asset:video", "source:selected", "current", "video", "2026-07-12T07:02:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:selected", "asset:video", "snapshot:selected")
	seedPlaceEvidenceAsset(t, ctx, db, "asset:stale", "source:selected", "current", "image", "2026-07-12T07:03:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:selected", "asset:stale", "snapshot:stale")
	seedPlaceEvidenceAsset(t, ctx, db, "asset:other", "source:other", "current", "image", "2026-07-12T07:04:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:other", "asset:other", "snapshot:other")
	closePlaceEvidenceInventoryFixture(t, db)

	inventory, err := ReadPlaceEvidenceInventory(ctx, path, "source:selected")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Assets) != 1 || inventory.Assets[0].AssetID != "asset:included" {
		t.Fatalf("inventory assets = %#v, want only asset:included", inventory.Assets)
	}
	if inventory.Assets[0].CreationDate != "2026-07-12T07:00:00Z" {
		t.Fatalf("creation date = %q", inventory.Assets[0].CreationDate)
	}
}

func TestPlaceEvidenceInventoryUsesFirstLocationAndKeepsMissingLocation(t *testing.T) {
	ctx := context.Background()
	db, path := newPlaceEvidenceInventoryFixture(t, SchemaVersion)
	seedPlaceEvidenceSource(t, ctx, db, "source:fixture")
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:fixture", "source:fixture", "2026-07-12T09:00:00Z", "complete", `{}`)
	seedPlaceEvidenceAsset(t, ctx, db, "asset:located", "source:fixture", "current", "image", "2026-07-12T07:00:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:fixture", "asset:located", "snapshot:fixture")
	seedPlaceEvidenceAsset(t, ctx, db, "asset:missing", "source:fixture", "current", "image", "2026-07-12T07:01:00Z")
	seedPlaceEvidenceSeenAsset(t, ctx, db, "source:fixture", "asset:missing", "snapshot:fixture")
	seedPlaceEvidenceLocation(t, ctx, db, "location:z", "asset:located", 52.37, 4.90)
	seedPlaceEvidenceLocation(t, ctx, db, "location:a", "asset:located", 40.71, -74.00)
	closePlaceEvidenceInventoryFixture(t, db)

	inventory, err := ReadPlaceEvidenceInventory(ctx, path, "source:fixture")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Assets) != 2 {
		t.Fatalf("inventory assets = %#v, want located and missing rows", inventory.Assets)
	}
	located := inventory.Assets[0]
	if located.AssetID != "asset:located" || located.Coordinate == nil || located.Coordinate.Latitude != 40.71 || located.Coordinate.Longitude != -74.00 {
		t.Fatalf("first location row = %#v, want location:a", located)
	}
	missing := inventory.Assets[1]
	if missing.AssetID != "asset:missing" || missing.Coordinate != nil {
		t.Fatalf("missing location row = %#v", missing)
	}
}

func TestPlaceEvidenceInventoryRequiresSchema13(t *testing.T) {
	ctx := context.Background()
	db, path := newPlaceEvidenceInventoryFixture(t, SchemaVersion-1)
	closePlaceEvidenceInventoryFixture(t, db)

	_, err := ReadPlaceEvidenceInventory(ctx, path, "source:fixture")
	if err == nil || !strings.Contains(err.Error(), "schema is 12, want 13") {
		t.Fatalf("schema error = %v", err)
	}
}

func TestPlaceEvidenceInventoryReportsMissingCompleteSnapshot(t *testing.T) {
	ctx := context.Background()
	db, path := newPlaceEvidenceInventoryFixture(t, SchemaVersion)
	seedPlaceEvidenceSource(t, ctx, db, "source:fixture")
	seedPlaceEvidenceSnapshot(t, ctx, db, "snapshot:partial", "source:fixture", "2026-07-12T09:00:00Z", "partial", `{"receipt":"partial"}`)
	closePlaceEvidenceInventoryFixture(t, db)

	_, err := ReadPlaceEvidenceInventory(ctx, path, "source:fixture")
	var incomplete *PlaceEvidenceSnapshotIncompleteError
	if !errors.As(err, &incomplete) || incomplete.SourceLibraryID != "source:fixture" {
		t.Fatalf("snapshot error = %T %v, want typed incomplete result", err, err)
	}
}

func newPlaceEvidenceInventoryFixture(t *testing.T, schemaVersion int) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photos.db")
	db, err := store.Open(context.Background(), store.Options{
		Path:          path,
		Schema:        Schema,
		SchemaVersion: schemaVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, path
}

func closePlaceEvidenceInventoryFixture(t *testing.T, db *store.Store) {
	t.Helper()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func seedPlaceEvidenceSource(t *testing.T, ctx context.Context, db *store.Store, sourceID string) {
	t.Helper()
	_, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values (?, '/tmp/Synthetic.photoslibrary', '/tmp/Synthetic.sqlite', '2026-07-12T07:00:00Z', 'synthetic', '{}')
`, sourceID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPlaceEvidenceSnapshot(t *testing.T, ctx context.Context, db *store.Store, snapshotID, sourceID, completedAt, state, evidenceJSON string) {
	t.Helper()
	_, err := db.DB().ExecContext(ctx, `
insert into crawl_snapshot(
  id, source_library_id, started_at, completed_at, provider,
  asset_count, resource_count, album_membership_count, location_count,
  completeness_state, completeness_evidence_json, metadata_json
)
values (?, ?, '2026-07-12T06:00:00Z', ?, 'synthetic', 0, 0, 0, 0, ?, ?, '{}')
`, snapshotID, sourceID, completedAt, state, evidenceJSON)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPlaceEvidenceAsset(t *testing.T, ctx context.Context, db *store.Store, assetID, sourceID, sourceState, mediaType, creationDate string) {
	t.Helper()
	_, err := db.DB().ExecContext(ctx, `
insert into asset(
  id, local_identifier, media_type, media_subtypes,
  creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden,
  burst_identifier, represents_burst, camera_make, camera_model, lens_model,
  source_library_id, source_state, source_state_snapshot_id, metadata_json
)
values (?, ?, ?, '', ?, ?, ?, 'UTC', 100, 80, 0, 0, 0, '', 0, '', '', '', ?, ?, '', '{}')
`, assetID, assetID, mediaType, creationDate, creationDate, creationDate, sourceID, sourceState)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPlaceEvidenceSeenAsset(t *testing.T, ctx context.Context, db *store.Store, sourceID, assetID, snapshotID string) {
	t.Helper()
	_, err := db.DB().ExecContext(ctx, `
insert into crawl_seen_asset(
  source_library_id, asset_id, first_seen_snapshot_id, last_seen_snapshot_id,
  source_fingerprint, last_seen_at
)
values (?, ?, ?, ?, 'synthetic', '2026-07-12T09:00:00Z')
`, sourceID, assetID, snapshotID, snapshotID)
	if err != nil {
		t.Fatal(err)
	}
}

func seedPlaceEvidenceLocation(t *testing.T, ctx context.Context, db *store.Store, locationID, assetID string, latitude, longitude float64) {
	t.Helper()
	_, err := db.DB().ExecContext(ctx, `
insert into location_observation(id, asset_id, latitude, longitude, source, evidence_id)
values (?, ?, ?, ?, 'synthetic', 'synthetic')
`, locationID, assetID, latitude, longitude)
	if err != nil {
		t.Fatal(err)
	}
}
