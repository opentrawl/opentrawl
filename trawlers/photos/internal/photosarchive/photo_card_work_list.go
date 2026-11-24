package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/placelookup"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
)

// PhotoReadyForCard is a present photo whose picture of its current Apple
// edit is fetched and whose camera spot has a place lookup of any outcome, so
// a card can be written from complete inputs.
type PhotoReadyForCard struct {
	ApplePhotoUUID                string
	PictureSHA256                 string
	PictureLongEdgePixelLimitUsed *int32
	// Set when the photo already has a written card for the same picture: it
	// is remade only when --recard finds its card model input, model, effort
	// or picture resolution changed.
	WrittenCardIsCurrentForPicture           bool
	WrittenCardModelInputSHA256              string
	WrittenCardModelProvider                 photo.CardModelProvider
	WrittenCardModelName                     string
	WrittenCardModelEffort                   string
	WrittenCardPictureLongEdgePixelLimitUsed *int32
}

// SelectPhotosReadyForCardNewestFirst returns, newest first, photos with no
// card, with a failed card that may be tried again now (or with no retry time
// when retryPermanentlyFailedWork is set), with a card written from other
// pixels, and, when includeWrittenCards is set, photos whose written card may
// be stale. A photo absent from Apple's library never gets a card; its
// existing card is kept.
func (photosArchive *PhotosArchive) SelectPhotosReadyForCardNewestFirst(
	ctx context.Context,
	now time.Time,
	onlyApplePhotoUUID string,
	retryPermanentlyFailedWork bool,
	includeWrittenCards bool,
) ([]PhotoReadyForCard, error) {
	storedLookupStates, err := photosArchive.readStoredPlaceLookupStates(ctx)
	if err != nil {
		return nil, err
	}
	var photosReadyForCard []PhotoReadyForCard
	err = scanStoredRows(ctx, photosArchive.database, `
select p.apple_photo_uuid, p.camera_latitude, p.camera_longitude, f.picture_sha256, f.picture_long_edge_pixel_limit_used,
  c.photo_card_outcome, c.retry_not_before_time_utc, c.picture_sha256_used, c.picture_long_edge_pixel_limit_used,
  c.photo_card_model_input_sha256_used, c.card_model_provider, c.card_model_name, c.card_model_effort
from apple_library_photo p
join picture_fetch f on f.apple_photo_uuid = p.apple_photo_uuid
  and f.picture_fetch_outcome = ?
  and f.fetched_for_apple_adjustments_state = p.apple_adjustments_state
  and f.fetched_for_apple_adjustment_time_utc is p.apple_adjustment_time_utc
left join photo_card c on c.apple_photo_uuid = p.apple_photo_uuid
where p.absent_from_apple_library_since_time_utc is null
  and (? = '' or p.apple_photo_uuid = ?)
order by p.capture_time_utc desc`,
		[]any{photo.PictureFetchOutcome_PICTURE_FETCH_OUTCOME_FETCHED.String(), onlyApplePhotoUUID, onlyApplePhotoUUID},
		func(rows *sql.Rows) error {
			var readyPhoto PhotoReadyForCard
			var cameraLatitude, cameraLongitude sql.NullFloat64
			var fetchPixelLimit, cardPixelLimit sql.NullInt64
			var cardOutcome, retryNotBefore, pictureSHA256Used, cardInputSHA256, cardModelProvider, cardModelName, cardModelEffort sql.NullString
			if err := rows.Scan(&readyPhoto.ApplePhotoUUID, &cameraLatitude, &cameraLongitude, &readyPhoto.PictureSHA256, &fetchPixelLimit,
				&cardOutcome, &retryNotBefore, &pictureSHA256Used, &cardPixelLimit, &cardInputSHA256, &cardModelProvider, &cardModelName, &cardModelEffort); err != nil {
				return err
			}
			if fetchPixelLimit.Valid {
				limitUsed := int32(fetchPixelLimit.Int64)
				readyPhoto.PictureLongEdgePixelLimitUsed = &limitUsed
			}
			if cameraLatitude.Valid {
				cameraSpot := CameraSpotForCameraPosition(cameraLatitude.Float64, cameraLongitude.Float64)
				requestSHA256, err := placelookup.PlaceLookupRequestSHA256(placelookup.PhotonReverseLookupRequestForCameraSpot(
					cameraSpot.CameraSpotKey, cameraSpot.CameraSpotLatitude, cameraSpot.CameraSpotLongitude))
				if err != nil {
					return err
				}
				if _, lookupIsStored := storedLookupStates[requestSHA256]; !lookupIsStored {
					return nil
				}
			}
			cardIsForThisPicture := pictureSHA256Used.String == readyPhoto.PictureSHA256
			switch {
			case !cardOutcome.Valid || !cardIsForThisPicture:
			case cardOutcome.String == photo.PhotoCardOutcome_PHOTO_CARD_OUTCOME_WRITTEN.String():
				if !includeWrittenCards {
					return nil
				}
				readyPhoto.WrittenCardIsCurrentForPicture = true
				readyPhoto.WrittenCardModelInputSHA256 = cardInputSHA256.String
				readyPhoto.WrittenCardModelProvider = photo.CardModelProvider(photo.CardModelProvider_value[cardModelProvider.String])
				readyPhoto.WrittenCardModelName = cardModelName.String
				readyPhoto.WrittenCardModelEffort = cardModelEffort.String
				if cardPixelLimit.Valid {
					limitUsed := int32(cardPixelLimit.Int64)
					readyPhoto.WrittenCardPictureLongEdgePixelLimitUsed = &limitUsed
				}
			case retryNotBefore.Valid:
				if retryNotBefore.String > formatArchiveTime(now) {
					return nil
				}
			case !retryPermanentlyFailedWork:
				return nil
			}
			photosReadyForCard = append(photosReadyForCard, readyPhoto)
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("select photos ready for a card: %w", err)
	}
	return photosReadyForCard, nil
}
