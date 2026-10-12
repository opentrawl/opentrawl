package photosarchive

import (
	"context"
	"fmt"
	"time"

	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

type PhotosArchiveStepWorkCounts struct {
	ApplePhotosInLibrary               int
	ApplePhotosAbsentFromLibrary       int
	ApplePhotosWaitingForPictureFetch  int
	CameraSpotLookupsWaiting           int
	ApplePhotosReadyForCardModelInput  int
	ApplePhotosWithoutCardModelInput   int
	PhotoCardModelInputsWaitingForCard int
}

func (photosArchive *PhotosArchive) CountPhotosStepWork(ctx context.Context, now time.Time, requestParameters []*photosstep.PhotosPlaceLookupRequestParameters) (PhotosArchiveStepWorkCounts, error) {
	var workCounts PhotosArchiveStepWorkCounts
	if err := photosArchive.database.QueryRowContext(ctx, `
select
  count(*) filter (where p.absent_from_apple_library_since_time_utc is null),
  count(*) filter (where p.absent_from_apple_library_since_time_utc is not null),
  count(*) filter (where p.absent_from_apple_library_since_time_utc is null and i.apple_photo_uuid is null)
from apple_library_photo p left join photo_card_model_input i on i.apple_photo_uuid = p.apple_photo_uuid`,
	).Scan(&workCounts.ApplePhotosInLibrary, &workCounts.ApplePhotosAbsentFromLibrary, &workCounts.ApplePhotosWithoutCardModelInput); err != nil {
		return workCounts, fmt.Errorf("count Apple photos: %w", err)
	}
	pictureFetchRequests, err := photosArchive.SelectApplePhotosWaitingForPictureFetchNewestFirst(ctx, NoWorkListLimit, now, false, "")
	if err != nil {
		return workCounts, err
	}
	workCounts.ApplePhotosWaitingForPictureFetch = len(pictureFetchRequests)
	placeLookupRequests, err := photosArchive.SelectCameraSpotsWaitingForPlaceLookupNewestFirst(ctx, NoWorkListLimit, now, "", requestParameters, false)
	if err != nil {
		return workCounts, err
	}
	workCounts.CameraSpotLookupsWaiting = len(placeLookupRequests)
	readyApplePhotoUUIDs, err := photosArchive.SelectApplePhotosReadyForCardModelInputNewestFirst(ctx, NoWorkListLimit, "", requestParameters)
	if err != nil {
		return workCounts, err
	}
	workCounts.ApplePhotosReadyForCardModelInput = len(readyApplePhotoUUIDs)
	photoCardRequests, err := photosArchive.SelectPhotoCardModelInputsWaitingForCardNewestFirst(ctx, NoWorkListLimit, now, false, "", false)
	if err != nil {
		return workCounts, err
	}
	workCounts.PhotoCardModelInputsWaitingForCard = len(photoCardRequests)
	return workCounts, nil
}
