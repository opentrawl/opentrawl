package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/proto"
)

// NoWorkListLimit selects every pending item.
const NoWorkListLimit = -1

// Work lists select missing outputs or failures ready for retry, newest photo
// first.

// CameraSpot is one cell of a grid of 0.0005 degrees, about 55 metres north to
// south: every photo taken in the cell shares one place lookup. Distances and
// directions to nearby places are measured later from each photo's own camera
// position, not from the spot.
type CameraSpot struct {
	CameraSpotKey       string
	CameraSpotLatitude  float64
	CameraSpotLongitude float64
}

const cameraSpotGridCellsPerDegree = 2000

func CameraSpotForCameraPosition(cameraLatitude, cameraLongitude float64) CameraSpot {
	cameraSpotLatitude := math.Round(cameraLatitude*cameraSpotGridCellsPerDegree)/cameraSpotGridCellsPerDegree + 0
	cameraSpotLongitude := math.Round(cameraLongitude*cameraSpotGridCellsPerDegree)/cameraSpotGridCellsPerDegree + 0
	return CameraSpot{
		CameraSpotKey: strconv.FormatFloat(cameraSpotLatitude, 'f', 4, 64) + "," +
			strconv.FormatFloat(cameraSpotLongitude, 'f', 4, 64),
		CameraSpotLatitude:  cameraSpotLatitude,
		CameraSpotLongitude: cameraSpotLongitude,
	}
}

// selectCameraSpotsNewestFirst returns each spot once, ordered by its newest
// photo.
func (photosArchive *PhotosArchive) selectCameraSpotsNewestFirst(ctx context.Context, onlyApplePhotoUUID string) ([]CameraSpot, error) {
	var cameraSpots []CameraSpot
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
			cameraSpot := CameraSpotForCameraPosition(cameraLatitude, cameraLongitude)
			if seenCameraSpotKeys[cameraSpot.CameraSpotKey] {
				return nil
			}
			seenCameraSpotKeys[cameraSpot.CameraSpotKey] = true
			cameraSpots = append(cameraSpots, cameraSpot)
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

// SelectCameraSpotsWaitingForPlaceLookupNewestFirst returns the request for
// each spot whose exact request has no stored lookup, or a failed one that may
// be retried. onlyApplePhotoUUID, when set, limits it to that photo's spot.
func (photosArchive *PhotosArchive) SelectCameraSpotsWaitingForPlaceLookupNewestFirst(
	ctx context.Context,
	limit int,
	now time.Time,
	onlyApplePhotoUUID string,
	retryPermanentlyFailedWork bool,
	placeLookupRequestForCameraSpot func(CameraSpot) *photosstep.LookUpPlacesNearSpotRequest,
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
	for _, cameraSpot := range cameraSpots {
		if limit != NoWorkListLimit && len(waitingLookupRequests) >= limit {
			break
		}
		lookupRequest := placeLookupRequestForCameraSpot(cameraSpot)
		requestSHA256, err := placelookup.PlaceLookupRequestSHA256(lookupRequest)
		if err != nil {
			return nil, err
		}
		lookupState, lookupWasStored := storedLookupStates[requestSHA256]
		if placeLookupIsWaiting(lookupState, lookupWasStored, now, retryPermanentlyFailedWork) {
			waitingLookupRequests = append(waitingLookupRequests, lookupRequest)
		}
	}
	return waitingLookupRequests, nil
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
		requestSHA256, err := placelookup.PlaceLookupRequestSHA256(selectedRequest)
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
