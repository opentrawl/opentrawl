package photosarchive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NoWorkListLimit selects every pending item.
const NoWorkListLimit = -1

// Most work lists select missing outputs or failures ready for retry. Card
// model inputs revisit every ready photo, so changed facts can refresh them.
// Lists are newest photo first.

func (photosArchive *PhotosArchive) SelectApplePhotosWaitingForPictureFetchNewestFirst(
	ctx context.Context,
	limit int,
	now time.Time,
	retryPermanentlyFailedWork bool,
	onlyApplePhotoUUID string,
) ([]*photosstep.FetchPictureRequest, error) {
	var fetchPictureRequests []*photosstep.FetchPictureRequest
	err := scanStoredRows(ctx, photosArchive.database, `
select p.apple_photo_uuid, p.apple_modification_time_utc
from apple_library_photo p
left join picture_fetch f on f.apple_photo_uuid = p.apple_photo_uuid
where p.absent_from_apple_library_since_time_utc is null
  and (? = '' or p.apple_photo_uuid = ?)
  and (f.apple_photo_uuid is null
       or f.fetched_for_apple_modification_time_utc is not p.apple_modification_time_utc
       or (f.picture_fetch_outcome = ? and (f.retry_not_before_time_utc <= ?
           or (? and f.retry_not_before_time_utc is null))))
order by p.capture_time_utc desc
limit ?`,
		[]any{onlyApplePhotoUUID, onlyApplePhotoUUID, photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED.String(), formatArchiveTime(now), retryPermanentlyFailedWork, limit},
		func(rows *sql.Rows) error {
			fetchPictureRequest := &photosstep.FetchPictureRequest{}
			var modificationTimeUTC sql.NullString
			if err := rows.Scan(&fetchPictureRequest.ApplePhotoUuid, &modificationTimeUTC); err != nil {
				return err
			}
			if modificationTimeUTC.Valid {
				modificationTime, err := parseArchiveTime(modificationTimeUTC.String)
				if err != nil {
					return err
				}
				fetchPictureRequest.AppleModificationTimeUtc = timestamppb.New(modificationTime)
			}
			fetchPictureRequests = append(fetchPictureRequests, fetchPictureRequest)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("select photos waiting for a picture fetch: %w", err)
	}
	return fetchPictureRequests, nil
}

// CameraSpotKey groups camera positions to about 10 metres: latitude and
// longitude rounded to four decimal places.
func CameraSpotKey(cameraLatitude, cameraLongitude float64) string {
	return strconv.FormatFloat(roundToFourDecimalPlaces(cameraLatitude), 'f', 4, 64) + "," +
		strconv.FormatFloat(roundToFourDecimalPlaces(cameraLongitude), 'f', 4, 64)
}

func roundToFourDecimalPlaces(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func PlaceLookupRequestSHA256(lookUpPlacesNearSpotRequest *photosstep.LookUpPlacesNearSpotRequest) (string, error) {
	encodedRequest, err := proto.MarshalOptions{Deterministic: true}.Marshal(lookUpPlacesNearSpotRequest)
	if err != nil {
		return "", fmt.Errorf("encode place lookup request: %w", err)
	}
	requestSHA256 := sha256.Sum256(encodedRequest)
	return hex.EncodeToString(requestSHA256[:]), nil
}

type cameraSpot struct {
	latitude  float64
	longitude float64
}

// selectCameraSpotsNewestFirst returns each spot once, ordered by its newest
// photo.
func (photosArchive *PhotosArchive) selectCameraSpotsNewestFirst(ctx context.Context, onlyApplePhotoUUID string) ([]cameraSpot, error) {
	var cameraSpots []cameraSpot
	seenCameraSpotKeys := map[string]bool{}
	err := scanStoredRows(ctx, photosArchive.database, `
select camera_latitude, camera_longitude from apple_library_photo
where absent_from_apple_library_since_time_utc is null and camera_latitude is not null
  and (? = '' or apple_photo_uuid = ?)
order by capture_time_utc desc`, []any{onlyApplePhotoUUID, onlyApplePhotoUUID},
		func(rows *sql.Rows) error {
			var cameraLatitude, cameraLongitude float64
			if err := rows.Scan(&cameraLatitude, &cameraLongitude); err != nil {
				return err
			}
			cameraSpotKey := CameraSpotKey(cameraLatitude, cameraLongitude)
			if seenCameraSpotKeys[cameraSpotKey] {
				return nil
			}
			seenCameraSpotKeys[cameraSpotKey] = true
			cameraSpots = append(cameraSpots, cameraSpot{
				latitude:  roundToFourDecimalPlaces(cameraLatitude),
				longitude: roundToFourDecimalPlaces(cameraLongitude),
			})
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("select camera spots: %w", err)
	}
	return cameraSpots, nil
}

type storedPlaceLookupState struct {
	placeLookupOutcome    string
	retryNotBeforeTimeUTC sql.NullString
}

func (photosArchive *PhotosArchive) readStoredPlaceLookupStates(ctx context.Context) (map[string]storedPlaceLookupState, error) {
	storedLookupStates := map[string]storedPlaceLookupState{}
	err := scanStoredRows(ctx, photosArchive.database, `
select place_lookup_request_sha256, place_lookup_outcome, retry_not_before_time_utc from place_lookup`, nil,
		func(rows *sql.Rows) error {
			var requestSHA256 string
			var lookupState storedPlaceLookupState
			if err := rows.Scan(&requestSHA256, &lookupState.placeLookupOutcome, &lookupState.retryNotBeforeTimeUTC); err != nil {
				return err
			}
			storedLookupStates[requestSHA256] = lookupState
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("read stored place lookups: %w", err)
	}
	return storedLookupStates, nil
}

func placeLookupIsWaiting(lookupState storedPlaceLookupState, lookupWasStored bool, now time.Time, retryPermanentlyFailedWork bool) bool {
	if !lookupWasStored {
		return true
	}
	return lookupState.placeLookupOutcome == photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED.String() &&
		((lookupState.retryNotBeforeTimeUTC.Valid && lookupState.retryNotBeforeTimeUTC.String <= formatArchiveTime(now)) ||
			(retryPermanentlyFailedWork && !lookupState.retryNotBeforeTimeUTC.Valid))
}

func PlaceLookupRequestsForCameraPosition(
	cameraLatitude, cameraLongitude float64,
	requestParameters []*photosstep.PhotosPlaceLookupRequestParameters,
) []*photosstep.LookUpPlacesNearSpotRequest {
	lookupRequests := make([]*photosstep.LookUpPlacesNearSpotRequest, 0, len(requestParameters))
	for _, parameters := range requestParameters {
		lookupRequests = append(lookupRequests, &photosstep.LookUpPlacesNearSpotRequest{
			PlaceProvider:           parameters.GetPlaceProvider(),
			CameraSpotKey:           CameraSpotKey(cameraLatitude, cameraLongitude),
			CameraSpotLatitude:      roundToFourDecimalPlaces(cameraLatitude),
			CameraSpotLongitude:     roundToFourDecimalPlaces(cameraLongitude),
			PlaceSearchRadiusMetres: parameters.GetPlaceSearchRadiusMetres(),
			PlaceSearchQuery:        parameters.GetPlaceSearchQuery(),
		})
	}
	return lookupRequests
}

// SelectCameraSpotsWaitingForPlaceLookupNewestFirst returns one request per
// spot and provider. onlyApplePhotoUUID, when set, limits it to that photo's
// spot.
func (photosArchive *PhotosArchive) SelectCameraSpotsWaitingForPlaceLookupNewestFirst(
	ctx context.Context,
	limit int,
	now time.Time,
	onlyApplePhotoUUID string,
	requestParameters []*photosstep.PhotosPlaceLookupRequestParameters,
	retryPermanentlyFailedWork bool,
) ([]*photosstep.LookUpPlacesNearSpotRequest, error) {
	cameraSpots, err := photosArchive.selectCameraSpotsNewestFirst(ctx, onlyApplePhotoUUID)
	if err != nil {
		return nil, err
	}
	storedLookupStates, err := photosArchive.readStoredPlaceLookupStates(ctx)
	if err != nil {
		return nil, err
	}
	var waitingLookupRequests []*photosstep.LookUpPlacesNearSpotRequest
	for _, spot := range cameraSpots {
		for _, lookupRequest := range PlaceLookupRequestsForCameraPosition(spot.latitude, spot.longitude, requestParameters) {
			if limit != NoWorkListLimit && len(waitingLookupRequests) >= limit {
				return waitingLookupRequests, nil
			}
			requestSHA256, err := PlaceLookupRequestSHA256(lookupRequest)
			if err != nil {
				return nil, err
			}
			lookupState, lookupWasStored := storedLookupStates[requestSHA256]
			if placeLookupIsWaiting(lookupState, lookupWasStored, now, retryPermanentlyFailedWork) {
				waitingLookupRequests = append(waitingLookupRequests, lookupRequest)
			}
		}
	}
	return waitingLookupRequests, nil
}

// SelectApplePhotosReadyForCardModelInputNewestFirst returns photos that have
// a fetched picture of their current version and a completed lookup for each
// exact configured request at their camera position. Existing card inputs are
// included so changed facts and request parameters can refresh them.
func (photosArchive *PhotosArchive) SelectApplePhotosReadyForCardModelInputNewestFirst(
	ctx context.Context,
	limit int,
	onlyApplePhotoUUID string,
	requestParameters []*photosstep.PhotosPlaceLookupRequestParameters,
) ([]string, error) {
	completedLookupRequests := map[string]bool{}
	if err := scanStoredRows(ctx, photosArchive.database, `
select place_lookup_request_sha256 from place_lookup where place_lookup_outcome != ? or retry_not_before_time_utc is null`,
		[]any{photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED.String()},
		func(rows *sql.Rows) error {
			var requestSHA256 string
			if err := rows.Scan(&requestSHA256); err != nil {
				return err
			}
			completedLookupRequests[requestSHA256] = true
			return nil
		}); err != nil {
		return nil, fmt.Errorf("read finished place lookups: %w", err)
	}
	var readyApplePhotoUUIDs []string
	err := scanStoredRows(ctx, photosArchive.database, `
select p.apple_photo_uuid, p.camera_latitude, p.camera_longitude
from apple_library_photo p
join picture_fetch f on f.apple_photo_uuid = p.apple_photo_uuid
  and f.picture_fetch_outcome = ?
  and f.fetched_for_apple_modification_time_utc is p.apple_modification_time_utc
where p.absent_from_apple_library_since_time_utc is null
  and (? = '' or p.apple_photo_uuid = ?)
order by p.capture_time_utc desc`,
		[]any{photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED.String(), onlyApplePhotoUUID, onlyApplePhotoUUID},
		func(rows *sql.Rows) error {
			if limit != NoWorkListLimit && len(readyApplePhotoUUIDs) >= limit {
				return nil
			}
			var applePhotoUUID string
			var cameraLatitude, cameraLongitude sql.NullFloat64
			if err := rows.Scan(&applePhotoUUID, &cameraLatitude, &cameraLongitude); err != nil {
				return err
			}
			if cameraLatitude.Valid {
				if len(requestParameters) == 0 {
					return nil
				}
				for _, lookupRequest := range PlaceLookupRequestsForCameraPosition(cameraLatitude.Float64, cameraLongitude.Float64, requestParameters) {
					requestSHA256, err := PlaceLookupRequestSHA256(lookupRequest)
					if err != nil {
						return err
					}
					if !completedLookupRequests[requestSHA256] {
						return nil
					}
				}
			}
			readyApplePhotoUUIDs = append(readyApplePhotoUUIDs, applePhotoUUID)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("select photos ready for a card model input: %w", err)
	}
	return readyApplePhotoUUIDs, nil
}

// SelectPhotoCardModelInputsWaitingForCardNewestFirst returns card model
// inputs with no card, with a failed card that may be retried now, or, when
// remaking stale cards, with a card made from a different card model input.
func (photosArchive *PhotosArchive) SelectPhotoCardModelInputsWaitingForCardNewestFirst(
	ctx context.Context,
	limit int,
	now time.Time,
	includeStaleCards bool,
	onlyApplePhotoUUID string,
	retryPermanentlyFailedWork bool,
) ([]*photosstep.WritePhotoCardRequest, error) {
	var writePhotoCardRequests []*photosstep.WritePhotoCardRequest
	err := scanStoredRows(ctx, photosArchive.database, `
select i.apple_photo_uuid, i.photo_card_model_input_text, i.photo_card_model_input_sha256, f.picture_sha256
from photo_card_model_input i
join apple_library_photo p on p.apple_photo_uuid = i.apple_photo_uuid
join picture_fetch f on f.apple_photo_uuid = i.apple_photo_uuid and f.picture_fetch_outcome = ?
left join photo_card c on c.apple_photo_uuid = i.apple_photo_uuid
where p.absent_from_apple_library_since_time_utc is null
  and (? = '' or i.apple_photo_uuid = ?)
  and (c.apple_photo_uuid is null
       or (c.photo_card_outcome = ? and c.retry_not_before_time_utc <= ?)
       or (? and c.photo_card_outcome in (?, ?) and c.retry_not_before_time_utc is null)
       or (? and c.photo_card_model_input_sha256_used != i.photo_card_model_input_sha256))
order by p.capture_time_utc desc
limit ?`,
		[]any{
			photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED.String(),
			onlyApplePhotoUUID, onlyApplePhotoUUID,
			photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_FAILED.String(), formatArchiveTime(now),
			retryPermanentlyFailedWork, photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_FAILED.String(),
			photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_ANSWER_INVALID.String(),
			includeStaleCards, limit,
		},
		func(rows *sql.Rows) error {
			photoCardModelInput := &photo.PhotoCardModelInput{}
			var pictureSHA256 sql.NullString
			if err := rows.Scan(&photoCardModelInput.ApplePhotoUuid, &photoCardModelInput.PhotoCardModelInputText,
				&photoCardModelInput.PhotoCardModelInputSha256, &pictureSHA256); err != nil {
				return err
			}
			writePhotoCardRequests = append(writePhotoCardRequests, &photosstep.WritePhotoCardRequest{
				PhotoCardModelInput: photoCardModelInput,
				PictureSha256:       pictureSHA256.String,
			})
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("select card model inputs waiting for a card: %w", err)
	}
	return writePhotoCardRequests, nil
}

// ClearPermanentlyFailedPhotosStepOutput deletes only the selected request's
// permanent failure, immediately before that request is run again.
func (photosArchive *PhotosArchive) ClearPermanentlyFailedPhotosStepOutput(ctx context.Context, stepInput proto.Message) error {
	var deleteStatement string
	var deleteArguments []any
	switch selectedRequest := stepInput.(type) {
	case *photosstep.FetchPictureRequest:
		deleteStatement = `delete from picture_fetch where apple_photo_uuid = ? and picture_fetch_outcome = ? and retry_not_before_time_utc is null`
		deleteArguments = []any{selectedRequest.GetApplePhotoUuid(), photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FAILED.String()}
	case *photosstep.LookUpPlacesNearSpotRequest:
		requestSHA256, err := PlaceLookupRequestSHA256(selectedRequest)
		if err != nil {
			return err
		}
		deleteStatement = `delete from place_lookup where place_lookup_request_sha256 = ? and place_lookup_outcome = ? and retry_not_before_time_utc is null`
		deleteArguments = []any{requestSHA256, photo.PlaceLookupOutcome_PLACE_LOOKUP_OUTCOME_FAILED.String()}
	case *photosstep.WritePhotoCardRequest:
		deleteStatement = `delete from photo_card where apple_photo_uuid = ? and photo_card_outcome in (?, ?) and retry_not_before_time_utc is null`
		deleteArguments = []any{
			selectedRequest.GetPhotoCardModelInput().GetApplePhotoUuid(),
			photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_FAILED.String(),
			photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_ANSWER_INVALID.String(),
		}
	default:
		return nil
	}
	_, err := photosArchive.database.ExecContext(ctx, deleteStatement, deleteArguments...)
	if err != nil {
		return fmt.Errorf("clear selected Photos step failure: %w", err)
	}
	return nil
}
