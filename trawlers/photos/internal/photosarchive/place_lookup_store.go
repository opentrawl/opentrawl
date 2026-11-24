package photosarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// StoreNearbyPlacesLookupResult replaces the stored lookup for its exact
// request, and that lookup's nearby places, in one transaction.
func (photosArchive *PhotosArchive) StoreNearbyPlacesLookupResult(
	ctx context.Context,
	nearbyPlacesLookupResult *photo.NearbyPlacesLookupResult,
	lookedUpTime time.Time,
) error {
	requestSHA256 := nearbyPlacesLookupResult.GetPlaceLookupRequestSha256()
	providerResponseBodyGzip, err := gzipProviderResponseBody(nearbyPlacesLookupResult.GetProviderResponseBody())
	if err != nil {
		return err
	}
	transaction, err := photosArchive.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin storing place lookup: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, `
insert or replace into place_lookup (
  place_lookup_request_sha256, place_provider, camera_spot_key, place_lookup_request_text,
  place_lookup_outcome, place_lookup_failure_message, retry_not_before_time_utc,
  provider_response_body_gzip, looked_up_time_utc
) values (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		requestSHA256,
		nearbyPlacesLookupResult.GetPlaceProvider().String(),
		nearbyPlacesLookupResult.GetCameraSpotKey(),
		nearbyPlacesLookupResult.GetPlaceLookupRequestText(),
		nearbyPlacesLookupResult.GetPlaceLookupOutcome().String(),
		emptyTextAsNull(nearbyPlacesLookupResult.GetPlaceLookupFailureMessage()),
		nullWhenMessageAbsent(nearbyPlacesLookupResult.GetRetryNotBeforeTimeUtc() != nil,
			formatArchiveTime(nearbyPlacesLookupResult.GetRetryNotBeforeTimeUtc().AsTime())),
		providerResponseBodyGzip,
		formatArchiveTime(lookedUpTime),
	); err != nil {
		return fmt.Errorf("store place lookup: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, `delete from nearby_place where place_lookup_request_sha256 = ?`, requestSHA256); err != nil {
		return fmt.Errorf("replace nearby places: %w", err)
	}
	for placeRank, nearbyPlace := range nearbyPlacesLookupResult.GetNearbyPlacesNearestFirst() {
		if _, err := transaction.ExecContext(ctx, `
insert into nearby_place (place_lookup_request_sha256, place_rank, place_name, place_kind, place_latitude, place_longitude)
values (?, ?, ?, ?, ?, ?)`,
			requestSHA256, placeRank, nearbyPlace.GetPlaceName(), nearbyPlace.GetPlaceKind(),
			nearbyPlace.GetPlaceLatitude(), nearbyPlace.GetPlaceLongitude(),
		); err != nil {
			return fmt.Errorf("store nearby place: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit place lookup: %w", err)
	}
	return nil
}

// A Photon answer is about 18 KB of JSON and about 2 KB compressed; one
// library has thousands of spots.
func gzipProviderResponseBody(providerResponseBody []byte) ([]byte, error) {
	var compressedBody bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBody)
	if _, err := gzipWriter.Write(providerResponseBody); err != nil {
		return nil, fmt.Errorf("compress provider response body: %w", err)
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, fmt.Errorf("compress provider response body: %w", err)
	}
	return compressedBody.Bytes(), nil
}
