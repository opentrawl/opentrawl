package archive

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

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
	if _, err := Crawl(ctx, paths, CrawlOptions{
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
