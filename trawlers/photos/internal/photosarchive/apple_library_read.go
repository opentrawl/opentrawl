package photosarchive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ReadStoredAppleLibraryPhotoFacts reads photos' facts back from the archive,
// in the order asked. A photo that is not stored is left out.
func (photosArchive *PhotosArchive) ReadStoredAppleLibraryPhotoFacts(ctx context.Context, applePhotoUUIDs []string) ([]*photo.AppleLibraryPhotoFacts, error) {
	if len(applePhotoUUIDs) == 0 {
		return nil, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(applePhotoUUIDs)), ",")
	uuidArguments := make([]any, len(applePhotoUUIDs))
	for index, applePhotoUUID := range applePhotoUUIDs {
		uuidArguments[index] = applePhotoUUID
	}
	rows, err := photosArchive.database.QueryContext(ctx, `
select
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
  is_favourite, is_hidden, apple_adjustments_state, apple_modification_time_utc
from apple_library_photo where apple_photo_uuid in (`+placeholders+`)`, uuidArguments...)
	if err != nil {
		return nil, fmt.Errorf("read stored Apple photos: %w", err)
	}
	defer func() { _ = rows.Close() }()
	storedFactsByUUID := map[string]*photo.AppleLibraryPhotoFacts{}
	for rows.Next() {
		var (
			applePhotoFacts                                 photo.AppleLibraryPhotoFacts
			appleLibraryScope, captureTimeUTC               string
			captureTimeZoneSource, appleDeviceOffsetSource  string
			appleDeviceOffsetSeconds                        sql.NullInt32
			appleDeviceTimeZoneName                         sql.NullString
			contributorIsOwner, appleSaysHome               sql.NullBool
			captureOffsetSeconds, focalLength35mm, isoSpeed sql.NullInt32
			captureTimeZoneName                             sql.NullString
			cameraLatitude, cameraLongitude                 sql.NullFloat64
			cameraAltitude, cameraHorizontalAccuracy        sql.NullFloat64
			shiftedLatitude, shiftedLongitude               sql.NullFloat64
			storedFullAddress, storedIsoCountryCode         sql.NullString
			storedCountryName, storedStateName              sql.NullString
			storedCountyName, storedTownName                sql.NullString
			storedNeighbourhoodName, storedStreet           sql.NullString
			storedPointOfInterestName, storedWaterName      sql.NullString
			storedIslandName                                sql.NullString
			momentUUID, momentTitle                         sql.NullString
			cameraMake, cameraModel, lensModel              sql.NullString
			focalLength, aperture, exposureTime, duration   sql.NullFloat64
			burstUUID, caption, title, modificationTimeUTC  sql.NullString
		)
		if err := rows.Scan(
			&applePhotoFacts.ApplePhotoUuid, &appleLibraryScope, &contributorIsOwner,
			&applePhotoFacts.AppleMediaKind, &applePhotoFacts.AppleMediaSubtype, &applePhotoFacts.IsAppleDetectedScreenshot,
			&captureTimeUTC, &captureOffsetSeconds, &captureTimeZoneName, &captureTimeZoneSource,
			&appleDeviceOffsetSeconds, &appleDeviceTimeZoneName, &appleDeviceOffsetSource,
			&cameraLatitude, &cameraLongitude, &cameraAltitude, &cameraHorizontalAccuracy,
			&shiftedLatitude, &shiftedLongitude,
			&storedFullAddress, &storedIsoCountryCode, &storedCountryName, &storedStateName,
			&storedCountyName, &storedTownName, &storedNeighbourhoodName, &storedStreet,
			&storedPointOfInterestName, &storedWaterName, &storedIslandName, &appleSaysHome,
			&momentUUID, &momentTitle,
			&cameraMake, &cameraModel, &lensModel, &focalLength, &focalLength35mm,
			&aperture, &exposureTime, &isoSpeed, &applePhotoFacts.PixelWidth, &applePhotoFacts.PixelHeight,
			&duration, &burstUUID, &caption, &title,
			&applePhotoFacts.IsFavourite, &applePhotoFacts.IsHidden, &applePhotoFacts.AppleAdjustmentsState, &modificationTimeUTC,
		); err != nil {
			return nil, fmt.Errorf("read stored Apple photo: %w", err)
		}
		applePhotoFacts.AppleLibraryScope = photo.AppleLibraryScope(photo.AppleLibraryScope_value[appleLibraryScope])
		applePhotoFacts.AppleSharedLibraryContributorIsOwner = optionalBoolFromColumn(contributorIsOwner)
		captureTime, err := parseArchiveTime(captureTimeUTC)
		if err != nil {
			return nil, err
		}
		applePhotoFacts.CaptureLocalTime = &photo.PhotoCaptureLocalTime{
			CaptureTimeUtc:              timestamppb.New(captureTime),
			CaptureUtcOffsetSeconds:     optionalInt32FromColumn(captureOffsetSeconds),
			CaptureTimeZoneName:         captureTimeZoneName.String,
			CaptureTimeZoneSource:       photo.PhotoCaptureTimeZoneSource(photo.PhotoCaptureTimeZoneSource_value[captureTimeZoneSource]),
			AppleDeviceUtcOffsetSeconds: optionalInt32FromColumn(appleDeviceOffsetSeconds),
			AppleDeviceTimeZoneName:     appleDeviceTimeZoneName.String,
			AppleDeviceUtcOffsetSource:  photo.AppleDeviceUtcOffsetSource(photo.AppleDeviceUtcOffsetSource_value[appleDeviceOffsetSource]),
		}
		if cameraLatitude.Valid {
			applePhotoFacts.CameraLocation = &photo.PhotoCameraLocation{
				CameraLatitude:                 cameraLatitude.Float64,
				CameraLongitude:                cameraLongitude.Float64,
				CameraAltitudeMetres:           optionalFloat64FromColumn(cameraAltitude),
				CameraHorizontalAccuracyMetres: optionalFloat64FromColumn(cameraHorizontalAccuracy),
			}
		}
		if shiftedLatitude.Valid {
			applePhotoFacts.AppleChinaShiftedLocation = &photo.AppleChinaShiftedLocation{
				AppleChinaShiftedLatitude:  shiftedLatitude.Float64,
				AppleChinaShiftedLongitude: shiftedLongitude.Float64,
			}
		}
		if appleSaysHome.Valid {
			applePhotoFacts.AppleStoredPlace = &photo.AppleStoredPlace{
				AppleStoredFullAddress:         storedFullAddress.String,
				AppleStoredIsoCountryCode:      storedIsoCountryCode.String,
				AppleStoredCountryName:         storedCountryName.String,
				AppleStoredStateName:           storedStateName.String,
				AppleStoredCountyName:          storedCountyName.String,
				AppleStoredTownName:            storedTownName.String,
				AppleStoredNeighbourhoodName:   storedNeighbourhoodName.String,
				AppleStoredStreet:              storedStreet.String,
				AppleStoredPointOfInterestName: storedPointOfInterestName.String,
				AppleStoredWaterName:           storedWaterName.String,
				AppleStoredIslandName:          storedIslandName.String,
				AppleSaysHome:                  appleSaysHome.Bool,
			}
		}
		applePhotoFacts.AppleMomentUuid = momentUUID.String
		applePhotoFacts.AppleMomentTitle = momentTitle.String
		applePhotoFacts.CameraMake = cameraMake.String
		applePhotoFacts.CameraModel = cameraModel.String
		applePhotoFacts.LensModel = lensModel.String
		applePhotoFacts.FocalLengthMillimetres = optionalFloat64FromColumn(focalLength)
		applePhotoFacts.FocalLength_35MmEquivalentMillimetres = optionalInt32FromColumn(focalLength35mm)
		applePhotoFacts.ApertureFNumber = optionalFloat64FromColumn(aperture)
		applePhotoFacts.ExposureTimeSeconds = optionalFloat64FromColumn(exposureTime)
		applePhotoFacts.IsoSpeed = optionalInt32FromColumn(isoSpeed)
		applePhotoFacts.AppleDurationSeconds = optionalFloat64FromColumn(duration)
		applePhotoFacts.AppleBurstUuid = burstUUID.String
		applePhotoFacts.AppleCaption = caption.String
		applePhotoFacts.AppleTitle = title.String
		if modificationTimeUTC.Valid {
			modificationTime, err := parseArchiveTime(modificationTimeUTC.String)
			if err != nil {
				return nil, err
			}
			applePhotoFacts.AppleModificationTimeUtc = timestamppb.New(modificationTime)
		}
		storedFactsByUUID[applePhotoFacts.ApplePhotoUuid] = &applePhotoFacts
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read stored Apple photos: %w", err)
	}
	if err := readStoredAppleLibraryPhotoChildren(ctx, photosArchive.database, placeholders, uuidArguments, storedFactsByUUID); err != nil {
		return nil, err
	}
	storedFacts := make([]*photo.AppleLibraryPhotoFacts, 0, len(storedFactsByUUID))
	for _, applePhotoUUID := range applePhotoUUIDs {
		if applePhotoFacts := storedFactsByUUID[applePhotoUUID]; applePhotoFacts != nil {
			storedFacts = append(storedFacts, applePhotoFacts)
		}
	}
	return storedFacts, nil
}

func readStoredAppleLibraryPhotoChildren(
	ctx context.Context,
	database archiveQueryer,
	placeholders string,
	uuidArguments []any,
	storedFactsByUUID map[string]*photo.AppleLibraryPhotoFacts,
) error {
	if err := scanStoredRows(ctx, database, `
select apple_photo_uuid, area_name, apple_area_square_metres, apple_place_type from apple_library_photo_place_area
where apple_photo_uuid in (`+placeholders+`) order by apple_photo_uuid, area_rank_in_apple_source_order`, uuidArguments,
		func(rows *sql.Rows) error {
			var applePhotoUUID string
			var placeArea photo.AppleStoredPlaceArea
			var areaSquareMetres sql.NullFloat64
			if err := rows.Scan(&applePhotoUUID, &placeArea.AreaName, &areaSquareMetres, &placeArea.ApplePlaceType); err != nil {
				return err
			}
			placeArea.AppleAreaSquareMetres = optionalFloat64FromColumn(areaSquareMetres)
			storedPlace := storedFactsByUUID[applePhotoUUID].GetAppleStoredPlace()
			storedPlace.AppleStoredPlaceAreasInSourceOrder = append(storedPlace.AppleStoredPlaceAreasInSourceOrder, &placeArea)
			return nil
		}); err != nil {
		return fmt.Errorf("read stored place areas: %w", err)
	}
	if err := scanStoredRows(ctx, database, `
select n.apple_photo_uuid, n.apple_person_uuid, n.person_full_name from apple_library_photo_named_person n
where n.apple_photo_uuid in (`+placeholders+`) order by n.apple_photo_uuid, n.person_full_name, n.apple_person_uuid`, uuidArguments,
		func(rows *sql.Rows) error {
			var applePhotoUUID string
			var namedPerson photo.AppleLibraryPhotoNamedPerson
			if err := rows.Scan(&applePhotoUUID, &namedPerson.ApplePersonUuid, &namedPerson.PersonFullName); err != nil {
				return err
			}
			applePhotoFacts := storedFactsByUUID[applePhotoUUID]
			applePhotoFacts.AppleNamedPeople = append(applePhotoFacts.AppleNamedPeople, &namedPerson)
			return nil
		}); err != nil {
		return fmt.Errorf("read stored named people: %w", err)
	}
	if err := scanStoredRows(ctx, database, `
select apple_photo_uuid, apple_album_uuid, album_title from apple_library_photo_album
where apple_photo_uuid in (`+placeholders+`) order by apple_photo_uuid, album_title, apple_album_uuid`, uuidArguments,
		func(rows *sql.Rows) error {
			var applePhotoUUID string
			var album photo.AppleLibraryPhotoAlbum
			if err := rows.Scan(&applePhotoUUID, &album.AppleAlbumUuid, &album.AlbumTitle); err != nil {
				return err
			}
			applePhotoFacts := storedFactsByUUID[applePhotoUUID]
			applePhotoFacts.AppleAlbums = append(applePhotoFacts.AppleAlbums, &album)
			return nil
		}); err != nil {
		return fmt.Errorf("read stored albums: %w", err)
	}
	if err := scanStoredRows(ctx, database, `
select apple_photo_uuid, recognised_text from apple_library_photo_recognised_text_line
where apple_photo_uuid in (`+placeholders+`) order by apple_photo_uuid, line_rank`, uuidArguments,
		func(rows *sql.Rows) error {
			var applePhotoUUID, recognisedText string
			if err := rows.Scan(&applePhotoUUID, &recognisedText); err != nil {
				return err
			}
			applePhotoFacts := storedFactsByUUID[applePhotoUUID]
			applePhotoFacts.AppleRecognisedTextLines = append(applePhotoFacts.AppleRecognisedTextLines, recognisedText)
			return nil
		}); err != nil {
		return fmt.Errorf("read stored recognised text: %w", err)
	}
	if err := scanStoredRows(ctx, database, `
select apple_photo_uuid, apple_trip_uuid from apple_library_photo_trip
where apple_photo_uuid in (`+placeholders+`) order by apple_photo_uuid, apple_trip_uuid`, uuidArguments,
		func(rows *sql.Rows) error {
			var applePhotoUUID, appleTripUUID string
			if err := rows.Scan(&applePhotoUUID, &appleTripUUID); err != nil {
				return err
			}
			applePhotoFacts := storedFactsByUUID[applePhotoUUID]
			applePhotoFacts.AppleTripUuids = append(applePhotoFacts.AppleTripUuids, appleTripUUID)
			return nil
		}); err != nil {
		return fmt.Errorf("read stored trips of photos: %w", err)
	}
	return nil
}

func scanStoredRows(ctx context.Context, database archiveQueryer, query string, arguments []any, scanRow func(*sql.Rows) error) error {
	rows, err := database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scanRow(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readAppleLibraryTrips(ctx context.Context, database archiveQueryer) (map[string]*photo.AppleLibraryTrip, error) {
	storedTrips := map[string]*photo.AppleLibraryTrip{}
	err := scanStoredRows(ctx, database, `
select apple_trip_uuid, apple_trip_place_title, apple_trip_date_title, apple_trip_start_time_utc, apple_trip_end_time_utc
from apple_library_trip`, nil, func(rows *sql.Rows) error {
		var appleTripUUID, startTimeUTC, endTimeUTC string
		var placeTitle, dateTitle sql.NullString
		if err := rows.Scan(&appleTripUUID, &placeTitle, &dateTitle, &startTimeUTC, &endTimeUTC); err != nil {
			return err
		}
		startTime, err := parseArchiveTime(startTimeUTC)
		if err != nil {
			return err
		}
		endTime, err := parseArchiveTime(endTimeUTC)
		if err != nil {
			return err
		}
		storedTrips[appleTripUUID] = &photo.AppleLibraryTrip{
			AppleTripUuid:         appleTripUUID,
			AppleTripPlaceTitle:   placeTitle.String,
			AppleTripDateTitle:    dateTitle.String,
			AppleTripStartTimeUtc: timestamppb.New(startTime),
			AppleTripEndTimeUtc:   timestamppb.New(endTime),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read stored Apple trips: %w", err)
	}
	return storedTrips, nil
}
