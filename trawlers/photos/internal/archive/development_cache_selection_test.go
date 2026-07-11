package archive

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestDevelopmentCacheSelectionUsesLatestCompleteCurrentSourceSnapshot(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Synthetic Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	first := developmentCacheSnapshot("complete", developmentCacheAsset("current-asset", 120), developmentCacheAsset("deleted-asset", 80))
	firstResult := syncSourceStateSnapshot(t, ctx, paths, libraryPath, first, "2026-07-11T08:00:00Z")
	second := developmentCacheSnapshot("complete", developmentCacheAsset("current-asset", 120))
	secondResult := syncSourceStateSnapshot(t, ctx, paths, libraryPath, second, "2026-07-11T09:00:00Z")

	for index, state := range []photos.SnapshotCompletenessState{photos.SnapshotPartial, photos.SnapshotFailed} {
		incomplete := developmentCacheSnapshot(state, developmentCacheAsset("incomplete-only-asset", 240))
		syncIncompleteSourceStateSnapshot(t, ctx, paths, libraryPath, incomplete, []string{"2026-07-11T10:00:00Z", "2026-07-11T11:00:00Z"}[index])
	}

	otherLibrary := filepath.Join(t.TempDir(), "Other Synthetic Photos Library.photoslibrary")
	if err := mkdirLibrary(otherLibrary); err != nil {
		t.Fatal(err)
	}
	syncSourceStateSnapshot(t, ctx, paths, otherLibrary, developmentCacheSnapshot("complete", developmentCacheAsset("other-source-asset", 360)), "2026-07-11T12:00:00Z")

	selection, err := SelectDevelopmentCacheAssets(ctx, paths.Database, firstResult.SourceLibraryID)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(selection)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=development_cache_selection input={\"archive\":%q,\"source_library_id\":%q} output=%s", paths.Database, firstResult.SourceLibraryID, output)
	if selection.SnapshotID != secondResult.SnapshotID || len(selection.Assets) != 1 {
		t.Fatalf("selection = %#v, want one asset from latest complete snapshot %q", selection, secondResult.SnapshotID)
	}
	asset := selection.Assets[0]
	if asset.Request.Query.LocalIdentifier != "current-asset" || asset.CacheKey == "" {
		t.Fatalf("selected asset = %#v", asset)
	}
}

func developmentCacheSnapshot(state photos.SnapshotCompletenessState, assets ...photos.Asset) photos.LibrarySnapshot {
	return photos.LibrarySnapshot{
		Provider:      "synthetic",
		PhotosVersion: "synthetic-1",
		Completeness: photos.SnapshotCompleteness{
			State:    state,
			Evidence: map[string]string{"synthetic_state": string(state)},
		},
		Assets: assets,
	}
}

func developmentCacheAsset(localIdentifier string, size int64) photos.Asset {
	return photos.Asset{
		LocalIdentifier:  localIdentifier,
		MediaType:        "image",
		CreationDate:     "2026-07-11T07:00:00Z",
		ModificationDate: "2026-07-11T07:05:00Z",
		AddedDate:        "2026-07-11T07:01:00Z",
		TimezoneName:     "UTC",
		Width:            1600,
		Height:           1200,
		Resources: []photos.Resource{{
			Type:             "photo",
			UTI:              "public.heic",
			OriginalFilename: localIdentifier + ".heic",
			Availability:     "remote",
			FileSize:         size,
			NeedsDownload:    true,
		}},
	}
}
