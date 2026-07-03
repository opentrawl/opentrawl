package archive

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/openclaw/crawlkit/conformance"
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
values ('fixture-place', ?, ?, 'Synthetic Pier', '{"text":"Synthetic Pier"}', null, 'fixture', 'fixture-model', 'fixture-prompt', 'fixture-place-evidence')
`, assetID, modelObservationCardPlacePhrase); err != nil {
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
	conformance.AssertSearchEnvelope(t, data)
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
	conformance.AssertSearchEnvelope(t, data)
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
	if opened.Ref != search.Results[0].Ref || opened.Time == "" || opened.MediaType == "" {
		t.Fatalf("open header = %#v", opened)
	}
	if opened.Original == nil || opened.Evidence.Count == 0 || opened.Evidence.Ref != opened.Ref {
		t.Fatalf("open shape original=%#v evidence=%#v", opened.Original, opened.Evidence)
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
	evidence, ok := top["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("open missing evidence object: %s", data)
	}
	if _, ok := evidence["count"].(float64); !ok {
		t.Fatalf("open missing evidence count: %s", data)
	}
	if _, ok := evidence["ref"].(string); !ok {
		t.Fatalf("open missing evidence ref: %s", data)
	}
}

func TestEvidenceRefsUsePlainLabelsAndKeepRawSource(t *testing.T) {
	t.Parallel()
	refs := openEvidenceRefs([]map[string]any{
		{
			"id":            "fixture-classification",
			"asset_id":      "fixture-asset",
			"evidence_kind": "classification_input",
			"source":        "archive_metadata",
		},
		{
			"id":            "fixture-snapshot",
			"asset_id":      "fixture-asset",
			"evidence_kind": "asset_metadata",
			"source":        "photos_sqlite_snapshot",
		},
	})
	if len(refs) != 2 {
		t.Fatalf("refs = %#v", refs)
	}
	if refs[0].Kind != "classification input" || refs[0].KindID != "classification_input" {
		t.Fatalf("classification kind labels = %#v", refs[0])
	}
	if refs[0].Source != "Photo metadata" || refs[0].SourceID != "archive_metadata" {
		t.Fatalf("classification source labels = %#v", refs[0])
	}
	if refs[0].Summary != "derived from photo metadata" {
		t.Fatalf("classification summary = %q", refs[0].Summary)
	}
	if refs[1].Source != "Photos library database" || refs[1].SourceID != "photos_sqlite_snapshot" {
		t.Fatalf("snapshot source labels = %#v", refs[1])
	}
	if refs[1].Summary != "details from the Photos library database" {
		t.Fatalf("snapshot summary = %q", refs[1].Summary)
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
