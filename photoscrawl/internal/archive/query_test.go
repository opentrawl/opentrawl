package archive

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
)

func TestSearchCapsLimitAndReportsTruncation(t *testing.T) {
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

	result, err := Search(ctx, paths, SearchOptions{Query: "image", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if result.Limit != 200 {
		t.Fatalf("limit = %d, want 200", result.Limit)
	}
	if len(result.Results) != 200 {
		t.Fatalf("results = %d, want 200", len(result.Results))
	}
	if result.TotalMatches != 250 || !result.Truncated {
		t.Fatalf("search metadata = total %d truncated %t", result.TotalMatches, result.Truncated)
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
	defer db.Close()
	if _, err := db.DB().ExecContext(ctx, `
insert into evidence_ref(id, asset_id, evidence_kind, source, pointer, value_json)
values ('fixture-face-evidence', ?, 'face', 'fixture', 'face:1', '{}')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into face_observation(id, asset_id, face_local_id, person_label, confidence, bounding_box_json, source, evidence_id)
values ('fixture-face', ?, 'face-1', 'Synthetic Person', 0.9, '{}', 'fixture', 'fixture-face-evidence')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into evidence_ref(id, asset_id, evidence_kind, source, pointer, value_json)
values ('fixture-place-evidence', ?, 'content_classification', 'fixture', 'place:1', '{}')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values ('fixture-place', ?, 'merchant_or_venue_name_candidate', 'Synthetic Pier', '{"text":"Synthetic Pier"}', 0.8, 'fixture', 'fixture-model', 'fixture-prompt', 'fixture-place-evidence')
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
}

func TestOpenUsesSlimShapeWithoutRawEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: fakeSnapshot(false, true)},
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
	if opened.Ref != search.Results[0].Ref || opened.Time == "" || opened.MediaType == "" || opened.Dimensions == nil {
		t.Fatalf("open header = %#v", opened)
	}
	if len(opened.Resources) != 1 || len(opened.Albums) != 1 || opened.LocationCount != 1 || len(opened.Evidence.Refs) == 0 {
		t.Fatalf("open shape resources=%d albums=%d locations=%d evidence=%d", len(opened.Resources), len(opened.Albums), opened.LocationCount, len(opened.Evidence.Refs))
	}
	data, err := json.Marshal(opened)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"asset", "locations", "visual_observations", "model_observations"} {
		if _, ok := top[field]; ok {
			t.Fatalf("open leaked raw field %q: %s", field, data)
		}
	}
	evidence, ok := top["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("open missing evidence object: %s", data)
	}
	refs, ok := evidence["refs"].([]any)
	if !ok || len(refs) == 0 {
		t.Fatalf("open missing evidence refs: %s", data)
	}
	if _, ok := evidence["raw"]; ok {
		t.Fatalf("open leaked raw evidence branch: %s", data)
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
