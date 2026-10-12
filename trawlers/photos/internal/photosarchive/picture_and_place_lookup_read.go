package photosarchive

import (
	"context"
	"database/sql"
	"fmt"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (photosArchive *PhotosArchive) ReadStoredPictureFetchResult(ctx context.Context, applePhotoUUID string) (*photo.PictureFetchResult, error) {
	var pictureFetchResult *photo.PictureFetchResult
	err := scanStoredRows(ctx, photosArchive.database, `
select fetched_for_apple_modification_time_utc, picture_fetch_outcome, picture_fetch_failure_message,
  retry_not_before_time_utc, picture_sha256, picture_byte_count, camera_bearing_degrees, camera_bearing_reference
from picture_fetch where apple_photo_uuid = ?`, []any{applePhotoUUID},
		func(rows *sql.Rows) error {
			var fetchedForModificationTimeUTC, fetchOutcome string
			var failureMessage, retryNotBeforeTimeUTC, pictureSHA256, bearingReference sql.NullString
			var pictureByteCount sql.NullInt64
			var bearingDegrees sql.NullFloat64
			if err := rows.Scan(&fetchedForModificationTimeUTC, &fetchOutcome, &failureMessage, &retryNotBeforeTimeUTC,
				&pictureSHA256, &pictureByteCount, &bearingDegrees, &bearingReference); err != nil {
				return err
			}
			fetchedForModificationTime, err := parseArchiveTime(fetchedForModificationTimeUTC)
			if err != nil {
				return err
			}
			pictureFetchResult = &photo.PictureFetchResult{
				ApplePhotoUuid:                     applePhotoUUID,
				FetchedForAppleModificationTimeUtc: timestamppb.New(fetchedForModificationTime),
				PictureFetchOutcome:                photo.PictureFetchOutcome(photo.PictureFetchOutcome_value[fetchOutcome]),
				PictureFetchFailureMessage:         failureMessage.String,
				PictureSha256:                      pictureSHA256.String,
				CameraBearingDegrees:               optionalFloat64FromColumn(bearingDegrees),
				CameraBearingReference:             photo.CameraBearingReference(photo.CameraBearingReference_value[bearingReference.String]),
			}
			if pictureByteCount.Valid {
				pictureFetchResult.PictureByteCount = &pictureByteCount.Int64
			}
			if retryNotBeforeTimeUTC.Valid {
				retryNotBeforeTime, err := parseArchiveTime(retryNotBeforeTimeUTC.String)
				if err != nil {
					return err
				}
				pictureFetchResult.RetryNotBeforeTimeUtc = timestamppb.New(retryNotBeforeTime)
			}
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read stored picture fetch: %w", err)
	}
	return pictureFetchResult, nil
}

func (photosArchive *PhotosArchive) ReadStoredNearbyPlacesLookupResults(
	ctx context.Context,
	lookupRequests []*photosstep.LookUpPlacesNearSpotRequest,
) ([]*photo.NearbyPlacesLookupResult, error) {
	lookupResults := make([]*photo.NearbyPlacesLookupResult, 0, len(lookupRequests))
	for _, lookupRequest := range lookupRequests {
		requestSHA256, err := PlaceLookupRequestSHA256(lookupRequest)
		if err != nil {
			return nil, err
		}
		lookupResult := &photo.NearbyPlacesLookupResult{
			PlaceLookupRequestSha256: requestSHA256,
			CameraSpotKey:            lookupRequest.GetCameraSpotKey(),
		}
		var placeProvider, lookupOutcome string
		var failureMessage sql.NullString
		if err := photosArchive.database.QueryRowContext(ctx, `
select place_provider, place_lookup_request_text, place_lookup_outcome, place_lookup_failure_message
from place_lookup where place_lookup_request_sha256 = ?`, requestSHA256).Scan(
			&placeProvider, &lookupResult.PlaceLookupRequestText, &lookupOutcome, &failureMessage,
		); err != nil {
			return nil, fmt.Errorf("read stored place lookup for the exact request: %w", err)
		}
		lookupResult.PlaceProvider = photo.PlaceProvider(photo.PlaceProvider_value[placeProvider])
		lookupResult.PlaceLookupOutcome = photo.PlaceLookupOutcome(photo.PlaceLookupOutcome_value[lookupOutcome])
		lookupResult.PlaceLookupFailureMessage = failureMessage.String
		if err := scanStoredRows(ctx, photosArchive.database, `
select place_name, place_kind, place_latitude, place_longitude
from nearby_place where place_lookup_request_sha256 = ? order by place_rank`, []any{requestSHA256},
			func(rows *sql.Rows) error {
				nearbyPlace := &photo.NearbyPlace{}
				if err := rows.Scan(&nearbyPlace.PlaceName, &nearbyPlace.PlaceKind,
					&nearbyPlace.PlaceLatitude, &nearbyPlace.PlaceLongitude); err != nil {
					return err
				}
				lookupResult.NearbyPlacesNearestFirst = append(lookupResult.NearbyPlacesNearestFirst, nearbyPlace)
				return nil
			}); err != nil {
			return nil, fmt.Errorf("read stored nearby places for the exact request: %w", err)
		}
		lookupResults = append(lookupResults, lookupResult)
	}
	return lookupResults, nil
}
