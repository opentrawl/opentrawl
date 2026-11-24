package photosarchive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
)

// ReadStoredNearbyPlacesLookupResult returns false when no lookup is stored
// for the exact request.
func (photosArchive *PhotosArchive) ReadStoredNearbyPlacesLookupResult(
	ctx context.Context,
	lookUpPlacesNearSpotRequest *photosstep.LookUpPlacesNearSpotRequest,
) (*photo.NearbyPlacesLookupResult, bool, error) {
	requestSHA256, err := placelookup.PlaceLookupRequestSHA256(lookUpPlacesNearSpotRequest)
	if err != nil {
		return nil, false, err
	}
	lookupResult := &photo.NearbyPlacesLookupResult{
		PlaceLookupRequestSha256: requestSHA256,
		CameraSpotKey:            lookUpPlacesNearSpotRequest.GetCameraSpotKey(),
	}
	var placeProvider, lookupOutcome string
	var failureMessage sql.NullString
	err = photosArchive.database.QueryRowContext(ctx, `
select place_provider, place_lookup_request_text, place_lookup_outcome, place_lookup_failure_message
from place_lookup where place_lookup_request_sha256 = ?`, requestSHA256).Scan(
		&placeProvider, &lookupResult.PlaceLookupRequestText, &lookupOutcome, &failureMessage,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read stored place lookup for the exact request: %w", err)
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
		return nil, false, fmt.Errorf("read stored nearby places for the exact request: %w", err)
	}
	return lookupResult, true, nil
}
