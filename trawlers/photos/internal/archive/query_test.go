package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestSearchRetainsExactVisiblePhotoField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeSnapshot(false, false)
	snapshot.Assets[0].Albums = []photos.AlbumMembership{{AlbumID: "fixture-album", AlbumTitle: "Synthetic voyage", AlbumKind: "album:1:2"}}
	if _, err := Sync(ctx, paths, SyncOptions{LibraryPath: libraryPath, Provider: fakeProvider{snapshot: snapshot}, Now: fixedClock("2026-05-28T10:00:00Z")}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	assetID := stableID("asset", stableID("source_library", libraryPath), "fixture-asset-1")
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, row := range []struct{ id, kind, value string }{
			{"fixture-description", modelObservationCardDescription, "A copper lantern on a table."},
			{"fixture-ocr", modelObservationCardOCR, "SERIAL EXAMPLE 42"},
		} {
			if _, err := tx.ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, '{}', 'fixture', '', '', '')
`, row.id, assetID, row.kind, row.value); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `insert into observation_fts(id, asset_id, title, body) values (?, ?, '', ?)`, row.id, assetID, row.value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct{ query, anchor string }{
		{"Fixture", "filename"},
		{"voyage", "album"},
		{"image", "media"},
		{"lantern", "description"},
		{"SERIAL", "ocr"},
	} {
		result, err := Search(ctx, paths, SearchOptions{Query: test.query, Limit: 5})
		if err != nil {
			t.Fatalf("search %q: %v", test.query, err)
		}
		if len(result.Results) != 1 || result.Results[0].AnchorID != test.anchor || len(result.Results[0].Matches) != 1 || result.Results[0].Matches[0].Field != test.anchor {
			t.Fatalf("search %q = %#v", test.query, result.Results)
		}
		matched := false
		for _, run := range result.Results[0].Matches[0].Runs {
			matched = matched || run.Matched
		}
		if !matched {
			t.Fatalf("search %q has no marked run: %#v", test.query, result.Results[0].Matches)
		}
	}
}

func TestSearchPreservesLateMetadataAndFocusedOpenShowsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, paths, SyncOptions{LibraryPath: libraryPath, Provider: fakeProvider{snapshot: fakeSnapshot(false, false)}, Now: fixedClock("2026-05-28T10:00:00Z")}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	assetID := stableID("asset", stableID("source_library", libraryPath), "fixture-asset-1")
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for index := 0; index <= maximumOpenSignals; index++ {
			label := fmt.Sprintf("aa-signal-%03d", index)
			if index == maximumOpenSignals {
				label = "zz-beyond-limit"
			}
			id := fmt.Sprintf("fixture-metadata-%03d", index)
			if _, err := tx.ExecContext(ctx, `
insert into metadata_observation(id, asset_id, observation_type, label, source, classifier_id, evidence_id)
values (?, ?, '000-fixture', ?, 'fixture', '', '')
`, id, assetID, label); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `insert into observation_fts(id, asset_id, title, body) values (?, ?, ?, ?)`, id, assetID, label, label); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	visible, err := Search(ctx, paths, SearchOptions{Query: "signal-000", Limit: 5})
	if err != nil || len(visible.Results) != 1 || !strings.HasPrefix(visible.Results[0].AnchorID, "metadata.") {
		t.Fatalf("visible metadata search = %#v, err=%v", visible.Results, err)
	}
	focused, err := Search(ctx, paths, SearchOptions{Query: "beyond-limit", Limit: 5})
	if err != nil || len(focused.Results) != 1 || !strings.HasPrefix(focused.Results[0].AnchorID, "metadata.") {
		t.Fatalf("focused metadata search = %#v, err=%v", focused.Results, err)
	}
	readStore, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenWithStoreFocused(ctx, readStore, AssetRef(assetID), focused.Results[0].AnchorID)
	_ = readStore.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.Mechanical.Signals) != maximumOpenSignals || opened.Mechanical.SignalsTruncated == false || opened.Mechanical.Signals[len(opened.Mechanical.Signals)-1].Label != "zz-beyond-limit" {
		t.Fatalf("bounded signals = %d, truncated=%t", len(opened.Mechanical.Signals), opened.Mechanical.SignalsTruncated)
	}
}

// TestSearchHonorsLimitContract pins the one --limit contract (trawlkit/flags):
// a positive limit is honored exactly with no hidden cap, a limit above the
// match count returns every match without truncation, and limit 0 returns
// everything for internal callers.
func TestSearchHonorsLimitContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	provider := fakeProvider{snapshot: manyAssetsSnapshot(250)}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	// A positive limit is honored exactly and truncates the rest.
	result, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if result.Limit != 25 || len(result.Results) != 25 {
		t.Fatalf("limit 25: limit=%d results=%d, want 25/25", result.Limit, len(result.Results))
	}
	if result.TotalMatches != 250 || !result.Truncated {
		t.Fatalf("limit 25: total=%d truncated=%t, want 250/true", result.TotalMatches, result.Truncated)
	}

	// A limit above the match count returns every match, not truncated,
	// with no hidden 200 cap.
	result, err = Search(ctx, paths, SearchOptions{Query: "image", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 250 || result.Truncated {
		t.Fatalf("limit 500: results=%d truncated=%t, want 250/false", len(result.Results), result.Truncated)
	}

	// Limit 0 returns everything for internal callers.
	result, err = Search(ctx, paths, SearchOptions{Query: "image", Limit: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 250 || result.Truncated {
		t.Fatalf("limit 0: results=%d truncated=%t, want 250/false", len(result.Results), result.Truncated)
	}
}

func TestSearchBoundedTotalsUseOneProbeRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: manyAssetsSnapshot(3)},
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	exact, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if exact.TotalMatches != 3 || exact.TotalIsLowerBound || !exact.Truncated || len(exact.Results) != 2 {
		t.Fatalf("exact result = %#v", exact)
	}

	bounded, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 2, BoundedTotals: true})
	if err != nil {
		t.Fatal(err)
	}
	if bounded.TotalMatches != 3 || !bounded.TotalIsLowerBound || !bounded.Truncated || len(bounded.Results) != 2 {
		t.Fatalf("bounded probe result = %#v", bounded)
	}
	if !reflect.DeepEqual(bounded.Results, exact.Results) {
		t.Fatalf("bounded results include a probe or change order: %#v", bounded.Results)
	}

	underLimit, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 5, BoundedTotals: true})
	if err != nil {
		t.Fatal(err)
	}
	if underLimit.TotalMatches != 3 || underLimit.TotalIsLowerBound || underLimit.Truncated || len(underLimit.Results) != 3 {
		t.Fatalf("bounded under-limit result = %#v", underLimit)
	}
}

func TestSearchAddsWhereAndWho(t *testing.T) {
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
	sourceID := stableID("source_library", libraryPath)
	assetID := stableID("asset", sourceID, "fixture-asset-1")
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.DB().ExecContext(ctx, `
insert into face_observation(id, asset_id, face_local_id, person_label, confidence, bounding_box_json, source, evidence_id)
values ('fixture-face', ?, 'face-1', 'Synthetic Person', 0.9, '{}', 'fixture', '')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into place_observation(id, asset_id, observation_type, value_text, value_json, source, provider, cache_status, tier, distance_meters, evidence_id)
values ('fixture-place', ?, 'venue', 'Synthetic Pier', '{"name":"Synthetic Pier","category":"pier"}', 'place_context', 'apple', 'hit', 'venue_candidate', 12, '')
`, assetID); err != nil {
		t.Fatal(err)
	}

	result, err := Search(ctx, paths, SearchOptions{Query: "beach", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results = %#v", result.Results)
	}
	if result.Results[0].Who != "Synthetic Person" || result.Results[0].Where != "Synthetic Pier" {
		t.Fatalf("who/where = %#v", result.Results[0])
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("search JSON is empty")
	}
}

func TestSearchKeepsEmptyWhoWhereJSONKeys(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: manyAssetsSnapshot(1)},
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results = %#v", result.Results)
	}
	if result.Results[0].Who != "" || result.Results[0].Where != "" {
		t.Fatalf("empty who/where should stay empty: %#v", result.Results[0])
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("search JSON is empty")
	}
	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 1 {
		t.Fatalf("decoded results = %#v", decoded.Results)
	}
	for _, key := range []string{"who", "where"} {
		value, ok := decoded.Results[0][key]
		if !ok {
			t.Fatalf("search JSON omitted %q: %s", key, data)
		}
		if value != "" {
			t.Fatalf("search JSON %q = %#v, want empty string", key, value)
		}
	}
}

func TestOpenUsesSlimShapeWithoutRawEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeSnapshot(false, true)
	snapshot.Assets[0].Resources = append(snapshot.Assets[0].Resources, snapshot.Assets[0].Resources[0])
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: snapshot},
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	search, err := Search(ctx, paths, SearchOptions{Query: "beach", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, paths, search.Results[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Ref != search.Results[0].Ref || opened.Mechanical.Captured == nil || opened.Mechanical.Media == nil {
		t.Fatalf("open header = %#v", opened)
	}
	if opened.Mechanical.Original == nil {
		t.Fatalf("open shape original=%#v", opened.Mechanical.Original)
	}
	data, err := json.Marshal(opened)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"asset", "locations", "metadata_observations", "visual_observations", "model_observations", "resources", "observations", "albums"} {
		if _, ok := top[field]; ok {
			t.Fatalf("open leaked raw field %q: %s", field, data)
		}
	}
	if _, ok := top["evidence"]; ok {
		t.Fatalf("open leaked evidence object: %s", data)
	}
}

func TestOpenDedupesAlbumTitles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeSnapshot(false, false)
	snapshot.Assets[0].Albums = append(snapshot.Assets[0].Albums,
		photos.AlbumMembership{AlbumID: "fixture-album-duplicate", AlbumTitle: "Beach", AlbumKind: "album:1:2"},
		photos.AlbumMembership{AlbumID: "fixture-album-spaced", AlbumTitle: "  Beach  ", AlbumKind: "album:1:2"},
		photos.AlbumMembership{AlbumID: "fixture-album-other", AlbumTitle: "Beach ideas", AlbumKind: "album:1:2"},
	)
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: snapshot},
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	sourceID := stableID("source_library", libraryPath)
	assetID := stableID("asset", sourceID, "fixture-asset-1")
	opened, err := Open(ctx, paths, AssetRef(assetID))
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, album := range opened.Mechanical.Albums {
		titles = append(titles, album.Title)
	}
	want := []string{"Beach", "Beach ideas"}
	if !reflect.DeepEqual(titles, want) {
		t.Fatalf("album titles = %#v, want %#v", titles, want)
	}
}

func manyAssetsSnapshot(count int) photos.LibrarySnapshot {
	snapshot := photos.LibrarySnapshot{
		Provider:      "fake",
		PhotosVersion: "fixture",
		Assets:        make([]photos.Asset, 0, count),
	}
	for i := 0; i < count; i++ {
		snapshot.Assets = append(snapshot.Assets, photos.Asset{
			LocalIdentifier: fmt.Sprintf("fixture-search-asset-%03d", i),
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-27T10:00:00Z",
			Width:           100,
			Height:          80,
		})
	}
	return snapshot
}
