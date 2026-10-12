// Package applelibrary is step 1, read_apple_library: it copies what Apple's
// Photos library already knows about each photo. It reads a snapshot of
// Photos.sqlite and never writes to the library.
package applelibrary

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ringsaturn/tzf"

	"github.com/opentrawl/opentrawl/trawlkit/cache"
	photo "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photo"
	photosstep "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/photos/step"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// appleCoreDataEpoch is where Photos.sqlite timestamps count from. The SQLite
// driver turns columns declared TIMESTAMP into Unix times, so every query casts
// them to real and adds this epoch here.
var appleCoreDataEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

const appleTripHighlightKind = 3

type ReadAppleLibraryStep struct{}

func (ReadAppleLibraryStep) PhotosStepName() photosstep.PhotosStepName {
	return photosstep.PhotosStepName_PHOTOS_STEP_NAME_READ_APPLE_LIBRARY
}

func DefaultApplePhotosLibraryPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory for the Apple Photos library: %w", err)
	}
	return filepath.Join(home, "Pictures", "Photos Library.photoslibrary"), nil
}

// RunPhotosStepOnOneInput returns the complete snapshot or an error; it never
// returns a partial read.
func (ReadAppleLibraryStep) RunPhotosStepOnOneInput(
	ctx context.Context,
	readAppleLibraryRequest *photosstep.ReadAppleLibraryRequest,
) (*photosstep.AppleLibrarySnapshot, error) {
	applePhotosDatabasePath := filepath.Join(readAppleLibraryRequest.GetApplePhotosLibraryPath(), "database", "Photos.sqlite")
	if readAppleLibraryRequest.GetReadWithoutCreatingSnapshot() {
		applePhotosDatabase, err := store.OpenReadOnlyWithoutWritingFiles(ctx, applePhotosDatabasePath)
		if err != nil {
			return nil, fmt.Errorf("open Apple Photos database without writing files: %w", err)
		}
		defer func() { _ = applePhotosDatabase.Close() }()
		applePhotosReadTransaction, err := applePhotosDatabase.DB().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return nil, fmt.Errorf("begin Apple Photos read transaction: %w", err)
		}
		defer func() { _ = applePhotosReadTransaction.Rollback() }()
		return readAppleLibrarySnapshot(ctx, applePhotosReadTransaction, readAppleLibraryRequest)
	}
	snapshotDirectory, err := os.MkdirTemp("", "opentrawl-apple-photos-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("create Apple Photos snapshot directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(snapshotDirectory) }()
	applePhotosDatabaseSnapshot, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     applePhotosDatabasePath,
		DestinationDir: snapshotDirectory,
		Name:           "Photos.sqlite",
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot Apple Photos database: %w", err)
	}
	applePhotosDatabase, err := store.OpenReadOnly(ctx, applePhotosDatabaseSnapshot.Path)
	if err != nil {
		return nil, fmt.Errorf("open Apple Photos database snapshot: %w", err)
	}
	defer func() { _ = applePhotosDatabase.Close() }()
	return readAppleLibrarySnapshot(ctx, applePhotosDatabase.DB(), readAppleLibraryRequest)
}

type applePhotosDatabaseReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readAppleLibrarySnapshot(
	ctx context.Context,
	applePhotosDatabase applePhotosDatabaseReader,
	readAppleLibraryRequest *photosstep.ReadAppleLibraryRequest,
) (*photosstep.AppleLibrarySnapshot, error) {
	timeZoneMap, err := tzf.NewDefaultFinder()
	if err != nil {
		return nil, fmt.Errorf("load the time-zone map: %w", err)
	}
	applePhotosByAssetPrimaryKey, applePhotosNewestFirst, err := readApplePhotoRows(ctx, applePhotosDatabase, readAppleLibraryRequest, timeZoneMap)
	if err != nil {
		return nil, err
	}
	if err := readAppleNamedPeople(ctx, applePhotosDatabase, applePhotosByAssetPrimaryKey); err != nil {
		return nil, err
	}
	if err := readAppleAlbums(ctx, applePhotosDatabase, applePhotosByAssetPrimaryKey); err != nil {
		return nil, err
	}
	if err := readAppleRecognisedText(ctx, applePhotosDatabase, applePhotosByAssetPrimaryKey); err != nil {
		return nil, err
	}
	appleLibraryTrips, err := readAppleTrips(ctx, applePhotosDatabase, applePhotosNewestFirst,
		readAppleLibraryRequest.NewestPhotoLimit != nil || len(readAppleLibraryRequest.GetSelectedApplePhotoUuids()) > 0)
	if err != nil {
		return nil, err
	}
	return &photosstep.AppleLibrarySnapshot{
		AppleLibraryPhotosNewestFirst: applePhotosNewestFirst,
		AppleLibraryTrips:             appleLibraryTrips,
	}, nil
}

const applePhotoRowsQuery = `
select
  a.Z_PK,
  a.ZUUID,
  a.ZLIBRARYSCOPE is not null,
  (select max(p.ZISCURRENTUSER) from ZASSETCONTRIBUTOR c join ZSHAREPARTICIPANT p on p.Z_PK = c.ZPARTICIPANT
     where c.Z3LIBRARYSCOPEASSETCONTRIBUTORS = a.Z_PK),
  a.ZKIND,
  a.ZKINDSUBTYPE,
  a.ZISDETECTEDSCREENSHOT,
  cast(a.ZDATECREATED as real),
  aa.ZTIMEZONEOFFSET,
  aa.ZTIMEZONENAME,
  ea.ZTIMEZONEOFFSET,
  ea.ZTIMEZONENAME,
  aa.ZINFERREDTIMEZONEOFFSET,
  a.ZLOCATIONDATA,
  aa.ZSHIFTEDLOCATIONISVALID,
  aa.ZSHIFTEDLOCATIONDATA,
  aa.ZREVERSELOCATIONDATA,
  m.ZUUID,
  m.ZTITLE,
  ea.ZCAMERAMAKE,
  ea.ZCAMERAMODEL,
  ea.ZLENSMODEL,
  ea.ZFOCALLENGTH,
  ea.ZFOCALLENGTHIN35MM,
  ea.ZAPERTURE,
  ea.ZSHUTTERSPEED,
  ea.ZISO,
  a.ZWIDTH,
  a.ZHEIGHT,
  a.ZDURATION,
  a.ZAVALANCHEUUID,
  d.ZLONGDESCRIPTION,
  aa.ZTITLE,
  a.ZFAVORITE,
  a.ZHIDDEN,
  a.ZADJUSTMENTSSTATE,
  cast(a.ZMODIFICATIONDATE as real),
  trip.ZUUID
from ZASSET a
join ZADDITIONALASSETATTRIBUTES aa on aa.ZASSET = a.Z_PK
left join ZEXTENDEDATTRIBUTES ea on ea.ZASSET = a.Z_PK
left join ZASSETDESCRIPTION d on d.Z_PK = aa.ZASSETDESCRIPTION
left join ZMOMENT m on m.Z_PK = a.ZMOMENT
left join ZPHOTOSHIGHLIGHT trip on trip.Z_PK = a.ZDAYGROUPHIGHLIGHTBEINGASSETS and trip.ZKIND = ?
where a.ZTRASHEDSTATE = 0 and a.ZVISIBILITYSTATE = 0 and a.ZCOLLECTIONSHARE is null`

func readApplePhotoRows(
	ctx context.Context,
	applePhotosDatabase applePhotosDatabaseReader,
	readAppleLibraryRequest *photosstep.ReadAppleLibraryRequest,
	timeZoneMap tzf.F,
) (map[int64]*photo.AppleLibraryPhotoFacts, []*photo.AppleLibraryPhotoFacts, error) {
	query := applePhotoRowsQuery
	queryArguments := []any{appleTripHighlightKind}
	if selectedUUIDs := readAppleLibraryRequest.GetSelectedApplePhotoUuids(); len(selectedUUIDs) > 0 {
		query += " and a.ZUUID in (" + strings.TrimSuffix(strings.Repeat("?,", len(selectedUUIDs)), ",") + ")"
		for _, selectedUUID := range selectedUUIDs {
			queryArguments = append(queryArguments, selectedUUID)
		}
	}
	query += " order by a.ZDATECREATED desc, a.ZUUID"
	if readAppleLibraryRequest.NewestPhotoLimit != nil {
		query += " limit ?"
		queryArguments = append(queryArguments, readAppleLibraryRequest.GetNewestPhotoLimit())
	}
	rows, err := applePhotosDatabase.QueryContext(ctx, query, queryArguments...)
	if err != nil {
		return nil, nil, fmt.Errorf("read Apple photos: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applePhotosByAssetPrimaryKey := map[int64]*photo.AppleLibraryPhotoFacts{}
	var applePhotosNewestFirst []*photo.AppleLibraryPhotoFacts
	for rows.Next() {
		var (
			assetPrimaryKey                       int64
			applePhotoUUID                        string
			isInICloudSharedLibrary               bool
			sharedLibraryContributorIsCurrentUser sql.NullBool
			appleMediaKind, appleMediaSubtype     int32
			isAppleDetectedScreenshot             sql.NullBool
			captureSecondsSinceAppleEpoch         float64
			additionalAttributesOffsetSeconds     sql.NullInt32
			additionalAttributesTimeZoneName      sql.NullString
			extendedAttributesOffsetSeconds       sql.NullInt32
			extendedAttributesTimeZoneName        sql.NullString
			inferredOffsetSeconds                 sql.NullInt32
			locationData                          []byte
			shiftedLocationIsValid                sql.NullBool
			shiftedLocationData                   []byte
			reverseLocationData                   []byte
			momentUUID, momentTitle               sql.NullString
			cameraMake, cameraModel, lensModel    sql.NullString
			focalLengthMillimetres                sql.NullFloat64
			focalLength35mmEquivalentMillimetres  sql.NullInt32
			apertureFNumber, exposureTimeSeconds  sql.NullFloat64
			isoSpeed                              sql.NullInt32
			pixelWidth, pixelHeight               sql.NullInt32
			durationSeconds                       sql.NullFloat64
			burstUUID, caption, title             sql.NullString
			isFavourite, isHidden                 sql.NullBool
			adjustmentsState                      sql.NullInt32
			modificationSecondsSinceAppleEpoch    sql.NullFloat64
			tripUUID                              sql.NullString
		)
		if err := rows.Scan(
			&assetPrimaryKey, &applePhotoUUID, &isInICloudSharedLibrary, &sharedLibraryContributorIsCurrentUser,
			&appleMediaKind, &appleMediaSubtype, &isAppleDetectedScreenshot, &captureSecondsSinceAppleEpoch,
			&additionalAttributesOffsetSeconds, &additionalAttributesTimeZoneName,
			&extendedAttributesOffsetSeconds, &extendedAttributesTimeZoneName, &inferredOffsetSeconds,
			&locationData, &shiftedLocationIsValid, &shiftedLocationData, &reverseLocationData,
			&momentUUID, &momentTitle,
			&cameraMake, &cameraModel, &lensModel, &focalLengthMillimetres, &focalLength35mmEquivalentMillimetres,
			&apertureFNumber, &exposureTimeSeconds, &isoSpeed,
			&pixelWidth, &pixelHeight, &durationSeconds, &burstUUID, &caption, &title,
			&isFavourite, &isHidden, &adjustmentsState, &modificationSecondsSinceAppleEpoch, &tripUUID,
		); err != nil {
			return nil, nil, fmt.Errorf("read Apple photo row: %w", err)
		}
		applePhotoFacts := &photo.AppleLibraryPhotoFacts{
			ApplePhotoUuid:            applePhotoUUID,
			AppleLibraryScope:         photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_PERSONAL_LIBRARY,
			AppleMediaKind:            appleMediaKind,
			AppleMediaSubtype:         appleMediaSubtype,
			IsAppleDetectedScreenshot: isAppleDetectedScreenshot.Bool,
			CaptureLocalTime: photoCaptureLocalTimeWithAppleDeviceOffset(
				captureSecondsSinceAppleEpoch,
				additionalAttributesOffsetSeconds, additionalAttributesTimeZoneName,
				extendedAttributesOffsetSeconds, extendedAttributesTimeZoneName,
				inferredOffsetSeconds,
			),
			AppleMomentUuid:       momentUUID.String,
			AppleMomentTitle:      momentTitle.String,
			CameraMake:            strings.TrimSpace(cameraMake.String),
			CameraModel:           strings.TrimSpace(cameraModel.String),
			LensModel:             strings.TrimSpace(lensModel.String),
			PixelWidth:            pixelWidth.Int32,
			PixelHeight:           pixelHeight.Int32,
			AppleBurstUuid:        burstUUID.String,
			AppleCaption:          strings.TrimSpace(caption.String),
			AppleTitle:            strings.TrimSpace(title.String),
			IsFavourite:           isFavourite.Bool,
			IsHidden:              isHidden.Bool,
			AppleAdjustmentsState: adjustmentsState.Int32,
		}
		if isInICloudSharedLibrary {
			applePhotoFacts.AppleLibraryScope = photo.AppleLibraryScope_APPLE_LIBRARY_SCOPE_ICLOUD_SHARED_LIBRARY
		}
		if sharedLibraryContributorIsCurrentUser.Valid {
			applePhotoFacts.AppleSharedLibraryContributorIsOwner = &sharedLibraryContributorIsCurrentUser.Bool
		}
		if applePhotoFacts.CameraLocation, err = decodeAppleLocationData(locationData); err != nil {
			return nil, nil, fmt.Errorf("read location of Apple photo %s: %w", applePhotoUUID, err)
		}
		if err := setPhotoCaptureTimeZone(applePhotoFacts.CaptureLocalTime, applePhotoFacts.CameraLocation, timeZoneMap); err != nil {
			return nil, nil, fmt.Errorf("find capture time zone of Apple photo %s: %w", applePhotoUUID, err)
		}
		if shiftedLocationIsValid.Bool {
			if applePhotoFacts.AppleChinaShiftedLocation, err = decodeAppleChinaShiftedLocationData(shiftedLocationData); err != nil {
				return nil, nil, fmt.Errorf("read China-grid location of Apple photo %s: %w", applePhotoUUID, err)
			}
		}
		if applePhotoFacts.AppleStoredPlace, err = decodeAppleReverseLocationData(reverseLocationData); err != nil {
			return nil, nil, fmt.Errorf("read stored place of Apple photo %s: %w", applePhotoUUID, err)
		}
		if focalLengthMillimetres.Valid && focalLengthMillimetres.Float64 > 0 {
			applePhotoFacts.FocalLengthMillimetres = &focalLengthMillimetres.Float64
		}
		if focalLength35mmEquivalentMillimetres.Valid && focalLength35mmEquivalentMillimetres.Int32 > 0 {
			applePhotoFacts.FocalLength_35MmEquivalentMillimetres = &focalLength35mmEquivalentMillimetres.Int32
		}
		if apertureFNumber.Valid && apertureFNumber.Float64 > 0 {
			applePhotoFacts.ApertureFNumber = &apertureFNumber.Float64
		}
		if exposureTimeSeconds.Valid && exposureTimeSeconds.Float64 > 0 {
			applePhotoFacts.ExposureTimeSeconds = &exposureTimeSeconds.Float64
		}
		if isoSpeed.Valid && isoSpeed.Int32 > 0 {
			applePhotoFacts.IsoSpeed = &isoSpeed.Int32
		}
		if durationSeconds.Valid && durationSeconds.Float64 > 0 {
			applePhotoFacts.AppleDurationSeconds = &durationSeconds.Float64
		}
		if modificationSecondsSinceAppleEpoch.Valid {
			applePhotoFacts.AppleModificationTimeUtc = timestampFromAppleSeconds(modificationSecondsSinceAppleEpoch.Float64)
		}
		if tripUUID.Valid {
			applePhotoFacts.AppleTripUuids = []string{tripUUID.String}
		}
		applePhotosByAssetPrimaryKey[assetPrimaryKey] = applePhotoFacts
		applePhotosNewestFirst = append(applePhotosNewestFirst, applePhotoFacts)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read Apple photos: %w", err)
	}
	if missing := missingSelectedApplePhotoUUIDs(readAppleLibraryRequest.GetSelectedApplePhotoUuids(), applePhotosNewestFirst); len(missing) > 0 {
		return nil, nil, fmt.Errorf("no visible photo in Apple Photos has UUID %s", strings.Join(missing, ", "))
	}
	return applePhotosByAssetPrimaryKey, applePhotosNewestFirst, nil
}

func missingSelectedApplePhotoUUIDs(selectedApplePhotoUUIDs []string, applePhotos []*photo.AppleLibraryPhotoFacts) []string {
	found := map[string]bool{}
	for _, applePhoto := range applePhotos {
		found[applePhoto.GetApplePhotoUuid()] = true
	}
	var missing []string
	for _, selectedApplePhotoUUID := range selectedApplePhotoUUIDs {
		if !found[selectedApplePhotoUUID] {
			missing = append(missing, selectedApplePhotoUUID)
		}
	}
	return missing
}

// photoCaptureLocalTimeWithAppleDeviceOffset reads Apple's device offset from
// the first Apple table that has one. The zone name comes from the same table,
// or from the other table when that table records the same offset. The
// capture zone is set later, from the camera position or this device evidence.
func photoCaptureLocalTimeWithAppleDeviceOffset(
	captureSecondsSinceAppleEpoch float64,
	additionalAttributesOffsetSeconds sql.NullInt32,
	additionalAttributesTimeZoneName sql.NullString,
	extendedAttributesOffsetSeconds sql.NullInt32,
	extendedAttributesTimeZoneName sql.NullString,
	inferredOffsetSeconds sql.NullInt32,
) *photo.PhotoCaptureLocalTime {
	captureLocalTime := &photo.PhotoCaptureLocalTime{
		CaptureTimeUtc:             timestampFromAppleSeconds(captureSecondsSinceAppleEpoch),
		AppleDeviceUtcOffsetSource: photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_NOT_RECORDED_BY_APPLE,
	}
	switch {
	case additionalAttributesOffsetSeconds.Valid:
		captureLocalTime.AppleDeviceUtcOffsetSeconds = &additionalAttributesOffsetSeconds.Int32
		captureLocalTime.AppleDeviceUtcOffsetSource = photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_APPLE_ADDITIONAL_ATTRIBUTES
		captureLocalTime.AppleDeviceTimeZoneName = additionalAttributesTimeZoneName.String
		if captureLocalTime.AppleDeviceTimeZoneName == "" && extendedAttributesOffsetSeconds.Valid &&
			extendedAttributesOffsetSeconds.Int32 == additionalAttributesOffsetSeconds.Int32 {
			captureLocalTime.AppleDeviceTimeZoneName = extendedAttributesTimeZoneName.String
		}
	case extendedAttributesOffsetSeconds.Valid:
		captureLocalTime.AppleDeviceUtcOffsetSeconds = &extendedAttributesOffsetSeconds.Int32
		captureLocalTime.AppleDeviceUtcOffsetSource = photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_APPLE_EXTENDED_ATTRIBUTES
		captureLocalTime.AppleDeviceTimeZoneName = extendedAttributesTimeZoneName.String
	case inferredOffsetSeconds.Valid:
		captureLocalTime.AppleDeviceUtcOffsetSeconds = &inferredOffsetSeconds.Int32
		captureLocalTime.AppleDeviceUtcOffsetSource = photo.AppleDeviceUtcOffsetSource_APPLE_DEVICE_UTC_OFFSET_SOURCE_APPLE_INFERRED
	default:
		// Apple can record a zone name with no
		// offset. The name is kept; the offset stays missing.
		captureLocalTime.AppleDeviceTimeZoneName = additionalAttributesTimeZoneName.String
		if captureLocalTime.AppleDeviceTimeZoneName == "" {
			captureLocalTime.AppleDeviceTimeZoneName = extendedAttributesTimeZoneName.String
		}
	}
	return captureLocalTime
}

// timestampFromAppleSeconds rounds to the microsecond: a double this size
// holds nothing finer, and rounding removes floating-point noise.
func timestampFromAppleSeconds(secondsSinceAppleEpoch float64) *timestamppb.Timestamp {
	microsecondsSinceAppleEpoch := math.Round(secondsSinceAppleEpoch * 1e6)
	return timestamppb.New(appleCoreDataEpoch.Add(time.Duration(microsecondsSinceAppleEpoch) * time.Microsecond))
}

func readAppleNamedPeople(ctx context.Context, applePhotosDatabase applePhotosDatabaseReader, applePhotosByAssetPrimaryKey map[int64]*photo.AppleLibraryPhotoFacts) error {
	rows, err := applePhotosDatabase.QueryContext(ctx, `
select f.ZASSETFORFACE, p.ZPERSONUUID, p.ZFULLNAME
from ZDETECTEDFACE f join ZPERSON p on p.Z_PK = f.ZPERSONFORFACE
where p.ZFULLNAME is not null and trim(p.ZFULLNAME) != ''
order by f.ZASSETFORFACE, p.ZFULLNAME, p.ZPERSONUUID`)
	if err != nil {
		return fmt.Errorf("read Apple named people: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var assetPrimaryKey sql.NullInt64
		var applePersonUUID, personFullName string
		if err := rows.Scan(&assetPrimaryKey, &applePersonUUID, &personFullName); err != nil {
			return fmt.Errorf("read Apple named person: %w", err)
		}
		applePhoto := applePhotosByAssetPrimaryKey[assetPrimaryKey.Int64]
		if applePhoto == nil || hasAppleNamedPerson(applePhoto, applePersonUUID) {
			continue
		}
		applePhoto.AppleNamedPeople = append(applePhoto.AppleNamedPeople, &photo.AppleLibraryPhotoNamedPerson{
			ApplePersonUuid: applePersonUUID,
			PersonFullName:  strings.TrimSpace(personFullName),
		})
	}
	return rows.Err()
}

func hasAppleNamedPerson(applePhoto *photo.AppleLibraryPhotoFacts, applePersonUUID string) bool {
	for _, namedPerson := range applePhoto.GetAppleNamedPeople() {
		if namedPerson.GetApplePersonUuid() == applePersonUUID {
			return true
		}
	}
	return false
}

const appleUserAlbumKind = 2

// readAppleAlbums reads membership of the albums a person made. Core Data names
// the join table after the entity numbers, so they are looked up first.
func readAppleAlbums(ctx context.Context, applePhotosDatabase applePhotosDatabaseReader, applePhotosByAssetPrimaryKey map[int64]*photo.AppleLibraryPhotoFacts) error {
	var assetEntityNumber, albumEntityNumber int
	if err := applePhotosDatabase.QueryRowContext(ctx, `
select (select Z_ENT from Z_PRIMARYKEY where Z_NAME = 'Asset'), (select Z_ENT from Z_PRIMARYKEY where Z_NAME = 'Album')`,
	).Scan(&assetEntityNumber, &albumEntityNumber); err != nil {
		return fmt.Errorf("find Apple album entity numbers: %w", err)
	}
	query := fmt.Sprintf(`
select j.Z_%[2]dASSETS, g.ZUUID, coalesce(g.ZTITLE, '')
from Z_%[1]dASSETS j join ZGENERICALBUM g on g.Z_PK = j.Z_%[1]dALBUMS
where g.ZKIND = ? and g.ZTRASHEDSTATE = 0
order by j.Z_%[2]dASSETS, g.ZTITLE, g.ZUUID`, albumEntityNumber, assetEntityNumber)
	rows, err := applePhotosDatabase.QueryContext(ctx, query, appleUserAlbumKind)
	if err != nil {
		return fmt.Errorf("read Apple albums: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var assetPrimaryKey int64
		var appleAlbumUUID, albumTitle string
		if err := rows.Scan(&assetPrimaryKey, &appleAlbumUUID, &albumTitle); err != nil {
			return fmt.Errorf("read Apple album: %w", err)
		}
		if applePhoto := applePhotosByAssetPrimaryKey[assetPrimaryKey]; applePhoto != nil {
			applePhoto.AppleAlbums = append(applePhoto.AppleAlbums, &photo.AppleLibraryPhotoAlbum{
				AppleAlbumUuid: appleAlbumUUID,
				AlbumTitle:     strings.TrimSpace(albumTitle),
			})
		}
	}
	return rows.Err()
}

func readAppleRecognisedText(ctx context.Context, applePhotosDatabase applePhotosDatabaseReader, applePhotosByAssetPrimaryKey map[int64]*photo.AppleLibraryPhotoFacts) error {
	rows, err := applePhotosDatabase.QueryContext(ctx, `
select ma.ZASSET, cr.ZCHARACTERRECOGNITIONDATA
from ZMEDIAANALYSISASSETATTRIBUTES ma
join ZCHARACTERRECOGNITIONATTRIBUTES cr on cr.ZMEDIAANALYSISASSETATTRIBUTES = ma.Z_PK
where cr.ZCHARACTERRECOGNITIONDATA is not null`)
	if err != nil {
		return fmt.Errorf("read Apple recognised text: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var assetPrimaryKey sql.NullInt64
		var characterRecognitionData []byte
		if err := rows.Scan(&assetPrimaryKey, &characterRecognitionData); err != nil {
			return fmt.Errorf("read Apple recognised text row: %w", err)
		}
		applePhoto := applePhotosByAssetPrimaryKey[assetPrimaryKey.Int64]
		if applePhoto == nil {
			continue
		}
		recognisedTextLines, err := decodeAppleCharacterRecognitionData(characterRecognitionData)
		if err != nil {
			return fmt.Errorf("read recognised text of Apple photo %s: %w", applePhoto.GetApplePhotoUuid(), err)
		}
		applePhoto.AppleRecognisedTextLines = recognisedTextLines
	}
	return rows.Err()
}

// appleTripTitleSeparator separates the long and short forms of a trip's date
// title in ZTITLE.
// It is U+F8FF, a private-use character that shows as nothing.
const appleTripTitleSeparator = "\uf8ff"

func readAppleTrips(ctx context.Context, applePhotosDatabase applePhotosDatabaseReader, selectedApplePhotos []*photo.AppleLibraryPhotoFacts, restrictToSelectedPhotos bool) ([]*photo.AppleLibraryTrip, error) {
	query := `
select ZUUID, coalesce(ZSUBTITLE, ''), coalesce(ZTITLE, ''), cast(ZSTARTDATE as real), cast(ZENDDATE as real)
from ZPHOTOSHIGHLIGHT where ZKIND = ?`
	queryArguments := []any{appleTripHighlightKind}
	if restrictToSelectedPhotos {
		selectedAppleTripUUIDs := map[string]bool{}
		for _, selectedApplePhoto := range selectedApplePhotos {
			for _, appleTripUUID := range selectedApplePhoto.GetAppleTripUuids() {
				if !selectedAppleTripUUIDs[appleTripUUID] {
					selectedAppleTripUUIDs[appleTripUUID] = true
					queryArguments = append(queryArguments, appleTripUUID)
				}
			}
		}
		if len(selectedAppleTripUUIDs) == 0 {
			return nil, nil
		}
		query += " and ZUUID in (" + strings.TrimSuffix(strings.Repeat("?,", len(selectedAppleTripUUIDs)), ",") + ")"
	}
	query += " order by ZSTARTDATE desc, ZUUID"
	rows, err := applePhotosDatabase.QueryContext(ctx, query, queryArguments...)
	if err != nil {
		return nil, fmt.Errorf("read Apple trips: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var appleLibraryTrips []*photo.AppleLibraryTrip
	for rows.Next() {
		var appleTripUUID, placeTitle, dateTitle string
		var startSeconds, endSeconds sql.NullFloat64
		if err := rows.Scan(&appleTripUUID, &placeTitle, &dateTitle, &startSeconds, &endSeconds); err != nil {
			return nil, fmt.Errorf("read Apple trip: %w", err)
		}
		if !startSeconds.Valid || !endSeconds.Valid {
			return nil, errors.New("trip " + appleTripUUID + " in Apple Photos has no start or end time")
		}
		longDateTitle, _, _ := strings.Cut(dateTitle, appleTripTitleSeparator)
		appleLibraryTrips = append(appleLibraryTrips, &photo.AppleLibraryTrip{
			AppleTripUuid:         appleTripUUID,
			AppleTripPlaceTitle:   strings.TrimSpace(placeTitle),
			AppleTripDateTitle:    strings.TrimSpace(longDateTitle),
			AppleTripStartTimeUtc: timestampFromAppleSeconds(startSeconds.Float64),
			AppleTripEndTimeUtc:   timestampFromAppleSeconds(endSeconds.Float64),
		})
	}
	return appleLibraryTrips, rows.Err()
}
