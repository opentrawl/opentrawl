package photospipeline

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
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

// Step 3, look_up_places_near_spot, is built in package placelookup. Its
// binding selects one exact request per camera spot that has no stored answer.

type lookUpPlacesNearSpotBinding struct{}

func (lookUpPlacesNearSpotBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.LookUpPlacesNearSpotRequest, error) {
	return photosArchive.SelectCameraSpotsWaitingForPlaceLookupNewestFirst(
		ctx,
		workSelection.Limit,
		workSelection.SelectionTime,
		workSelection.OnlyApplePhotoUUID,
		workSelection.RetryPermanentlyFailedWork,
		func(cameraSpot photosarchive.CameraSpot) *photosstep.LookUpPlacesNearSpotRequest {
			return placelookup.PhotonReverseLookupRequestForCameraSpot(
				cameraSpot.CameraSpotKey, cameraSpot.CameraSpotLatitude, cameraSpot.CameraSpotLongitude)
		},
	)
}

func (lookUpPlacesNearSpotBinding) StorePhotosStepOutput(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	_ *photosstep.LookUpPlacesNearSpotRequest,
	nearbyPlacesLookupResult *photo.NearbyPlacesLookupResult,
) error {
	return photosArchive.StoreNearbyPlacesLookupResult(ctx, nearbyPlacesLookupResult, time.Now())
}
