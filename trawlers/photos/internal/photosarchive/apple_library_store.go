package photosarchive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"google.golang.org/protobuf/proto"
)

type AppleLibrarySnapshotStoreCounts struct {
	PhotosAdded        int
	PhotosUpdated      int
	PhotosUnchanged    int
	PhotosMarkedAbsent int
	TripsWritten       int
	TripsRemoved       int
}

// AppleLibraryPhotoFingerprint is the SHA-256 of the photo's facts in
// deterministic protobuf encoding. A photo's rows are written only when it
// changes.
func AppleLibraryPhotoFingerprint(applePhotoFacts *photo.AppleLibraryPhotoFacts) (string, error) {
	encodedFacts, err := proto.MarshalOptions{Deterministic: true}.Marshal(applePhotoFacts)
	if err != nil {
		return "", fmt.Errorf("encode facts of Apple photo %s: %w", applePhotoFacts.GetApplePhotoUuid(), err)
	}
	fingerprint := sha256.Sum256(encodedFacts)
	return hex.EncodeToString(fingerprint[:]), nil
}

type storedAppleLibraryPhotoState struct {
	fingerprint                        string
	firstSeenTimeUTC                   string
	absentFromAppleLibrarySinceTimeUTC sql.NullString
}

// StoreAppleLibrarySnapshot writes a complete read in one transaction. Only a
// read of the whole library may mark photos absent.
func (photosArchive *PhotosArchive) StoreAppleLibrarySnapshot(
	ctx context.Context,
	appleLibrarySnapshot *photosstep.AppleLibrarySnapshot,
	snapshotCoversWholeAppleLibrary bool,
	storedTime time.Time,
) (AppleLibrarySnapshotStoreCounts, error) {
	var storeCounts AppleLibrarySnapshotStoreCounts
	transaction, err := photosArchive.database.BeginTx(ctx, nil)
	if err != nil {
		return storeCounts, fmt.Errorf("begin Apple library write: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	storedPhotoStates, err := readStoredAppleLibraryPhotoStates(ctx, transaction)
	if err != nil {
		return storeCounts, err
	}
	storedTimeText := formatArchiveTime(storedTime)
	presentApplePhotoUUIDs := make(map[string]bool, len(appleLibrarySnapshot.GetAppleLibraryPhotosNewestFirst()))
	for _, applePhotoFacts := range appleLibrarySnapshot.GetAppleLibraryPhotosNewestFirst() {
		applePhotoUUID := applePhotoFacts.GetApplePhotoUuid()
		presentApplePhotoUUIDs[applePhotoUUID] = true
		fingerprint, err := AppleLibraryPhotoFingerprint(applePhotoFacts)
		if err != nil {
			return storeCounts, err
		}
		storedState, wasStored := storedPhotoStates[applePhotoUUID]
		switch {
		case wasStored && storedState.fingerprint == fingerprint && !storedState.absentFromAppleLibrarySinceTimeUTC.Valid:
			storeCounts.PhotosUnchanged++
			continue
		case wasStored:
			storeCounts.PhotosUpdated++
		default:
			storeCounts.PhotosAdded++
			storedState.firstSeenTimeUTC = storedTimeText
		}
		if err := replaceAppleLibraryPhotoRows(ctx, transaction, applePhotoFacts, fingerprint, storedState.firstSeenTimeUTC, wasStored); err != nil {
			return storeCounts, err
		}
	}
	if snapshotCoversWholeAppleLibrary {
		for applePhotoUUID, storedState := range storedPhotoStates {
			if presentApplePhotoUUIDs[applePhotoUUID] || storedState.absentFromAppleLibrarySinceTimeUTC.Valid {
				continue
			}
			if _, err := transaction.ExecContext(ctx,
				`update apple_library_photo set absent_from_apple_library_since_time_utc = ? where apple_photo_uuid = ?`,
				storedTimeText, applePhotoUUID,
			); err != nil {
				return storeCounts, fmt.Errorf("mark Apple photo absent: %w", err)
			}
			storeCounts.PhotosMarkedAbsent++
		}
	}
	if storeCounts.TripsWritten, storeCounts.TripsRemoved, err = replaceChangedAppleLibraryTrips(ctx, transaction, appleLibrarySnapshot.GetAppleLibraryTrips(), snapshotCoversWholeAppleLibrary); err != nil {
		return storeCounts, err
	}
	if err := transaction.Commit(); err != nil {
		return storeCounts, fmt.Errorf("commit Apple library write: %w", err)
	}
	return storeCounts, nil
}

func readStoredAppleLibraryPhotoStates(ctx context.Context, transaction *sql.Tx) (map[string]storedAppleLibraryPhotoState, error) {
	rows, err := transaction.QueryContext(ctx, `
select apple_photo_uuid, apple_library_photo_fingerprint, first_seen_time_utc, absent_from_apple_library_since_time_utc
from apple_library_photo`)
	if err != nil {
		return nil, fmt.Errorf("read stored Apple photos: %w", err)
	}
	defer func() { _ = rows.Close() }()
	storedPhotoStates := map[string]storedAppleLibraryPhotoState{}
	for rows.Next() {
		var applePhotoUUID string
		var storedState storedAppleLibraryPhotoState
		if err := rows.Scan(&applePhotoUUID, &storedState.fingerprint, &storedState.firstSeenTimeUTC, &storedState.absentFromAppleLibrarySinceTimeUTC); err != nil {
			return nil, fmt.Errorf("read stored Apple photo: %w", err)
		}
		storedPhotoStates[applePhotoUUID] = storedState
	}
	return storedPhotoStates, rows.Err()
}

var appleLibraryPhotoChildTables = []string{
	"apple_library_photo_place_area",
	"apple_library_photo_named_person",
	"apple_library_photo_album",
	"apple_library_photo_recognised_text_line",
	"apple_library_photo_trip",
	"apple_library_photo_search",
}

func replaceAppleLibraryPhotoRows(
	ctx context.Context,
	transaction *sql.Tx,
	applePhotoFacts *photo.AppleLibraryPhotoFacts,
	fingerprint string,
	firstSeenTimeUTC string,
	photoWasStored bool,
) error {
	applePhotoUUID := applePhotoFacts.GetApplePhotoUuid()
	if photoWasStored {
		for _, childTable := range appleLibraryPhotoChildTables {
			if _, err := transaction.ExecContext(ctx, "delete from "+childTable+" where apple_photo_uuid = ?", applePhotoUUID); err != nil {
				return fmt.Errorf("clear %s of Apple photo: %w", childTable, err)
			}
		}
	}
	captureLocalTime := applePhotoFacts.GetCaptureLocalTime()
	cameraLocation := applePhotoFacts.GetCameraLocation()
	cameraLocationIsPresent := cameraLocation != nil
	if !cameraLocationIsPresent {
		cameraLocation = &photo.PhotoCameraLocation{}
	}
	chinaShiftedLocation := applePhotoFacts.GetAppleChinaShiftedLocation()
	storedPlace := applePhotoFacts.GetAppleStoredPlace()
	var appleModificationTimeUTC any
	if applePhotoFacts.GetAppleModificationTimeUtc() != nil {
		appleModificationTimeUTC = formatArchiveTime(applePhotoFacts.GetAppleModificationTimeUtc().AsTime())
	}
	if _, err := transaction.ExecContext(ctx, `
insert or replace into apple_library_photo (
  apple_photo_uuid, apple_library_scope, apple_shared_library_contributor_is_owner,
  apple_media_kind, apple_media_subtype, is_apple_detected_screenshot,
  capture_time_utc, capture_utc_offset_seconds, capture_time_zone_name, capture_time_zone_source,
  apple_device_utc_offset_seconds, apple_device_time_zone_name, apple_device_utc_offset_source,
  camera_latitude, camera_longitude, camera_altitude_metres, camera_horizontal_accuracy_metres,
  apple_china_shifted_latitude, apple_china_shifted_longitude,
  apple_stored_full_address, apple_stored_iso_country_code, apple_stored_country_name, apple_stored_state_name,
  apple_stored_county_name, apple_stored_town_name, apple_stored_neighbourhood_name, apple_stored_street,
  apple_stored_point_of_interest_name, apple_stored_water_name, apple_stored_island_name, apple_says_home,
  apple_moment_uuid, apple_moment_title,
  camera_make, camera_model, lens_model, focal_length_millimetres, focal_length_35mm_equivalent_millimetres,
  aperture_f_number, exposure_time_seconds, iso_speed, pixel_width, pixel_height,
  apple_duration_seconds, apple_burst_uuid, apple_caption, apple_title,
  is_favourite, is_hidden, apple_adjustments_state, apple_modification_time_utc,
  apple_library_photo_fingerprint, first_seen_time_utc, absent_from_apple_library_since_time_utc
) values (?,?,?, ?,?,?, ?,?,?,?, ?,?,?, ?,?,?,?, ?,?, ?,?,?,?,?,?,?,?,?,?,?,?, ?,?, ?,?,?,?,?, ?,?,?,?,?, ?,?,?,?, ?,?,?,?, ?,?,null)`,
		applePhotoUUID,
		applePhotoFacts.GetAppleLibraryScope().String(),
		optionalBoolColumn(applePhotoFacts.AppleSharedLibraryContributorIsOwner),
		applePhotoFacts.GetAppleMediaKind(),
		applePhotoFacts.GetAppleMediaSubtype(),
		applePhotoFacts.GetIsAppleDetectedScreenshot(),
		formatArchiveTime(captureLocalTime.GetCaptureTimeUtc().AsTime()),
		optionalInt32Column(captureLocalTime.CaptureUtcOffsetSeconds),
		emptyTextAsNull(captureLocalTime.GetCaptureTimeZoneName()),
		captureLocalTime.GetCaptureTimeZoneSource().String(),
		optionalInt32Column(captureLocalTime.AppleDeviceUtcOffsetSeconds),
		emptyTextAsNull(captureLocalTime.GetAppleDeviceTimeZoneName()),
		captureLocalTime.GetAppleDeviceUtcOffsetSource().String(),
		nullWhenMessageAbsent(cameraLocationIsPresent, cameraLocation.GetCameraLatitude()),
		nullWhenMessageAbsent(cameraLocationIsPresent, cameraLocation.GetCameraLongitude()),
		optionalFloat64Column(cameraLocation.CameraAltitudeMetres),
		optionalFloat64Column(cameraLocation.CameraHorizontalAccuracyMetres),
		nullWhenMessageAbsent(chinaShiftedLocation != nil, chinaShiftedLocation.GetAppleChinaShiftedLatitude()),
		nullWhenMessageAbsent(chinaShiftedLocation != nil, chinaShiftedLocation.GetAppleChinaShiftedLongitude()),
		emptyTextAsNull(storedPlace.GetAppleStoredFullAddress()),
		emptyTextAsNull(storedPlace.GetAppleStoredIsoCountryCode()),
		emptyTextAsNull(storedPlace.GetAppleStoredCountryName()),
		emptyTextAsNull(storedPlace.GetAppleStoredStateName()),
		emptyTextAsNull(storedPlace.GetAppleStoredCountyName()),
		emptyTextAsNull(storedPlace.GetAppleStoredTownName()),
		emptyTextAsNull(storedPlace.GetAppleStoredNeighbourhoodName()),
		emptyTextAsNull(storedPlace.GetAppleStoredStreet()),
		emptyTextAsNull(storedPlace.GetAppleStoredPointOfInterestName()),
		emptyTextAsNull(storedPlace.GetAppleStoredWaterName()),
		emptyTextAsNull(storedPlace.GetAppleStoredIslandName()),
		nullWhenMessageAbsent(storedPlace != nil, storedPlace.GetAppleSaysHome()),
		emptyTextAsNull(applePhotoFacts.GetAppleMomentUuid()),
		emptyTextAsNull(applePhotoFacts.GetAppleMomentTitle()),
		emptyTextAsNull(applePhotoFacts.GetCameraMake()),
		emptyTextAsNull(applePhotoFacts.GetCameraModel()),
		emptyTextAsNull(applePhotoFacts.GetLensModel()),
		optionalFloat64Column(applePhotoFacts.FocalLengthMillimetres),
		optionalInt32Column(applePhotoFacts.FocalLength_35MmEquivalentMillimetres),
		optionalFloat64Column(applePhotoFacts.ApertureFNumber),
		optionalFloat64Column(applePhotoFacts.ExposureTimeSeconds),
		optionalInt32Column(applePhotoFacts.IsoSpeed),
		applePhotoFacts.GetPixelWidth(),
		applePhotoFacts.GetPixelHeight(),
		optionalFloat64Column(applePhotoFacts.AppleDurationSeconds),
		emptyTextAsNull(applePhotoFacts.GetAppleBurstUuid()),
		emptyTextAsNull(applePhotoFacts.GetAppleCaption()),
		emptyTextAsNull(applePhotoFacts.GetAppleTitle()),
		applePhotoFacts.GetIsFavourite(),
		applePhotoFacts.GetIsHidden(),
		applePhotoFacts.GetAppleAdjustmentsState(),
		appleModificationTimeUTC,
		fingerprint,
		firstSeenTimeUTC,
	); err != nil {
		return fmt.Errorf("write Apple photo: %w", err)
	}
	for areaRankInAppleSourceOrder, placeArea := range storedPlace.GetAppleStoredPlaceAreasInSourceOrder() {
		if _, err := transaction.ExecContext(ctx, `
insert into apple_library_photo_place_area (apple_photo_uuid, area_rank_in_apple_source_order, area_name, apple_area_square_metres, apple_place_type)
values (?, ?, ?, ?, ?)`,
			applePhotoUUID, areaRankInAppleSourceOrder, placeArea.GetAreaName(), optionalFloat64Column(placeArea.AppleAreaSquareMetres), placeArea.GetApplePlaceType(),
		); err != nil {
			return fmt.Errorf("write stored place area of Apple photo: %w", err)
		}
	}
	for _, namedPerson := range applePhotoFacts.GetAppleNamedPeople() {
		if _, err := transaction.ExecContext(ctx,
			`insert into apple_library_photo_named_person (apple_photo_uuid, apple_person_uuid, person_full_name) values (?, ?, ?)`,
			applePhotoUUID, namedPerson.GetApplePersonUuid(), namedPerson.GetPersonFullName(),
		); err != nil {
			return fmt.Errorf("write named person of Apple photo: %w", err)
		}
	}
	for _, album := range applePhotoFacts.GetAppleAlbums() {
		if _, err := transaction.ExecContext(ctx,
			`insert into apple_library_photo_album (apple_photo_uuid, apple_album_uuid, album_title) values (?, ?, ?)`,
			applePhotoUUID, album.GetAppleAlbumUuid(), album.GetAlbumTitle(),
		); err != nil {
			return fmt.Errorf("write album of Apple photo: %w", err)
		}
	}
	for lineRank, recognisedText := range applePhotoFacts.GetAppleRecognisedTextLines() {
		if _, err := transaction.ExecContext(ctx,
			`insert into apple_library_photo_recognised_text_line (apple_photo_uuid, line_rank, recognised_text) values (?, ?, ?)`,
			applePhotoUUID, lineRank, recognisedText,
		); err != nil {
			return fmt.Errorf("write recognised text of Apple photo: %w", err)
		}
	}
	for _, appleTripUUID := range applePhotoFacts.GetAppleTripUuids() {
		if _, err := transaction.ExecContext(ctx,
			`insert into apple_library_photo_trip (apple_photo_uuid, apple_trip_uuid) values (?, ?)`,
			applePhotoUUID, appleTripUUID,
		); err != nil {
			return fmt.Errorf("write trip of Apple photo: %w", err)
		}
	}
	return insertAppleLibraryPhotoSearchRow(ctx, transaction, applePhotoFacts)
}

func insertAppleLibraryPhotoSearchRow(ctx context.Context, transaction *sql.Tx, applePhotoFacts *photo.AppleLibraryPhotoFacts) error {
	storedPlace := applePhotoFacts.GetAppleStoredPlace()
	placeNames := []string{
		storedPlace.GetAppleStoredFullAddress(),
		storedPlace.GetAppleStoredIsoCountryCode(),
		storedPlace.GetAppleStoredNeighbourhoodName(),
		storedPlace.GetAppleStoredTownName(),
		storedPlace.GetAppleStoredCountyName(),
		storedPlace.GetAppleStoredStateName(),
		storedPlace.GetAppleStoredCountryName(),
		storedPlace.GetAppleStoredStreet(),
		storedPlace.GetAppleStoredPointOfInterestName(),
		storedPlace.GetAppleStoredWaterName(),
		storedPlace.GetAppleStoredIslandName(),
	}
	for _, placeArea := range storedPlace.GetAppleStoredPlaceAreasInSourceOrder() {
		placeNames = append(placeNames, placeArea.GetAreaName())
	}
	var namedPeople []string
	for _, namedPerson := range applePhotoFacts.GetAppleNamedPeople() {
		namedPeople = append(namedPeople, namedPerson.GetPersonFullName())
	}
	var albumTitles []string
	for _, album := range applePhotoFacts.GetAppleAlbums() {
		albumTitles = append(albumTitles, album.GetAlbumTitle())
	}
	captionAndTitleSearchText := SearchTextWithEastAsianCharacterPairs(joinNonEmptyLines(applePhotoFacts.GetAppleCaption(), applePhotoFacts.GetAppleTitle()))
	placeNamesSearchText := SearchTextWithEastAsianCharacterPairs(joinNonEmptyLines(placeNames...))
	namedPeopleSearchText := SearchTextWithEastAsianCharacterPairs(joinNonEmptyLines(namedPeople...))
	albumTitlesSearchText := SearchTextWithEastAsianCharacterPairs(joinNonEmptyLines(albumTitles...))
	momentTitleSearchText := SearchTextWithEastAsianCharacterPairs(applePhotoFacts.GetAppleMomentTitle())
	recognisedTextSearchText := SearchTextWithEastAsianCharacterPairs(joinNonEmptyLines(applePhotoFacts.GetAppleRecognisedTextLines()...))
	if _, err := transaction.ExecContext(ctx, `
insert into apple_library_photo_search (
  apple_photo_uuid, apple_caption_and_title, apple_place_names, apple_named_people,
  apple_album_titles, apple_moment_title, apple_recognised_text
) values (?, ?, ?, ?, ?, ?, ?)`,
		applePhotoFacts.GetApplePhotoUuid(), captionAndTitleSearchText, placeNamesSearchText,
		namedPeopleSearchText, albumTitlesSearchText, momentTitleSearchText, recognisedTextSearchText,
	); err != nil {
		return fmt.Errorf("write search row of Apple photo: %w", err)
	}
	return nil
}

func joinNonEmptyLines(texts ...string) string {
	nonEmptyTexts := make([]string, 0, len(texts))
	for _, text := range texts {
		if text = strings.TrimSpace(text); text != "" {
			nonEmptyTexts = append(nonEmptyTexts, text)
		}
	}
	return strings.Join(nonEmptyTexts, "\n")
}

func replaceChangedAppleLibraryTrips(ctx context.Context, transaction *sql.Tx, appleLibraryTrips []*photo.AppleLibraryTrip, snapshotCoversWholeAppleLibrary bool) (int, int, error) {
	storedTrips, err := readAppleLibraryTrips(ctx, transaction)
	if err != nil {
		return 0, 0, err
	}
	tripsWritten := 0
	presentTripUUIDs := map[string]bool{}
	for _, appleLibraryTrip := range appleLibraryTrips {
		presentTripUUIDs[appleLibraryTrip.GetAppleTripUuid()] = true
		if proto.Equal(storedTrips[appleLibraryTrip.GetAppleTripUuid()], appleLibraryTrip) {
			continue
		}
		if _, err := transaction.ExecContext(ctx, `
insert or replace into apple_library_trip (apple_trip_uuid, apple_trip_place_title, apple_trip_date_title, apple_trip_start_time_utc, apple_trip_end_time_utc)
values (?, ?, ?, ?, ?)`,
			appleLibraryTrip.GetAppleTripUuid(),
			emptyTextAsNull(appleLibraryTrip.GetAppleTripPlaceTitle()),
			emptyTextAsNull(appleLibraryTrip.GetAppleTripDateTitle()),
			formatArchiveTime(appleLibraryTrip.GetAppleTripStartTimeUtc().AsTime()),
			formatArchiveTime(appleLibraryTrip.GetAppleTripEndTimeUtc().AsTime()),
		); err != nil {
			return 0, 0, fmt.Errorf("write Apple trip: %w", err)
		}
		tripsWritten++
	}
	tripsRemoved := 0
	for storedTripUUID := range storedTrips {
		if !snapshotCoversWholeAppleLibrary || presentTripUUIDs[storedTripUUID] {
			continue
		}
		if _, err := transaction.ExecContext(ctx, `delete from apple_library_trip where apple_trip_uuid = ?`, storedTripUUID); err != nil {
			return 0, 0, fmt.Errorf("remove Apple trip: %w", err)
		}
		tripsRemoved++
	}
	return tripsWritten, tripsRemoved, nil
}
