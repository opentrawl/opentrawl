package photospipeline

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/knownplacesfile"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
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

// Step 2, fetch_picture.

type fetchPictureStep struct{}

func (fetchPictureStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_FETCH_PICTURE
}

func (fetchPictureStep) RunPhotosStepOnOneInput(context.Context, *photosstep.FetchPictureRequest) (*photo.PictureFetchResult, error) {
	return nil, errPhotosStepNotImplemented
}

type fetchPictureBinding struct{}

func (fetchPictureBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.FetchPictureRequest, error) {
	return photosArchive.SelectApplePhotosWaitingForPictureFetchNewestFirst(
		ctx, workSelection.Limit, workSelection.SelectionTime, workSelection.RetryPermanentlyFailedWork,
		workSelection.OnlyApplePhotoUUID)
}

func (fetchPictureBinding) StorePhotosStepOutput(context.Context, *photosarchive.PhotosArchive, *photosstep.FetchPictureRequest, *photo.PictureFetchResult) error {
	return errPhotosStepNotImplemented
}

// Step 3, look_up_places_near_spot.

type lookUpPlacesNearSpotStep struct{}

func (lookUpPlacesNearSpotStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_LOOK_UP_PLACES_NEAR_SPOT
}

func (lookUpPlacesNearSpotStep) RunPhotosStepOnOneInput(context.Context, *photosstep.LookUpPlacesNearSpotRequest) (*photo.NearbyPlacesLookupResult, error) {
	return nil, errPhotosStepNotImplemented
}

type lookUpPlacesNearSpotBinding struct {
	placeLookupRequestParameters []*photosstep.PhotosPlaceLookupRequestParameters
}

func (binding lookUpPlacesNearSpotBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.LookUpPlacesNearSpotRequest, error) {
	return photosArchive.SelectCameraSpotsWaitingForPlaceLookupNewestFirst(
		ctx, workSelection.Limit, workSelection.SelectionTime, workSelection.OnlyApplePhotoUUID,
		binding.placeLookupRequestParameters, workSelection.RetryPermanentlyFailedWork)
}

func (lookUpPlacesNearSpotBinding) StorePhotosStepOutput(context.Context, *photosarchive.PhotosArchive, *photosstep.LookUpPlacesNearSpotRequest, *photo.NearbyPlacesLookupResult) error {
	return errPhotosStepNotImplemented
}

// Step 4, write_card_model_input.

type writeCardModelInputStep struct{}

func (writeCardModelInputStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_CARD_MODEL_INPUT
}

func (writeCardModelInputStep) RunPhotosStepOnOneInput(context.Context, *photosstep.WriteCardModelInputRequest) (*photo.PhotoCardModelInput, error) {
	return nil, errPhotosStepNotImplemented
}

// The known-places file is read on every selection, so a mistake in it stops
// the step at once, naming the line.
type writeCardModelInputBinding struct {
	knownPlacesFilePath          string
	placeLookupRequestParameters []*photosstep.PhotosPlaceLookupRequestParameters
}

func (binding writeCardModelInputBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.WriteCardModelInputRequest, error) {
	var knownPlaces []*knownplaces.KnownPlace
	if binding.knownPlacesFilePath != "" {
		knownPlacesFile, err := knownplacesfile.ReadKnownPlacesFile(binding.knownPlacesFilePath)
		if err != nil {
			return nil, err
		}
		knownPlaces = knownPlacesFile.GetKnownPlaces()
	}
	readyApplePhotoUUIDs, err := photosArchive.SelectApplePhotosReadyForCardModelInputNewestFirst(ctx, workSelection.Limit, workSelection.OnlyApplePhotoUUID, binding.placeLookupRequestParameters)
	if err != nil {
		return nil, err
	}
	readyApplePhotosFacts, err := photosArchive.ReadStoredAppleLibraryPhotoFacts(ctx, readyApplePhotoUUIDs)
	if err != nil {
		return nil, err
	}
	writeCardModelInputRequests := make([]*photosstep.WriteCardModelInputRequest, 0, len(readyApplePhotosFacts))
	for _, applePhotoFacts := range readyApplePhotosFacts {
		pictureFetchResult, err := photosArchive.ReadStoredPictureFetchResult(ctx, applePhotoFacts.GetApplePhotoUuid())
		if err != nil {
			return nil, err
		}
		writeCardModelInputRequest := &photosstep.WriteCardModelInputRequest{
			AppleLibraryPhotoFacts: applePhotoFacts,
			PictureFetchResult:     pictureFetchResult,
			KnownPlaces:            knownPlaces,
		}
		if cameraLocation := applePhotoFacts.GetCameraLocation(); cameraLocation != nil {
			lookupRequests := photosarchive.PlaceLookupRequestsForCameraPosition(cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude(), binding.placeLookupRequestParameters)
			if writeCardModelInputRequest.NearbyPlacesLookupResults, err = photosArchive.ReadStoredNearbyPlacesLookupResults(ctx, lookupRequests); err != nil {
				return nil, err
			}
		}
		writeCardModelInputRequests = append(writeCardModelInputRequests, writeCardModelInputRequest)
	}
	return writeCardModelInputRequests, nil
}

func (writeCardModelInputBinding) StorePhotosStepOutput(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	_ *photosstep.WriteCardModelInputRequest,
	photoCardModelInput *photo.PhotoCardModelInput,
) error {
	return photosArchive.StorePhotoCardModelInputIfChanged(ctx, photoCardModelInput, time.Now())
}

// Step 5, write_photo_card.

type writePhotoCardStep struct{}

func (writePhotoCardStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_WRITE_PHOTO_CARD
}

func (writePhotoCardStep) RunPhotosStepOnOneInput(context.Context, *photosstep.WritePhotoCardRequest) (*photo.PhotoCard, error) {
	return nil, errPhotosStepNotImplemented
}

type writePhotoCardBinding struct{}

func (writePhotoCardBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.WritePhotoCardRequest, error) {
	return photosArchive.SelectPhotoCardModelInputsWaitingForCardNewestFirst(
		ctx, workSelection.Limit, workSelection.SelectionTime, workSelection.IncludeStaleCards,
		workSelection.OnlyApplePhotoUUID, workSelection.RetryPermanentlyFailedWork)
}

func (writePhotoCardBinding) StorePhotosStepOutput(context.Context, *photosarchive.PhotosArchive, *photosstep.WritePhotoCardRequest, *photo.PhotoCard) error {
	return errPhotosStepNotImplemented
}
