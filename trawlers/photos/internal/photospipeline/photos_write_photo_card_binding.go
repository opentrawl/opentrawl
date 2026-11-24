package photospipeline

import (
	"context"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photocard"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photosarchive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/knownplaces"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

// Step 4, write_photo_card, is built in package photocard. Its binding renders
// each ready photo's card model input from the stored facts when it selects
// the photo; the input is never stored on its own.

// ReadPhotoCardModelInputSources gathers what one photo's card model input is
// made from. The picture fetch and the nearby places are nil when their steps
// have not run for the photo.
func ReadPhotoCardModelInputSources(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	applePhotoUUID string,
	knownPlacesFile *knownplaces.KnownPlacesFile,
) (photocard.PhotoCardModelInputSources, bool, error) {
	storedFacts, err := photosArchive.ReadStoredAppleLibraryPhotoFacts(ctx, []string{applePhotoUUID})
	if err != nil || len(storedFacts) == 0 {
		return photocard.PhotoCardModelInputSources{}, false, err
	}
	sources := photocard.PhotoCardModelInputSources{AppleLibraryPhotoFacts: storedFacts[0]}
	if sources.AppleLibraryTrips, err = photosArchive.ReadTripsForPhotoFacts(ctx, storedFacts); err != nil {
		return photocard.PhotoCardModelInputSources{}, false, err
	}
	if sources.PictureFetchResult, err = photosArchive.ReadStoredPictureFetchResult(ctx, applePhotoUUID); err != nil {
		return photocard.PhotoCardModelInputSources{}, false, err
	}
	if cameraLocation := storedFacts[0].GetCameraLocation(); cameraLocation != nil {
		cameraSpot := photosarchive.CameraSpotForCameraPosition(cameraLocation.GetCameraLatitude(), cameraLocation.GetCameraLongitude())
		lookupResult, lookupIsStored, err := photosArchive.ReadStoredNearbyPlacesLookupResult(ctx,
			placelookup.PhotonReverseLookupRequestForCameraSpot(cameraSpot.CameraSpotKey, cameraSpot.CameraSpotLatitude, cameraSpot.CameraSpotLongitude))
		if err != nil {
			return photocard.PhotoCardModelInputSources{}, false, err
		}
		if lookupIsStored {
			sources.NearbyPlacesLookupResult = lookupResult
		}
		sources.KnownPlaceAtCamera, sources.KnownPlaceDistanceMetres = photosarchive.KnownPlaceContainingPhotoCamera(storedFacts[0], knownPlacesFile)
	}
	return sources, true, nil
}

type writePhotoCardBinding struct {
	knownPlacesFile   *knownplaces.KnownPlacesFile
	cardModelProvider photo.CardModelProvider
	cardModelName     string
	cardModelEffort   string
}

func (binding writePhotoCardBinding) SelectPendingPhotosStepInputsNewestFirst(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	workSelection PhotosStepWorkSelection,
) ([]*photosstep.WritePhotoCardRequest, error) {
	photosReadyForCard, err := photosArchive.SelectPhotosReadyForCardNewestFirst(ctx, workSelection.SelectionTime,
		workSelection.OnlyApplePhotoUUID, workSelection.RetryPermanentlyFailedWork, workSelection.IncludeStaleCards)
	if err != nil {
		return nil, err
	}
	var writePhotoCardRequests []*photosstep.WritePhotoCardRequest
	for _, readyPhoto := range photosReadyForCard {
		if workSelection.Limit != photosarchive.NoWorkListLimit && len(writePhotoCardRequests) >= workSelection.Limit {
			break
		}
		sources, photoIsStored, err := ReadPhotoCardModelInputSources(ctx, photosArchive, readyPhoto.ApplePhotoUUID, binding.knownPlacesFile)
		if err != nil {
			return nil, err
		}
		if !photoIsStored {
			continue
		}
		modelInput, err := photocard.RenderPhotoCardModelInput(sources)
		if err != nil {
			return nil, err
		}
		if readyPhoto.WrittenCardIsCurrentForPicture &&
			readyPhoto.WrittenCardModelInputSHA256 == modelInput.GetPhotoCardModelInputSha256() &&
			readyPhoto.WrittenCardModelProvider == binding.cardModelProvider &&
			readyPhoto.WrittenCardModelName == binding.cardModelName &&
			readyPhoto.WrittenCardModelEffort == binding.cardModelEffort &&
			equalOptionalInt32(readyPhoto.WrittenCardPictureLongEdgePixelLimitUsed, readyPhoto.PictureLongEdgePixelLimitUsed) {
			continue
		}
		writePhotoCardRequests = append(writePhotoCardRequests, &photosstep.WritePhotoCardRequest{
			PhotoCardModelInput:           modelInput,
			PictureSha256:                 readyPhoto.PictureSHA256,
			PictureLongEdgePixelLimitUsed: readyPhoto.PictureLongEdgePixelLimitUsed,
		})
	}
	return writePhotoCardRequests, nil
}

func equalOptionalInt32(left, right *int32) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (writePhotoCardBinding) StorePhotosStepOutput(
	ctx context.Context,
	photosArchive *photosarchive.PhotosArchive,
	_ *photosstep.WritePhotoCardRequest,
	photoCard *photo.PhotoCard,
) error {
	return photosArchive.StorePhotoCard(ctx, photoCard, time.Now())
}
