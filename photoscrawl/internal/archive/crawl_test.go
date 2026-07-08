package archive

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
)

func TestSyncImportsSnapshotAndTracksDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	provider := fakeProvider{snapshot: fakeSnapshot(false, true)}
	result, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsSeen != 2 || result.AssetsNew != 2 || result.AssetsChanged != 0 || result.AssetsUnchanged != 0 {
		t.Fatalf("first sync delta = new %d changed %d unchanged %d seen %d", result.AssetsNew, result.AssetsChanged, result.AssetsUnchanged, result.AssetsSeen)
	}
	if result.ResourcesSeen != 2 || result.AlbumMembershipsSeen != 2 || result.LocationsSeen != 1 {
		t.Fatalf("first sync counts = resources %d albums %d locations %d", result.ResourcesSeen, result.AlbumMembershipsSeen, result.LocationsSeen)
	}
	if result.QueuedForClassify != 2 || result.QueuedNeedsDownload != 1 {
		t.Fatalf("first sync queue = classify %d download %d", result.QueuedForClassify, result.QueuedNeedsDownload)
	}

	search, err := Search(ctx, paths, SearchOptions{Query: "beach", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("search results = %d, want 1", len(search.Results))
	}
	if search.TotalMatches != 1 || search.Truncated {
		t.Fatalf("search metadata = total %d truncated %t", search.TotalMatches, search.Truncated)
	}

	opened, err := Open(ctx, paths, search.Results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Mechanical.Original == nil {
		t.Fatalf("open returned original=%#v", opened.Mechanical.Original)
	}
	if opened.Mechanical.GPS == nil || opened.Mechanical.GPS.HorizontalAccuracyMeters != 8 {
		t.Fatalf("open GPS = %#v", opened.Mechanical.GPS)
	}
	if opened.Mechanical.Camera == nil || opened.Mechanical.Camera.Display != "Apple iPhone 15 Pro, 24mm equiv, f/1.8, 1/120s, ISO 64" {
		t.Fatalf("open camera = %#v", opened.Mechanical.Camera)
	}
	openedJSON, err := json.Marshal(opened)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(openedJSON), `"source"`) {
		t.Fatalf("open JSON leaked provenance source: %s", openedJSON)
	}

	classified, err := Classify(ctx, paths, ClassifyOptions{
		All: true,
		Now: fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if classified.Processed != 2 || classified.MetadataClassified != 2 || classified.WaitingForLocalContent != 1 || classified.MetadataObservationsWritten == 0 {
		t.Fatalf("classify result = processed %d metadata %d waiting %d observations %d", classified.Processed, classified.MetadataClassified, classified.WaitingForLocalContent, classified.MetadataObservationsWritten)
	}
	observationSearch, err := Search(ctx, paths, SearchOptions{Query: "screenshot", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(observationSearch.Results) != 1 {
		t.Fatalf("observation search = %#v", observationSearch.Results)
	}
	opened, err = Open(ctx, paths, observationSearch.Results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	openedJSON, err = json.Marshal(opened)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(openedJSON), "bounding_box") || strings.Contains(string(openedJSON), "confidence") || strings.Contains(string(openedJSON), "observations") {
		t.Fatalf("metadata open JSON leaked vision-shaped fields: %s", openedJSON)
	}
	status, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.Summary == "" || status.LastImportAt == "" {
		t.Fatalf("status summary=%q last_import_at=%q", status.Summary, status.LastImportAt)
	}
	if status.State != "ok" {
		t.Fatalf("status state = %q, want ok", status.State)
	}
	if status.Freshness == nil || status.Freshness.LastSync == "" {
		t.Fatalf("status freshness = %#v", status.Freshness)
	}
	if len(status.Counts) != 2 {
		t.Fatalf("status counts = %#v", status.Counts)
	}
	if !hasStatusCount(status.Counts, "photos") || !hasStatusCount(status.Counts, "since") {
		t.Fatalf("missing curated status counts in %#v", status.Counts)
	}

	provider.snapshot = fakeSnapshot(true, false)
	result, err = Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T11:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsSeen != 1 || result.AssetsNew != 0 || result.AssetsChanged != 1 || result.AssetsUnchanged != 0 || result.PreviouslySeenMissing != 1 {
		t.Fatalf("second sync delta = seen %d new %d changed %d unchanged %d missing %d", result.AssetsSeen, result.AssetsNew, result.AssetsChanged, result.AssetsUnchanged, result.PreviouslySeenMissing)
	}

	result, err = Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T11:30:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsSeen != 1 || result.AssetsNew != 0 || result.AssetsChanged != 0 || result.AssetsUnchanged != 1 || result.QueuedForClassify != 0 || result.PreviouslySeenMissing != 1 {
		t.Fatalf("third sync delta = seen %d new %d changed %d unchanged %d queued %d missing %d", result.AssetsSeen, result.AssetsNew, result.AssetsChanged, result.AssetsUnchanged, result.QueuedForClassify, result.PreviouslySeenMissing)
	}
}

func TestSyncExpandsHomeInLibraryPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	libraryPath := filepath.Join(home, "Pictures", "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	provider := &pathRecordingProvider{snapshot: fakeSnapshot(false, false)}
	if _, err := Sync(context.Background(), testPaths(t), SyncOptions{
		LibraryPath: "~/Pictures/Fixture Photos Library.photoslibrary",
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	if provider.path != libraryPath {
		t.Fatalf("provider library path = %q, want %q", provider.path, libraryPath)
	}
}

func TestSyncMarksObservationsStaleAndKeepsCardsSearchable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	provider := fakeProvider{snapshot: fakeSnapshot(false, false)}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	sourceID := stableID("source_library", libraryPath)
	assetID := stableID("asset", sourceID, "fixture-asset-1")
	seedObservationInvalidationRows(t, ctx, paths, assetID)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	updateResult, err := db.DB().ExecContext(ctx, `
update classification_queue
set state = 'content_classified',
    reason = 'model_observations',
    updated_at = ?
where asset_id = ?
`, fixedClock("2026-05-28T10:30:00Z")().Format(time.RFC3339Nano), assetID)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := updateResult.RowsAffected(); err != nil {
		t.Fatal(err)
	} else if rows != 1 {
		t.Fatalf("updated classified queue rows = %d, want 1", rows)
	}

	provider.snapshot = fakeSnapshot(false, false)
	provider.snapshot.Assets[0].Favorite = true
	result, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T11:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsChanged != 1 {
		t.Fatalf("changed assets = %d, want 1", result.AssetsChanged)
	}

	assertSyncSummaryNumber(t, result, "marked_stale_model_assets", 1)
	assertSyncSummaryNumber(t, result, "marked_stale_model_rows", 2)
	assertSyncSummaryNumber(t, result, "marked_stale_place_assets", 1)
	assertSyncSummaryNumber(t, result, "marked_stale_place_rows", 1)
	assertSyncSummaryNumber(t, result, "classification_queue_pending", 1)

	var modelRows, placeRows, cardFTSRows int
	var modelStaleSince, modelStaleReason, placeStaleSince, placeStaleReason string
	if err := db.DB().QueryRowContext(ctx, `
select count(*), min(stale_since), min(stale_reason)
from model_observation
where asset_id = ? and superseded_at is null
`, assetID).Scan(&modelRows, &modelStaleSince, &modelStaleReason); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `
select count(*), min(stale_since), min(stale_reason)
from place_observation
where asset_id = ? and superseded_at is null
`, assetID).Scan(&placeRows, &placeStaleSince, &placeStaleReason); err != nil {
		t.Fatal(err)
	}
	if modelRows != 2 || placeRows != 1 {
		t.Fatalf("retained rows: model=%d place=%d, want 2/1", modelRows, placeRows)
	}
	if modelStaleSince != "2026-05-28T11:00:00Z" || placeStaleSince != "2026-05-28T11:00:00Z" {
		t.Fatalf("stale_since model=%q place=%q", modelStaleSince, placeStaleSince)
	}
	if modelStaleReason != syncStaleReason || placeStaleReason != syncStaleReason {
		t.Fatalf("stale_reason model=%q place=%q", modelStaleReason, placeStaleReason)
	}
	if err := db.DB().QueryRowContext(ctx, `
select count(*)
from observation_fts
where asset_id = ? and id = 'fixture-card-summary'
`, assetID).Scan(&cardFTSRows); err != nil {
		t.Fatal(err)
	}
	if cardFTSRows != 1 {
		t.Fatalf("card fts rows = %d, want 1", cardFTSRows)
	}
	search, err := Search(ctx, paths, SearchOptions{Query: "visible", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || !search.Results[0].Stale {
		t.Fatalf("stale card search = %#v", search.Results)
	}
	opened, err := Open(ctx, paths, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Stale == nil || !strings.Contains(opened.Stale.Banner, "DEBUG INFO: THIS CARD IS STALE") {
		t.Fatalf("open stale banner = %#v", opened.Stale)
	}

	provider.snapshot.Assets[0].Hidden = true
	result, err = Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T12:00:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AssetsChanged != 1 {
		t.Fatalf("third sync changed assets = %d, want 1", result.AssetsChanged)
	}
	assertSyncSummaryNumber(t, result, "marked_stale_model_assets", 0)
	assertSyncSummaryNumber(t, result, "marked_stale_model_rows", 0)
	assertSyncSummaryNumber(t, result, "marked_stale_place_assets", 0)
	assertSyncSummaryNumber(t, result, "marked_stale_place_rows", 0)
	if err := db.DB().QueryRowContext(ctx, `
select min(stale_since)
from model_observation
where asset_id = ? and superseded_at is null
`, assetID).Scan(&modelStaleSince); err != nil {
		t.Fatal(err)
	}
	if modelStaleSince != "2026-05-28T11:00:00Z" {
		t.Fatalf("third sync stale_since = %q, want first stale mark", modelStaleSince)
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{DataDir: root, Database: filepath.Join(root, "photos.sqlite"), CacheDir: filepath.Join(root, "cache")}
}

func seedObservationInvalidationRows(t *testing.T, ctx context.Context, paths Paths, assetID string) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	for _, row := range []struct{ id, observationType, text string }{
		{"fixture-card-summary", modelObservationCardSummary, "Synthetic beach scene."},
		{"fixture-card-description", modelObservationCardDescription, "A synthetic beach fixture with visible album context."},
	} {
		if _, err := db.DB().ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, '{}', 1.0, 'model_multimodal', 'fixture-model', 'v1', '')
`, row.id, assetID, row.observationType, row.text); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, evidence_id)
values ('fixture-place', ?, 'venue', 'Synthetic Pier', '{"name":"Synthetic Pier"}', 'place_context', 'apple', 'hit', 'venue_candidate', 12, '')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('fixture-card-summary', ?, '', 'Synthetic beach scene. A synthetic beach fixture with visible album context.')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('fixture-place', ?, '', 'Synthetic Pier')
`, assetID); err != nil {
		t.Fatal(err)
	}
}

func assertSyncSummaryNumber(t *testing.T, result SyncResult, field string, want int) {
	t.Helper()
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var summary map[string]any
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	got, ok := summary[field].(float64)
	if !ok {
		t.Fatalf("sync summary missing numeric field %q in %s", field, data)
	}
	if int(got) != want {
		t.Fatalf("sync summary %s = %d, want %d in %s", field, int(got), want, data)
	}
}

func mkdirLibrary(path string) error {
	return os.MkdirAll(path, 0o755)
}

func fixedClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}

type fakeProvider struct {
	snapshot photos.LibrarySnapshot
}

func (f fakeProvider) Snapshot(context.Context, string) (photos.LibrarySnapshot, error) {
	return f.snapshot, nil
}

type pathRecordingProvider struct {
	path     string
	snapshot photos.LibrarySnapshot
}

func (p *pathRecordingProvider) Snapshot(_ context.Context, path string) (photos.LibrarySnapshot, error) {
	p.path = path
	return p.snapshot, nil
}

func fakeSnapshot(changed, includeSecond bool) photos.LibrarySnapshot {
	altitude := 12.5
	accuracy := 8.25
	focalLength := 6.86
	focalLength35MM := 24.0
	aperture := 1.8
	shutterSpeed := 0.008333333333333333
	iso := int64(64)
	snapshot := photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Metadata: map[string]any{
			"fixture": true,
		},
		Assets: []photos.Asset{
			{
				LocalIdentifier:  "fixture-asset-1",
				MediaType:        "image",
				MediaSubtypes:    "0",
				CreationDate:     "2026-05-27T10:00:00Z",
				ModificationDate: pick(changed, "2026-05-28T10:30:00Z", "2026-05-27T10:05:00Z"),
				AddedDate:        "2026-05-27T10:01:00Z",
				TimezoneName:     "Europe/Amsterdam",
				Width:            4032,
				Height:           3024,
				Favorite:         changed,
				Location: &photos.Location{
					Latitude:           52.3676,
					Longitude:          4.9041,
					Altitude:           &altitude,
					HorizontalAccuracy: &accuracy,
				},
				Camera: &photos.Camera{
					Make:            "Apple",
					Model:           "iPhone 15 Pro",
					LensModel:       "back camera",
					FocalLengthMM:   &focalLength,
					FocalLength35MM: &focalLength35MM,
					Aperture:        &aperture,
					ShutterSpeed:    &shutterSpeed,
					ISO:             &iso,
				},
				Resources: []photos.Resource{
					{Type: "photo", UTI: "public.heic", OriginalFilename: "Screenshot Beach Fixture.heic", Availability: "remote", NeedsDownload: true},
				},
				Albums: []photos.AlbumMembership{
					{AlbumID: "fixture-album-1", AlbumTitle: "Beach", AlbumKind: "album:1:2"},
				},
			},
		},
	}
	if includeSecond {
		snapshot.Assets = append(snapshot.Assets, photos.Asset{
			LocalIdentifier:  "fixture-asset-2",
			MediaType:        "video",
			MediaSubtypes:    "0",
			CreationDate:     "2026-05-27T11:00:00Z",
			ModificationDate: "2026-05-27T11:05:00Z",
			AddedDate:        "2026-05-27T11:01:00Z",
			TimezoneName:     "Europe/Amsterdam",
			Width:            1920,
			Height:           1080,
			DurationSeconds:  7.5,
			Resources: []photos.Resource{
				{Type: "video", UTI: "public.mpeg-4", OriginalFilename: "kitchen-fixture.mp4", Availability: "local", AvailableLocally: true},
			},
			Albums: []photos.AlbumMembership{
				{AlbumID: "fixture-album-2", AlbumTitle: "Kitchen", AlbumKind: "album:1:2"},
			},
		})
	}
	return snapshot
}

func pick(changed bool, ifChanged, otherwise string) string {
	if changed {
		return ifChanged
	}
	return otherwise
}

func hasStatusCount(counts []control.Count, id string) bool {
	for _, count := range counts {
		if count.ID == id {
			return true
		}
	}
	return false
}
