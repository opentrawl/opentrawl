package photos

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturefetch"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
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
	)

	sanitized := pathLeakyPhoto.GetPictureFetchResult().GetPictureFetchFailureMessage()
	if strings.Contains(sanitized, "/Users/") {
		t.Fatalf("failure message leaked filesystem path: %q", sanitized)
	}
	if sanitized == "" {
		t.Fatal("failure message should not be empty")
	}
}
