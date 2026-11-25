package photos

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturefetch"
	"github.com/opentrawl/opentrawl/trawlkit"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestOnDemandPhotoHydrationPreservesApplePhotosPermissionFailure(t *testing.T) {
	openedPhoto := &photo.OpenedPhotoRecord{
		AppleLibraryPhotoFacts: &photo.AppleLibraryPhotoFacts{
			ApplePhotoUuid: "00000000-0000-0000-0000-000000000001",
		},
	}

	hydrateOpenedPhotoPicture(
		context.Background(),
		openedPhoto,
		func(context.Context, *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
			return nil, picturefetch.ErrApplePhotosAccessNotGranted
		},
		func(string) (string, bool) { return "", false },
		nil,
	)

	result := openedPhoto.GetPictureFetchResult()
	if result.GetPictureFetchOutcome() != photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED {
		t.Fatalf("outcome = %v, want FAILED", result.GetPictureFetchOutcome())
	}
	if !strings.Contains(result.GetPictureFetchFailureMessage(), "Apple Photos") {
		t.Fatalf("failure message = %q, want Apple Photos explanation", result.GetPictureFetchFailureMessage())
	}
	if openedPhoto.GetCachedPictureFilePath() != "" {
		t.Fatalf("cached path = %q, want empty", openedPhoto.GetCachedPictureFilePath())
	}
}

func TestOnDemandPhotoHydrationPreservesFailedFetchResultWithoutLeakingPaths(t *testing.T) {
	openedPhoto := &photo.OpenedPhotoRecord{
		AppleLibraryPhotoFacts: &photo.AppleLibraryPhotoFacts{
			ApplePhotoUuid: "00000000-0000-0000-0000-000000000002",
		},
	}

	hydrateOpenedPhotoPicture(
		context.Background(),
		openedPhoto,
		func(context.Context, *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
			return &photo.PictureFetchResult{
				ApplePhotoUuid:             "00000000-0000-0000-0000-000000000002",
				PictureFetchOutcome:        photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED,
				PictureFetchFailureMessage: "Apple Photos did not deliver the picture: iCloud download failed",
				PictureSource:              photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS,
			}, nil
		},
		func(string) (string, bool) { return "", false },
		nil,
	)

	if got := openedPhoto.GetPictureFetchResult().GetPictureFetchFailureMessage(); got != "Apple Photos did not deliver the picture: iCloud download failed" {
		t.Fatalf("failure message = %q, want exact iCloud failure detail", got)
	}

	pathLeakyPhoto := &photo.OpenedPhotoRecord{
		AppleLibraryPhotoFacts: &photo.AppleLibraryPhotoFacts{
			ApplePhotoUuid: "00000000-0000-0000-0000-000000000003",
		},
	}
	hydrateOpenedPhotoPicture(
		context.Background(),
		pathLeakyPhoto,
		func(context.Context, *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
			return nil, errors.New("open /Users/example/Pictures/photo.jpg: permission denied")
		},
		func(string) (string, bool) { return "", false },
		nil,
	)

	sanitized := pathLeakyPhoto.GetPictureFetchResult().GetPictureFetchFailureMessage()
	if strings.Contains(sanitized, "/Users/") {
		t.Fatalf("failure message leaked filesystem path: %q", sanitized)
	}
	if sanitized == "" {
		t.Fatal("failure message should not be empty")
	}
}

func setupTempPhotosArchiveWithOnePhoto(t *testing.T, applePhotoUUID string) string {
	t.Helper()
	ctx := context.Background()
	archivePath := filepath.Join(t.TempDir(), "photos.db")
	writableStore, err := store.Open(ctx, store.Options{Path: archivePath})
	if err != nil {
		t.Fatalf("open writable photos store: %v", err)
	}
	defer func() { _ = writableStore.Close() }()

	archive, err := photosarchive.UsePhotosArchive(ctx, writableStore)
	if err != nil {
		t.Fatalf("initialize photos archive schema: %v", err)
	}
	captureTime := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	snapshot := &photosstep.AppleLibrarySnapshot{
		AppleLibraryPhotosNewestFirst: []*photo.AppleLibraryPhotoFacts{{
			ApplePhotoUuid:    applePhotoUUID,
			AppleLibraryScope: photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_PERSONAL_LIBRARY,
			CaptureLocalTime: &photo.PhotoCaptureLocalTime{
				CaptureTimeUtc:        timestamppb.New(captureTime),
				CaptureTimeZoneSource: photo.PhotoCaptureTimeZoneSource_PHOTO_CAPTURE_TIME_ZONE_SOURCE_NOT_KNOWN,
			},
			PixelWidth:  1000,
			PixelHeight: 800,
		}},
	}
	if _, err := archive.StoreAppleLibrarySnapshot(ctx, snapshot, true, captureTime); err != nil {
		t.Fatalf("store photo snapshot: %v", err)
	}
	return archivePath
}

func openPhotoRecordInReadOnlyRequest(
	t *testing.T,
	ctx context.Context,
	crawler *Crawler,
	archivePath string,
	applePhotoUUID string,
) *photo.OpenedPhotoRecord {
	t.Helper()
	readOnlyStore, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		t.Fatalf("open read-only photos store: %v", err)
	}
	defer func() { _ = readOnlyStore.Close() }()

	req := &trawlkit.TrawlerCommandExecutionRequest{
		OpenedTrawlerArchiveStore: readOnlyStore,
		TrawlerArchivePaths: trawlkit.TrawlerArchivePaths{
			TrawlerArchivePath: archivePath,
		},
	}
	record, err := crawler.OpenRecord(ctx, req, trawlkit.NewLocalTrawlerShortReference(applePhotoUUID))
	if err != nil {
		t.Fatalf("OpenRecord(%s): %v", applePhotoUUID, err)
	}
	return record.GetOpenedPhotoRecord()
}

func TestOnDemandPhotoHydrationPersistsPictureFetchResultAndSkipsWhenRequested(t *testing.T) {
	const applePhotoUUID = "00000000-0000-0000-0000-000000000010"
	archivePath := setupTempPhotosArchiveWithOnePhoto(t, applePhotoUUID)
	cache := picturecache.NewPictureCache(picturecache.PictureCacheFolderPath(archivePath))
	jpegBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0xff, 0xd9}
	digest := sha256.Sum256(jpegBytes)
	pictureSHA256 := hex.EncodeToString(digest[:])

	var fetchCalls atomic.Int32
	crawler := New()
	crawler.onDemandPictureFetchOverrideFunc = func(_ context.Context, _ string, req *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
		fetchCalls.Add(1)
		if err := cache.StorePicture(pictureSHA256, jpegBytes); err != nil {
			return nil, err
		}
		return &photo.PictureFetchResult{
			ApplePhotoUuid:                  req.GetApplePhotoUuid(),
			FetchedForAppleAdjustmentsState: req.GetAppleAdjustmentsState(),
			PictureFetchOutcome:             photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED,
			PictureSource:                   photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS,
			PictureSha256:                   pictureSHA256,
			PictureByteCount:                proto.Int64(int64(len(jpegBytes))),
		}, nil
	}

	// 1. Metadata-only open skips on-demand hydration (used by GET /app/open).
	metadataOnly := openPhotoRecordInReadOnlyRequest(
		t,
		trawlkit.WithSkipOnDemandPhotoPictureHydration(context.Background()),
		crawler,
		archivePath,
		applePhotoUUID,
	)
	if fetchCalls.Load() != 0 {
		t.Fatalf("fetchCalls after skip-hydration open = %d, want 0", fetchCalls.Load())
	}
	if metadataOnly.GetCachedPictureFilePath() != "" {
		t.Fatalf("cached path after skip-hydration open = %q, want empty", metadataOnly.GetCachedPictureFilePath())
	}

	// 2. Normal open hydrates on demand and persists PictureFetchResult to photos.db.
	firstHydrated := openPhotoRecordInReadOnlyRequest(
		t,
		context.Background(),
		crawler,
		archivePath,
		applePhotoUUID,
	)
	if fetchCalls.Load() != 1 {
		t.Fatalf("fetchCalls after first hydrating open = %d, want 1", fetchCalls.Load())
	}
	if firstHydrated.GetCachedPictureFilePath() == "" {
		t.Fatal("cached path after first hydrating open should not be empty")
	}

	// 3. Subsequent open reads persisted picture_fetch from photos.db and hits cache without fetching again.
	secondOpen := openPhotoRecordInReadOnlyRequest(
		t,
		context.Background(),
		crawler,
		archivePath,
		applePhotoUUID,
	)
	if fetchCalls.Load() != 1 {
		t.Fatalf("fetchCalls after second open = %d, want 1 (should hit persisted cache)", fetchCalls.Load())
	}
	if secondOpen.GetCachedPictureFilePath() != firstHydrated.GetCachedPictureFilePath() {
		t.Fatalf("second open cached path = %q, want %q", secondOpen.GetCachedPictureFilePath(), firstHydrated.GetCachedPictureFilePath())
	}

	// 4. Subsequent skip-hydration open also sees the persisted cached path immediately.
	metadataAfterPersist := openPhotoRecordInReadOnlyRequest(
		t,
		trawlkit.WithSkipOnDemandPhotoPictureHydration(context.Background()),
		crawler,
		archivePath,
		applePhotoUUID,
	)
	if metadataAfterPersist.GetCachedPictureFilePath() != firstHydrated.GetCachedPictureFilePath() {
		t.Fatalf("metadata-only open cached path after persist = %q, want %q", metadataAfterPersist.GetCachedPictureFilePath(), firstHydrated.GetCachedPictureFilePath())
	}
}

func TestOnDemandPhotoHydrationDeduplicatesConcurrentFetchesForSamePhotoUUID(t *testing.T) {
	const applePhotoUUID = "00000000-0000-0000-0000-000000000020"
	archivePath := setupTempPhotosArchiveWithOnePhoto(t, applePhotoUUID)
	cache := picturecache.NewPictureCache(picturecache.PictureCacheFolderPath(archivePath))
	jpegBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x01, 0xff, 0xd9}
	digest := sha256.Sum256(jpegBytes)
	pictureSHA256 := hex.EncodeToString(digest[:])

	var fetchCalls atomic.Int32
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	var closeStartedOnce sync.Once

	crawler := New()
	crawler.onDemandPictureFetchOverrideFunc = func(_ context.Context, _ string, req *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
		fetchCalls.Add(1)
		closeStartedOnce.Do(func() { close(fetchStarted) })
		<-releaseFetch
		if err := cache.StorePicture(pictureSHA256, jpegBytes); err != nil {
			return nil, err
		}
		return &photo.PictureFetchResult{
			ApplePhotoUuid:                  req.GetApplePhotoUuid(),
			FetchedForAppleAdjustmentsState: req.GetAppleAdjustmentsState(),
			PictureFetchOutcome:             photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED,
			PictureSource:                   photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS,
			PictureSha256:                   pictureSHA256,
			PictureByteCount:                proto.Int64(int64(len(jpegBytes))),
		}, nil
	}

	const concurrentCallers = 6
	results := make([]*photo.OpenedPhotoRecord, concurrentCallers)
	var waitGroup sync.WaitGroup
	waitGroup.Add(concurrentCallers)

	go func() {
		defer waitGroup.Done()
		results[0] = openPhotoRecordInReadOnlyRequest(t, context.Background(), crawler, archivePath, applePhotoUUID)
	}()
	<-fetchStarted

	for callerIndex := 1; callerIndex < concurrentCallers; callerIndex++ {
		index := callerIndex
		go func() {
			defer waitGroup.Done()
			results[index] = openPhotoRecordInReadOnlyRequest(t, context.Background(), crawler, archivePath, applePhotoUUID)
		}()
	}

	// Give follower goroutines a moment to attach to the in-flight fetch before releasing.
	time.Sleep(40 * time.Millisecond)
	close(releaseFetch)
	waitGroup.Wait()

	if gotCalls := fetchCalls.Load(); gotCalls != 1 {
		t.Fatalf("fetchCalls across %d concurrent opens = %d, want 1", concurrentCallers, gotCalls)
	}
	wantPath, ok := cache.CachedPictureFilePath(pictureSHA256)
	if !ok {
		t.Fatal("cached JPEG missing after deduplicated fetch")
	}
	for callerIndex, record := range results {
		if record.GetCachedPictureFilePath() != wantPath {
			t.Fatalf("caller %d cached path = %q, want %q", callerIndex, record.GetCachedPictureFilePath(), wantPath)
		}
	}
}
