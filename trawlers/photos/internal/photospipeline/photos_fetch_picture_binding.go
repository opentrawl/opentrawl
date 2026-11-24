package photospipeline

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/picturecache"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

// Step 2, fetch_picture, is built in package picturefetch.

type fetchPictureBinding struct {
	pictureCache              *picturecache.PictureCache
	pictureLongEdgePixelLimit int32
}

func (binding fetchPictureBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.FetchPictureRequest, error) {
	var cachedPictureExists func(string) bool
	if binding.pictureCache != nil {
		cachedPictureExists = func(pictureSHA256 string) bool {
			_, cached := binding.pictureCache.CachedPictureFilePath(pictureSHA256)
			return cached
		}
	}
	return photosArchive.SelectApplePhotosWaitingForPictureFetchNewestFirst(ctx, workSelection.Limit, workSelection.SelectionTime,
		workSelection.RetryPermanentlyFailedWork, workSelection.OnlyApplePhotoUUID, binding.pictureLongEdgePixelLimit, cachedPictureExists)
}

func (fetchPictureBinding) StorePhotosStepOutput(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	_ *photosstep.FetchPictureRequest,
	pictureFetchResult *photo.PictureFetchResult,
) error {
	return photosArchive.StorePictureFetchResult(ctx, pictureFetchResult, time.Now())
}
