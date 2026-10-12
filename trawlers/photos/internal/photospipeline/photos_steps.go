package photospipeline

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

// Step 1, read_apple_library, is built in package applelibrary. Its binding
// always selects one request: the library, narrowed by --photo or --limit.

type readAppleLibraryBinding struct {
	applePhotosLibraryPath          string
	appleLibrarySnapshotStoreCounts photosarchive.AppleLibrarySnapshotStoreCounts
}

func (binding *readAppleLibraryBinding) SelectPendingPhotosStepInputsNewestFirst(
	_ context.Context,
	_ *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.ReadAppleLibraryRequest, error) {
	readAppleLibraryRequest := &photosstep.ReadAppleLibraryRequest{
		ApplePhotosLibraryPath:      binding.applePhotosLibraryPath,
		ReadWithoutCreatingSnapshot: workSelection.ReadWithoutCreatingSnapshot,
	}
	if workSelection.OnlyApplePhotoUUID != "" {
		readAppleLibraryRequest.SelectedApplePhotoUuids = []string{workSelection.OnlyApplePhotoUUID}
	}
	if workSelection.Limit != photosarchive.NoWorkListLimit {
		newestPhotoLimit := int32(workSelection.Limit)
		readAppleLibraryRequest.NewestPhotoLimit = &newestPhotoLimit
	}
	return []*photosstep.ReadAppleLibraryRequest{readAppleLibraryRequest}, nil
}

func (binding *readAppleLibraryBinding) StorePhotosStepOutput(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	readAppleLibraryRequest *photosstep.ReadAppleLibraryRequest,
	appleLibrarySnapshot *photosstep.AppleLibrarySnapshot,
) error {
	snapshotCoversWholeAppleLibrary := len(readAppleLibraryRequest.GetSelectedApplePhotoUuids()) == 0 &&
		readAppleLibraryRequest.NewestPhotoLimit == nil
	storeCounts, err := photosArchive.StoreAppleLibrarySnapshot(ctx, appleLibrarySnapshot, snapshotCoversWholeAppleLibrary, time.Now())
	binding.appleLibrarySnapshotStoreCounts = storeCounts
	return err
}
