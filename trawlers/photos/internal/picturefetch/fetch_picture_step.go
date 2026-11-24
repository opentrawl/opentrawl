// Package picturefetch is step 2, fetch_picture: it makes each photo's
// picture into an upright JPEG a model can read, and reads the compass
// bearing from the picture file.
package picturefetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrApplePhotosAccessNotGranted stops a run: without access every photo
// would fail, and none of those failures is about the photo.
var ErrApplePhotosAccessNotGranted = errors.New("OpenTrawl does not have full access to Apple Photos")

const (
	// DefaultPictureLongEdgePixelLimit matches the largest long edge current
	// frontier vision models read without downscaling (Claude 2576 px, Gemini
	// high-resolution tiling).
	DefaultPictureLongEdgePixelLimit int32 = 2576
	pictureFetchRetryDelay                 = time.Hour
)

// FetchPictureStep reads the picture from the picture mirror folder when it
// has the photo's current edit, and from Apple Photos otherwise.
type FetchPictureStep struct {
	// Nil when the person has no picture mirror folder.
	PictureMirrorFolder *PictureMirrorFolder
	// Nil when the run stores nothing.
	PictureCache *picturecache.PictureCache
	// Maximum pixel count on the picture's long edge. Zero uses
	// DefaultPictureLongEdgePixelLimit.
	PictureLongEdgePixelLimit int32
}

func (step FetchPictureStep) ResolvedPictureLongEdgePixelLimit() int32 {
	if step.PictureLongEdgePixelLimit <= 0 {
		return DefaultPictureLongEdgePixelLimit
	}
	return step.PictureLongEdgePixelLimit
}

type fetchedUprightPicture struct {
	pictureJPEGBytes       []byte
	cameraBearingDegrees   *float64
	cameraBearingReference photo.CameraBearingReference
}

type pictureFetchFailure struct {
	pictureFetchFailureMessage string
	failureMayPassOnRetry      bool
}

func (failure *pictureFetchFailure) Error() string { return failure.pictureFetchFailureMessage }

func (FetchPictureStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE
}

func (step FetchPictureStep) RunPhotosStepOnOneInput(ctx context.Context, fetchPictureRequest *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pixelLimit := step.ResolvedPictureLongEdgePixelLimit()
	pictureFetchResult := &photo.PictureFetchResult{
		ApplePhotoUuid:                   fetchPictureRequest.GetApplePhotoUuid(),
		FetchedForAppleAdjustmentsState:  fetchPictureRequest.GetAppleAdjustmentsState(),
		FetchedForAppleAdjustmentTimeUtc: fetchPictureRequest.GetAppleAdjustmentTimeUtc(),
		PictureLongEdgePixelLimitUsed:    &pixelLimit,
	}
	fetchedPicture, pictureSource, err := step.fetchUprightPicture(fetchPictureRequest, pixelLimit)
	pictureFetchResult.PictureSource = pictureSource
	var failure *pictureFetchFailure
	if errors.As(err, &failure) {
		pictureFetchResult.PictureFetchOutcome = photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED
		pictureFetchResult.PictureFetchFailureMessage = failure.pictureFetchFailureMessage
		if failure.failureMayPassOnRetry {
			pictureFetchResult.RetryNotBeforeTimeUtc = timestamppb.New(time.Now().Add(pictureFetchRetryDelay))
		}
		return pictureFetchResult, nil
	}
	if err != nil {
		return nil, err
	}
	pictureSHA256 := sha256.Sum256(fetchedPicture.pictureJPEGBytes)
	pictureByteCount := int64(len(fetchedPicture.pictureJPEGBytes))
	pictureFetchResult.PictureFetchOutcome = photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED
	pictureFetchResult.PictureSha256 = hex.EncodeToString(pictureSHA256[:])
	pictureFetchResult.PictureByteCount = &pictureByteCount
	pictureFetchResult.CameraBearingDegrees = fetchedPicture.cameraBearingDegrees
	pictureFetchResult.CameraBearingReference = fetchedPicture.cameraBearingReference
	if step.PictureCache != nil {
		if err := step.PictureCache.StorePicture(pictureFetchResult.GetPictureSha256(), fetchedPicture.pictureJPEGBytes); err != nil {
			return nil, err
		}
	}
	return pictureFetchResult, nil
}

func (step FetchPictureStep) fetchUprightPicture(fetchPictureRequest *photosstep.FetchPictureRequest, pixelLimit int32) (fetchedUprightPicture, photo.PictureSource, error) {
	if step.PictureMirrorFolder != nil {
		pictureFilePath, err := step.PictureMirrorFolder.FindPictureFile(fetchPictureRequest)
		if err != nil {
			return fetchedUprightPicture{}, photo.PictureSource_PICTURE_SOURCE_PICTURE_MIRROR_FOLDER, err
		}
		if pictureFilePath != "" {
			fetchedPicture, err := fetchUprightPictureFromFile(pictureFilePath, fetchPictureRequest.GetAppleMediaKind() == appleMediaKindVideo, pixelLimit)
			return fetchedPicture, photo.PictureSource_PICTURE_SOURCE_PICTURE_MIRROR_FOLDER, err
		}
	}
	fetchedPicture, err := fetchUprightPictureFromApplePhotos(fetchPictureRequest.GetApplePhotoUuid(), pixelLimit)
	if errors.Is(err, ErrApplePhotosAccessNotGranted) && step.PictureMirrorFolder != nil {
		return fetchedUprightPicture{}, photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS, &pictureFetchFailure{
			pictureFetchFailureMessage: "picture is not in the picture mirror folder, and OpenTrawl does not have full access to Apple Photos",
			failureMayPassOnRetry:      true,
		}
	}
	return fetchedPicture, photo.PictureSource_PICTURE_SOURCE_APPLE_PHOTOS, err
}
