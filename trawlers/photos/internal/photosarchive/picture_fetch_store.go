package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SelectApplePhotosWaitingForPictureFetchNewestFirst returns photos with no
// fetched picture of their current Apple edit, or with a failed fetch that may
// be tried again now. A favourite, an album or a caption does not change the
// pixels, so only Apple's edit state and edit time decide. When
// onlyApplePhotoUUID is set and cachedPictureExists reports its cached JPEG was
// evicted, that photo is selected again so an explicit fetch can restore it.
func (photosArchive *PhotosArchive) SelectApplePhotosWaitingForPictureFetchNewestFirst(
	ctx context.Context,
	limit int,
	now time.Time,
	retryPermanentlyFailedWork bool,
	onlyApplePhotoUUID string,
	pictureLongEdgePixelLimit int32,
	cachedPictureExists func(pictureSHA256 string) bool,
) ([]*photosstep.FetchPictureRequest, error) {
	var fetchPictureRequests []*photosstep.FetchPictureRequest
	err := scanStoredRows(ctx, photosArchive.database, `
select p.apple_photo_uuid, p.capture_time_utc, p.apple_media_kind, p.apple_adjustments_state, p.apple_adjustment_time_utc,
  f.picture_fetch_outcome, f.picture_sha256,
  f.fetched_for_apple_adjustments_state, f.fetched_for_apple_adjustment_time_utc,
  f.picture_long_edge_pixel_limit_used, f.retry_not_before_time_utc
from apple_library_photo p
left join picture_fetch f on f.apple_photo_uuid = p.apple_photo_uuid
where p.absent_from_apple_library_since_time_utc is null
  and (? = '' or p.apple_photo_uuid = ?)
  and (? != ''
       or f.apple_photo_uuid is null
       or f.fetched_for_apple_adjustments_state != p.apple_adjustments_state
       or f.fetched_for_apple_adjustment_time_utc is not p.apple_adjustment_time_utc
       or f.picture_long_edge_pixel_limit_used is not ?
       or (f.picture_fetch_outcome = ? and (f.retry_not_before_time_utc <= ?
           or (? and f.retry_not_before_time_utc is null))))
order by p.capture_time_utc desc
limit ?`,
		[]any{onlyApplePhotoUUID, onlyApplePhotoUUID, onlyApplePhotoUUID, pictureLongEdgePixelLimit, photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED.String(), formatArchiveTime(now), retryPermanentlyFailedWork, limit},
		func(rows *sql.Rows) error {
			fetchPictureRequest := &photosstep.FetchPictureRequest{}
			var captureTimeUTC string
			var adjustmentTimeUTC, fetchOutcome, pictureSHA256 sql.NullString
			var fetchedAdjustmentsState, fetchedPixelLimit sql.NullInt64
			var fetchedAdjustmentTimeUTC, retryNotBeforeTimeUTC sql.NullString
			if err := rows.Scan(&fetchPictureRequest.ApplePhotoUuid, &captureTimeUTC, &fetchPictureRequest.AppleMediaKind,
				&fetchPictureRequest.AppleAdjustmentsState, &adjustmentTimeUTC, &fetchOutcome, &pictureSHA256,
				&fetchedAdjustmentsState, &fetchedAdjustmentTimeUTC, &fetchedPixelLimit, &retryNotBeforeTimeUTC); err != nil {
				return err
			}
			captureTime, err := parseArchiveTime(captureTimeUTC)
			if err != nil {
				return err
			}
			fetchPictureRequest.CaptureTimeUtc = timestamppb.New(captureTime)
			if fetchPictureRequest.AppleAdjustmentTimeUtc, err = optionalArchiveTimeFromColumn(adjustmentTimeUTC); err != nil {
				return err
			}
			if onlyApplePhotoUUID != "" && fetchOutcome.Valid {
				fetchedAdjustmentTime, err := optionalArchiveTimeFromColumn(fetchedAdjustmentTimeUTC)
				if err != nil {
					return err
				}
				sameEditAndResolution := fetchedAdjustmentsState.Valid &&
					int32(fetchedAdjustmentsState.Int64) == fetchPictureRequest.GetAppleAdjustmentsState() &&
					fetchedAdjustmentTime.AsTime().Equal(fetchPictureRequest.GetAppleAdjustmentTimeUtc().AsTime()) &&
					fetchedPixelLimit.Valid && int32(fetchedPixelLimit.Int64) == pictureLongEdgePixelLimit
				if fetchOutcome.String == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED.String() &&
					sameEditAndResolution && (cachedPictureExists == nil || cachedPictureExists(pictureSHA256.String)) {
					return nil
				}
				if fetchOutcome.String == photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED.String() && sameEditAndResolution {
					retryNotBefore, err := optionalArchiveTimeFromColumn(retryNotBeforeTimeUTC)
					if err != nil {
						return err
					}
					retryDue := retryNotBefore != nil && !retryNotBefore.AsTime().After(now)
					retryPermanent := retryPermanentlyFailedWork && retryNotBefore == nil
					if !retryDue && !retryPermanent {
						return nil
					}
				}
			}
			fetchPictureRequests = append(fetchPictureRequests, fetchPictureRequest)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("select photos waiting for a picture fetch: %w", err)
	}
	return fetchPictureRequests, nil
}

func (photosArchive *PhotosArchive) StorePictureFetchResult(ctx context.Context, pictureFetchResult *photo.PictureFetchResult, fetchedTime time.Time) error {
	bearingReference := any(nil)
	if pictureFetchResult.GetCameraBearingReference() != photo.CameraBearingReference_CAMERA_BEARING_REFERENCE_UNSPECIFIED {
		bearingReference = pictureFetchResult.GetCameraBearingReference().String()
	}
	pictureSource := any(nil)
	if pictureFetchResult.GetPictureSource() != photo.PictureSource_PICTURE_SOURCE_UNSPECIFIED {
		pictureSource = pictureFetchResult.GetPictureSource().String()
	}
	pictureByteCount := any(nil)
	if pictureFetchResult.PictureByteCount != nil {
		pictureByteCount = pictureFetchResult.GetPictureByteCount()
	}
	pictureLongEdgePixelLimitUsed := any(nil)
	if pictureFetchResult.PictureLongEdgePixelLimitUsed != nil {
		pictureLongEdgePixelLimitUsed = pictureFetchResult.GetPictureLongEdgePixelLimitUsed()
	}
	_, err := photosArchive.database.ExecContext(ctx, `
insert or replace into picture_fetch (
  apple_photo_uuid, fetched_for_apple_adjustments_state, fetched_for_apple_adjustment_time_utc,
  picture_fetch_outcome, picture_fetch_failure_message, retry_not_before_time_utc,
  picture_source, picture_sha256, picture_byte_count, picture_long_edge_pixel_limit_used,
  camera_bearing_degrees, camera_bearing_reference, fetched_time_utc
) values (?,?,?, ?,?,?, ?,?,?,?, ?,?,?)`,
		pictureFetchResult.GetApplePhotoUuid(),
		pictureFetchResult.GetFetchedForAppleAdjustmentsState(),
		optionalArchiveTimeColumn(pictureFetchResult.GetFetchedForAppleAdjustmentTimeUtc()),
		pictureFetchResult.GetPictureFetchOutcome().String(),
		emptyTextAsNull(pictureFetchResult.GetPictureFetchFailureMessage()),
		optionalArchiveTimeColumn(pictureFetchResult.GetRetryNotBeforeTimeUtc()),
		pictureSource,
		emptyTextAsNull(pictureFetchResult.GetPictureSha256()),
		pictureByteCount,
		pictureLongEdgePixelLimitUsed,
		optionalFloat64Column(pictureFetchResult.CameraBearingDegrees),
		bearingReference,
		formatArchiveTime(fetchedTime),
	)
	if err != nil {
		return fmt.Errorf("store picture fetch: %w", err)
	}
	return nil
}

// ReadStoredPictureFetchResult returns nil when the picture was never fetched.
func (photosArchive *PhotosArchive) ReadStoredPictureFetchResult(ctx context.Context, applePhotoUUID string) (*photo.PictureFetchResult, error) {
	var pictureFetchResult *photo.PictureFetchResult
	err := scanStoredRows(ctx, photosArchive.database, `
select fetched_for_apple_adjustments_state, fetched_for_apple_adjustment_time_utc,
  picture_fetch_outcome, picture_fetch_failure_message, retry_not_before_time_utc,
  picture_source, picture_sha256, picture_byte_count, picture_long_edge_pixel_limit_used,
  camera_bearing_degrees, camera_bearing_reference
from picture_fetch where apple_photo_uuid = ?`, []any{applePhotoUUID},
		func(rows *sql.Rows) error {
			storedResult := &photo.PictureFetchResult{ApplePhotoUuid: applePhotoUUID}
			var fetchOutcome string
			var adjustmentTimeUTC, failureMessage, retryNotBeforeTimeUTC, pictureSource, pictureSHA256, bearingReference sql.NullString
			var pictureByteCount, pictureLongEdgePixelLimitUsed sql.NullInt64
			var bearingDegrees sql.NullFloat64
			if err := rows.Scan(&storedResult.FetchedForAppleAdjustmentsState, &adjustmentTimeUTC,
				&fetchOutcome, &failureMessage, &retryNotBeforeTimeUTC,
				&pictureSource, &pictureSHA256, &pictureByteCount, &pictureLongEdgePixelLimitUsed,
				&bearingDegrees, &bearingReference); err != nil {
				return err
			}
			var err error
			if storedResult.FetchedForAppleAdjustmentTimeUtc, err = optionalArchiveTimeFromColumn(adjustmentTimeUTC); err != nil {
				return err
			}
			if storedResult.RetryNotBeforeTimeUtc, err = optionalArchiveTimeFromColumn(retryNotBeforeTimeUTC); err != nil {
				return err
			}
			storedResult.PictureFetchOutcome = photo.PictureFetchOutcome(photo.PictureFetchOutcome_value[fetchOutcome])
			storedResult.PictureFetchFailureMessage = failureMessage.String
			storedResult.PictureSource = photo.PictureSource(photo.PictureSource_value[pictureSource.String])
			storedResult.PictureSha256 = pictureSHA256.String
			if pictureByteCount.Valid {
				storedResult.PictureByteCount = &pictureByteCount.Int64
			}
			if pictureLongEdgePixelLimitUsed.Valid {
				limitUsed := int32(pictureLongEdgePixelLimitUsed.Int64)
				storedResult.PictureLongEdgePixelLimitUsed = &limitUsed
			}
			storedResult.CameraBearingDegrees = optionalFloat64FromColumn(bearingDegrees)
			storedResult.CameraBearingReference = photo.CameraBearingReference(photo.CameraBearingReference_value[bearingReference.String])
			pictureFetchResult = storedResult
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read stored picture fetch: %w", err)
	}
	return pictureFetchResult, nil
}
