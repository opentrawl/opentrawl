package archive

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/store"
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
	if result.SourceLibraryID != photos.SourceLibraryID(libraryPath) {
		t.Fatalf("source library ID = %q, want %q", result.SourceLibraryID, photos.SourceLibraryID(libraryPath))
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
	if opened.Mechanical.Source.State != sourceStateCurrent {
		t.Fatalf("open JSON source state = %#v", opened.Mechanical.Source)
	}

	classified, err := Classify(ctx, paths, ClassifyOptions{
		Now: fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if classified.Processed != 2 || classified.MetadataClassified != 2 || classified.WaitingForLocalContent != 1 || classified.MetadataObservationsWritten == 0 {
		t.Fatalf("classify result = processed %d metadata %d waiting %d observations %d", classified.Processed, classified.MetadataClassified, classified.WaitingForLocalContent, classified.MetadataObservationsWritten)
	}
	observationSearch, err := Search(ctx, paths, SearchOptions{Query: "candidate", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(observationSearch.Results) != 1 {
		t.Fatalf("observation search = %#v", observationSearch.Results)
	}
	if !strings.HasPrefix(observationSearch.Results[0].AnchorID, "metadata.") || len(observationSearch.Results[0].Matches) == 0 || observationSearch.Results[0].Matches[0].Field != "metadata" {
		t.Fatalf("observation match = %#v", observationSearch.Results[0])
	}
	opened, err = Open(ctx, paths, observationSearch.Results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(opened.Mechanical.Signals, func(signal OpenSignal) bool { return signal.Label == "screenshot_candidate" }) {
		t.Fatalf("open signals = %#v", opened.Mechanical.Signals)
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
	if len(status.Counts) != 5 {
		t.Fatalf("status counts = %#v", status.Counts)
	}
	for _, id := range []string{"photos", "since", "queued_for_classify", "queued_needs_download", "classification_queue_pending"} {
		if !hasStatusCount(status.Counts, id) {
			t.Fatalf("missing status count %q in %#v", id, status.Counts)
		}
	}
	if !hasStatusCountValue(status.Counts, "queued_for_classify", 2) ||
		!hasStatusCountValue(status.Counts, "queued_needs_download", 1) ||
		!hasStatusCountValue(status.Counts, "classification_queue_pending", 0) {
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

func TestSyncWithStorePreparesSchemaBeforeSnapshotFailure(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{Path: paths.Database})
	if err != nil {
		t.Fatal(err)
	}
	_, err = SyncWithStore(ctx, db, paths, SyncOptions{
		LibraryPath: filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary"),
		Provider:    failingProvider{err: errors.New("snapshot failed")},
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	})
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err == nil || !strings.Contains(err.Error(), "snapshot failed") {
		t.Fatalf("sync error = %v, want snapshot failure", err)
	}
	status, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "empty" {
		t.Fatalf("status state = %q, want empty", status.State)
	}
	if !hasStatusCountValue(status.Counts, "photos", 0) ||
		!hasStatusCountValue(status.Counts, "queued_for_classify", 0) ||
		!hasStatusCountValue(status.Counts, "classification_queue_pending", 0) {
		t.Fatalf("status counts after failed first sync = %#v", status.Counts)
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()
	root := t.TempDir()
	return Paths{DataDir: root, Database: filepath.Join(root, "photoscrawl.db"), CacheDir: filepath.Join(root, "cache")}
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
	if f.snapshot.Completeness.State == "" {
		f.snapshot.Completeness = photos.SnapshotCompleteness{
			State:    photos.SnapshotComplete,
			Evidence: map[string]string{"fixture": "complete"},
		}
	}
	return f.snapshot, nil
}

type failingProvider struct {
	err error
}

func (f failingProvider) Snapshot(context.Context, string) (photos.LibrarySnapshot, error) {
	return photos.LibrarySnapshot{}, f.err
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
		Completeness: photos.SnapshotCompleteness{
			State:    photos.SnapshotComplete,
			Evidence: map[string]string{"fixture": "complete"},
		},
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

func hasStatusCountValue(counts []control.Count, id string, value int64) bool {
	for _, count := range counts {
		if count.ID == id && count.Value == value {
			return true
		}
	}
	return false
}
