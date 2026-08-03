package photos

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

	"github.com/opentrawl/opentrawl/trawlkit/cache"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const maxPhotosSQLiteSnapshotBytes int64 = 20 * 1024 * 1024 * 1024

type SQLiteSnapshotProvider struct{}

type sqliteSourceSnapshot struct {
	description SnapshotDescription
	database    *store.Store
	workingDir  string
	albumJoin   sqliteAlbumJoinTable
	report      func(SnapshotProgress)
}

func (SQLiteSnapshotProvider) OpenSnapshot(ctx context.Context, request SnapshotRequest) (SourceSnapshot, error) {
	libraryPath := strings.TrimSpace(request.LibraryPath)
	workingRoot := strings.TrimSpace(request.WorkingRoot)
	if libraryPath == "" {
		return nil, errors.New("Photos library path is required")
	}
	if workingRoot == "" {
		return nil, errors.New("caller-owned Photos source working root is required")
	}
	if err := os.MkdirAll(workingRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create Photos source working root: %w", err)
	}
	workingDir, err := os.MkdirTemp(workingRoot, "photos-source-snapshot-*")
	if err != nil {
		return nil, fmt.Errorf("create Photos source snapshot working directory: %w", err)
	}
	cleanupWorkingDirectory := func() { _ = os.RemoveAll(workingDir) }

	dbPath := filepath.Join(libraryPath, "database", "Photos.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		cleanupWorkingDirectory()
		return nil, fmt.Errorf("open Photos sqlite snapshot: %w", err)
	}
	if request.ReportProgress != nil {
		request.ReportProgress(SnapshotProgress{Phase: SnapshotProgressCopyingDatabase})
	}
	snapshot, err := snapshotPhotosSQLite(ctx, dbPath, workingDir)
	if err != nil {
		cleanupWorkingDirectory()
		return nil, fmt.Errorf("snapshot Photos sqlite: %w", err)
	}

	db, err := store.OpenReadOnly(ctx, snapshot.Path)
	if err != nil {
		cleanupWorkingDirectory()
		return nil, fmt.Errorf("open Photos sqlite snapshot: %w", err)
	}

	activeAssetCount, uniqueActiveAssetIdentifierCount, err := sqliteActiveAssetIdentityCounts(ctx, db.DB())
	if err != nil {
		_ = db.Close()
		cleanupWorkingDirectory()
		return nil, err
	}
	if activeAssetCount != uniqueActiveAssetIdentifierCount {
		_ = db.Close()
		cleanupWorkingDirectory()
		return nil, fmt.Errorf("Photos sqlite active asset identities are not unique: assets=%d unique_identifiers=%d", activeAssetCount, uniqueActiveAssetIdentifierCount)
	}
	libraryDatabaseUUID, err := sqlitePhotosLibraryDatabaseUUID(ctx, db.DB())
	if err != nil {
		_ = db.Close()
		cleanupWorkingDirectory()
		return nil, err
	}
	albumJoin, found, err := sqliteAlbumJoin(ctx, db.DB())
	if err != nil {
		_ = db.Close()
		cleanupWorkingDirectory()
		return nil, err
	}
	if !found {
		_ = db.Close()
		cleanupWorkingDirectory()
		return nil, errors.New("required Photos sqlite album relation was not found")
	}
	description := SnapshotDescription{
		LibraryPath:                        libraryPath,
		Provider:                           SnapshotProviderPhotosSQLite,
		LibraryDatabaseUUID:                libraryDatabaseUUID,
		ExpectedActiveAssetCount:           activeAssetCount,
		ExpectedUniqueAssetIdentifierCount: uniqueActiveAssetIdentifierCount,
		DatabaseSnapshotFileCount:          len(snapshot.Files),
		DatabaseSnapshotBytes:              snapshot.SizeBytes,
		AlbumJoinTable:                     albumJoin.Table,
	}
	return &sqliteSourceSnapshot{description: description, database: db, workingDir: workingDir, albumJoin: albumJoin, report: request.ReportProgress}, nil
}

func (snapshot *sqliteSourceSnapshot) Description() SnapshotDescription { return snapshot.description }

func (snapshot *sqliteSourceSnapshot) Close() error {
	if snapshot == nil {
		return nil
	}
	var closeErr error
	if snapshot.database != nil {
		closeErr = snapshot.database.Close()
		snapshot.database = nil
	}
	removeErr := os.RemoveAll(snapshot.workingDir)
	if closeErr != nil {
		return closeErr
	}
	return removeErr
}

func (snapshot *sqliteSourceSnapshot) ReadAssetBatches(ctx context.Context, batchSize int, consume func([]Asset) error) (SnapshotReceipt, error) {
	if snapshot == nil || snapshot.database == nil {
		return SnapshotReceipt{}, errors.New("Photos source snapshot is closed")
	}
	if batchSize <= 0 {
		return SnapshotReceipt{}, errors.New("Photos source asset batch size must be positive")
	}
	if consume == nil {
		return SnapshotReceipt{}, errors.New("Photos source asset consumer is required")
	}
	receipt := SnapshotReceipt{Description: snapshot.description}
	lastAssetPrimaryKey := int64(0)
	for {
		assetRows, err := sqliteAssetBatch(ctx, snapshot.database.DB(), lastAssetPrimaryKey, batchSize)
		if err != nil {
			return SnapshotReceipt{}, err
		}
		if len(assetRows) == 0 {
			break
		}
		assetPrimaryKeys := make([]int64, len(assetRows))
		for index, assetRow := range assetRows {
			assetPrimaryKeys[index] = assetRow.pk
		}
		resources, err := sqliteResourcesForAssets(ctx, snapshot.database.DB(), assetPrimaryKeys)
		if err != nil {
			return SnapshotReceipt{}, err
		}
		albums, err := sqliteAlbumsForAssets(ctx, snapshot.database.DB(), snapshot.albumJoin, assetPrimaryKeys)
		if err != nil {
			return SnapshotReceipt{}, err
		}
		assets := make([]Asset, 0, len(assetRows))
		for _, assetRow := range assetRows {
			asset := sqliteAsset(assetRow, resources[assetRow.pk], albums[assetRow.pk])
			assets = append(assets, asset)
			receipt.ResourceCount += len(asset.Resources)
			receipt.AlbumMembershipCount += len(asset.Albums)
			if asset.Location != nil {
				receipt.LocationCount++
			}
		}
		if err := consume(assets); err != nil {
			return SnapshotReceipt{}, err
		}
		receipt.AssetCount += len(assets)
		lastAssetPrimaryKey = assetRows[len(assetRows)-1].pk
		if snapshot.report != nil {
			snapshot.report(SnapshotProgress{Phase: SnapshotProgressReadingAssets, AssetsRead: receipt.AssetCount, ExpectedAssets: snapshot.description.ExpectedActiveAssetCount})
		}
	}
	if receipt.AssetCount != snapshot.description.ExpectedActiveAssetCount {
		return SnapshotReceipt{}, fmt.Errorf("Photos sqlite active asset count does not match enumeration: source=%d enumerated=%d", snapshot.description.ExpectedActiveAssetCount, receipt.AssetCount)
	}
	receipt.Completeness = SnapshotCompleteness{
		State:                            SnapshotComplete,
		DatabaseCopyCompleted:            true,
		ResourceQueriesCompleted:         true,
		AlbumQueriesCompleted:            true,
		AssetQueryCompleted:              true,
		ActiveAssetCount:                 snapshot.description.ExpectedActiveAssetCount,
		UniqueActiveAssetIdentifierCount: snapshot.description.ExpectedUniqueAssetIdentifierCount,
	}
	return receipt, receipt.Completeness.Validate()
}

func sqliteActiveAssetIdentityCounts(ctx context.Context, db *sql.DB) (activeAssetCount, uniqueActiveAssetIdentifierCount int, err error) {
	err = db.QueryRowContext(ctx, `
select count(*), count(distinct ZUUID)
from ZASSET
where coalesce(ZTRASHEDSTATE, 0) = 0
	`).Scan(&activeAssetCount, &uniqueActiveAssetIdentifierCount)
	if err != nil {
		return 0, 0, fmt.Errorf("count Photos sqlite active asset identities: %w", err)
	}
	return activeAssetCount, uniqueActiveAssetIdentifierCount, nil
}

func sqlitePhotosLibraryDatabaseUUID(ctx context.Context, db *sql.DB) (PhotosLibraryDatabaseUUID, error) {
	var distinctCount int
	var identifier string
	err := db.QueryRowContext(ctx, `select count(distinct upper(trim(Z_UUID))), coalesce(min(upper(trim(Z_UUID))), '') from Z_METADATA where trim(coalesce(Z_UUID, '')) <> ''`).Scan(&distinctCount, &identifier)
	if err != nil {
		return "", fmt.Errorf("read Photos library database UUID: %w", err)
	}
	if distinctCount != 1 {
		return "", fmt.Errorf("Photos sqlite must contain exactly one database UUID; found %d", distinctCount)
	}
	value := PhotosLibraryDatabaseUUID(identifier)
	if err := value.Validate(); err != nil {
		return "", err
	}
	return value, nil
}

func snapshotPhotosSQLite(ctx context.Context, sourcePath, destinationDir string) (cache.SQLiteSnapshot, error) {
	destination := strings.TrimSpace(destinationDir)
	if destination == "" {
		return cache.SQLiteSnapshot{}, errors.New("caller-owned Photos source snapshot directory is required")
	}
	return cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     sourcePath,
		DestinationDir: destination,
		Name:           "Photos.sqlite",
		MaxFileBytes:   maxPhotosSQLiteSnapshotBytes,
	})
}

func sqliteAssetBatch(ctx context.Context, db *sql.DB, afterPrimaryKey int64, limit int) ([]sqliteAssetRow, error) {
	rows, err := db.QueryContext(ctx, `
select a.Z_PK,
       coalesce(a.ZUUID, ''),
       coalesce(a.ZKIND, -1),
       coalesce(a.ZKINDSUBTYPE, 0),
       cast(a.ZDATECREATED as real),
       cast(a.ZMODIFICATIONDATE as real),
       cast(a.ZADDEDDATE as real),
       coalesce(nullif(aa.ZTIMEZONENAME, ''), ea.ZTIMEZONENAME, ''),
       coalesce(a.ZWIDTH, 0),
       coalesce(a.ZHEIGHT, 0),
       coalesce(a.ZDURATION, 0),
       coalesce(a.ZFAVORITE, 0),
       coalesce(a.ZHIDDEN, 0),
       coalesce(a.ZAVALANCHEUUID, ''),
       cast(a.ZLATITUDE as real),
       cast(a.ZLONGITUDE as real),
       cast(aa.ZGPSHORIZONTALACCURACY as real),
       coalesce(a.ZUNIFORMTYPEIDENTIFIER, ''),
       coalesce(a.ZFILENAME, ''),
       coalesce(aa.ZORIGINALFILENAME, ''),
       coalesce(ea.ZCAMERAMAKE, ''),
       coalesce(ea.ZCAMERAMODEL, ''),
       coalesce(ea.ZLENSMODEL, ''),
       cast(ea.ZFOCALLENGTH as real),
       cast(ea.ZFOCALLENGTHIN35MM as real),
       cast(ea.ZAPERTURE as real),
       cast(ea.ZSHUTTERSPEED as real),
       cast(ea.ZISO as real)
from ZASSET a
left join ZADDITIONALASSETATTRIBUTES aa on aa.ZASSET = a.Z_PK
left join ZEXTENDEDATTRIBUTES ea on ea.ZASSET = a.Z_PK
where coalesce(a.ZTRASHEDSTATE, 0) = 0
  and coalesce(a.ZUUID, '') <> ''
  and a.Z_PK > ?
order by a.Z_PK
limit ?
`, afterPrimaryKey, limit)
	if err != nil {
		return nil, fmt.Errorf("query sqlite assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	assetRows := []sqliteAssetRow{}
	for rows.Next() {
		var row sqliteAssetRow
		if err := rows.Scan(
			&row.pk,
			&row.uuid,
			&row.kind,
			&row.kindSubtype,
			&row.creationDate,
			&row.modificationDate,
			&row.addedDate,
			&row.timezoneName,
			&row.width,
			&row.height,
			&row.duration,
			&row.favorite,
			&row.hidden,
			&row.burstIdentifier,
			&row.latitude,
			&row.longitude,
			&row.horizontalAccuracy,
			&row.uti,
			&row.filename,
			&row.originalFilename,
			&row.cameraMake,
			&row.cameraModel,
			&row.lensModel,
			&row.focalLengthMM,
			&row.focalLength35MM,
			&row.aperture,
			&row.shutterSpeed,
			&row.iso,
		); err != nil {
			return nil, err
		}
		assetRows = append(assetRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return assetRows, nil
}

func sqliteAsset(row sqliteAssetRow, resources []Resource, albums []AlbumMembership) Asset {
	asset := Asset{
		PhotosSQLiteAssetPrimaryKey: row.pk,
		LocalIdentifier:             row.uuid,
		MediaType:                   sqliteMediaType(row.kind),
		PhotosSQLiteKind:            row.kind,
		PhotosSQLiteKindSubtype:     row.kindSubtype,
		CreationDate:                coreDataTime(row.creationDate),
		ModificationDate:            coreDataTime(row.modificationDate),
		AddedDate:                   coreDataTime(row.addedDate),
		TimezoneName:                row.timezoneName,
		Width:                       row.width,
		Height:                      row.height,
		DurationSeconds:             row.duration,
		Favorite:                    row.favorite != 0,
		Hidden:                      row.hidden != 0,
		BurstIdentifier:             row.burstIdentifier,
		UniformTypeIdentifier:       row.uti,
		Filename:                    row.filename,
		OriginalFilename:            row.originalFilename,
		Camera:                      sqliteCamera(row),
		Resources:                   resources,
		Albums:                      albums,
	}
	if row.latitude.Valid && row.longitude.Valid && validLocation(row.latitude.Float64, row.longitude.Float64) {
		var accuracy *float64
		if row.horizontalAccuracy.Valid && row.horizontalAccuracy.Float64 >= 0 {
			accuracy = &row.horizontalAccuracy.Float64
		}
		asset.Location = &Location{Latitude: row.latitude.Float64, Longitude: row.longitude.Float64, HorizontalAccuracy: accuracy}
	}
	return asset
}

func sqliteResourcesForAssets(ctx context.Context, db *sql.DB, assetPrimaryKeys []int64) (map[int64][]Resource, error) {
	placeholders, arguments := sqliteIntegerArguments(assetPrimaryKeys)
	rows, err := db.QueryContext(ctx, `
select r.Z_PK,
       r.ZASSET,
       coalesce(r.ZRESOURCETYPE, -1),
       coalesce(r.ZCOMPACTUTI, ''),
       coalesce(a.ZUNIFORMTYPEIDENTIFIER, ''),
       coalesce(aa.ZORIGINALFILENAME, a.ZFILENAME, ''),
       coalesce(r.ZDATALENGTH, 0),
       coalesce(r.ZSTABLEHASH, ''),
       coalesce(r.ZFINGERPRINT, ''),
       coalesce(r.ZLOCALAVAILABILITY, 0),
       coalesce(r.ZREMOTEAVAILABILITY, 0),
       coalesce(r.ZVERSION, 0)
from ZINTERNALRESOURCE r
left join ZASSET a on a.Z_PK = r.ZASSET
left join ZADDITIONALASSETATTRIBUTES aa on aa.ZASSET = a.Z_PK
where r.ZASSET in (`+placeholders+`)
order by r.ZASSET, r.ZRESOURCETYPE, r.ZVERSION
`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query sqlite resources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64][]Resource{}
	for rows.Next() {
		var resourcePrimaryKey, assetPK, resourceType, fileSize, localAvailability, remoteAvailability, version int64
		var compactUTI, assetUTI, originalFilename, stableHash, fingerprint string
		if err := rows.Scan(&resourcePrimaryKey, &assetPK, &resourceType, &compactUTI, &assetUTI, &originalFilename, &fileSize, &stableHash, &fingerprint, &localAvailability, &remoteAvailability, &version); err != nil {
			return nil, err
		}
		uti := humanUTI(compactUTI)
		if uti == "" && resourceType == 0 {
			uti = humanUTI(assetUTI)
		}
		availableLocally := localAvailability > 0
		needsDownload := !availableLocally && remoteAvailability > 0
		out[assetPK] = append(out[assetPK], Resource{
			PhotosSQLiteResourcePrimaryKey: resourcePrimaryKey,
			PhotosSQLiteResourceType:       resourceType,
			PhotosSQLiteCompactUTI:         compactUTI,
			PhotosSQLiteResourceVersion:    version,
			PhotosSQLiteLocalAvailability:  localAvailability,
			PhotosSQLiteRemoteAvailability: remoteAvailability,
			PhotosSQLiteStableHash:         stableHash,
			PhotosSQLiteFingerprint:        fingerprint,
			Kind:                           sqliteResourceKind(resourceType),
			UniformTypeIdentifier:          humanUTI(uti),
			OriginalFilename:               originalFilename,
			Availability:                   sqliteAvailability(availableLocally, needsDownload),
			FileSize:                       fileSize,
			AvailableLocally:               availableLocally,
			NeedsDownload:                  needsDownload,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sqliteAlbumsForAssets(ctx context.Context, db *sql.DB, join sqliteAlbumJoinTable, assetPrimaryKeys []int64) (map[int64][]AlbumMembership, error) {
	placeholders, arguments := sqliteIntegerArguments(assetPrimaryKeys)
	query := fmt.Sprintf(`
select m.%s,
       coalesce(g.ZUUID, printf('sqlite_album:%%d', g.Z_PK)),
       coalesce(g.ZTITLE, ''),
       coalesce(g.ZKIND, -1),
       coalesce(g.ZCLOUDALBUMSUBTYPE, 0)
from %s m
join ZGENERICALBUM g on g.Z_PK = m.%s
where coalesce(g.ZTRASHEDSTATE, 0) = 0
  and g.ZKIND = 2
  and m.%s in (%s)
order by m.%s, g.ZTITLE
`, store.QuoteIdent(join.AssetColumn), store.QuoteIdent(join.Table), store.QuoteIdent(join.AlbumColumn), store.QuoteIdent(join.AssetColumn), placeholders, store.QuoteIdent(join.AssetColumn))
	rows, err := db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query sqlite albums: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64][]AlbumMembership{}
	for rows.Next() {
		var assetPK, kind, subtype int64
		var albumID, title string
		if err := rows.Scan(&assetPK, &albumID, &title, &kind, &subtype); err != nil {
			return nil, err
		}
		out[assetPK] = append(out[assetPK], AlbumMembership{
			AlbumID:                  albumID,
			AlbumTitle:               title,
			PhotosSQLiteAlbumKind:    kind,
			PhotosSQLiteAlbumSubtype: subtype,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sqliteIntegerArguments(values []int64) (string, []any) {
	placeholders := make([]string, len(values))
	arguments := make([]any, len(values))
	for index, value := range values {
		placeholders[index] = "?"
		arguments[index] = value
	}
	return strings.Join(placeholders, ","), arguments
}

type sqliteAlbumJoinTable struct {
	Table       string
	AlbumColumn string
	AssetColumn string
}

func sqliteAlbumJoin(ctx context.Context, db *sql.DB) (sqliteAlbumJoinTable, bool, error) {
	rows, err := db.QueryContext(ctx, `select name from sqlite_schema where type = 'table' and name glob 'Z_*ASSETS' order by name`)
	if err != nil {
		return sqliteAlbumJoinTable{}, false, fmt.Errorf("list sqlite album join tables: %w", err)
	}

	tables := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			_ = rows.Close()
			return sqliteAlbumJoinTable{}, false, err
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		return sqliteAlbumJoinTable{}, false, err
	}
	if err := rows.Err(); err != nil {
		return sqliteAlbumJoinTable{}, false, err
	}

	for _, table := range tables {
		columns, err := sqliteColumnNames(ctx, db, table)
		if err != nil {
			return sqliteAlbumJoinTable{}, false, err
		}
		albumColumn, err := sqliteAlbumJoinColumn(table, columns, "album", "ALBUMS")
		if err != nil {
			return sqliteAlbumJoinTable{}, false, err
		}
		assetColumn, err := sqliteAlbumJoinColumn(table, columns, "asset", "ASSETS")
		if err != nil {
			return sqliteAlbumJoinTable{}, false, err
		}
		if albumColumn != "" && assetColumn != "" {
			return sqliteAlbumJoinTable{Table: table, AlbumColumn: albumColumn, AssetColumn: assetColumn}, true, nil
		}
	}
	return sqliteAlbumJoinTable{}, false, nil
}

func sqliteAlbumJoinColumn(table string, columns []string, role, suffix string) (string, error) {
	candidates := []string{}
	for _, column := range columns {
		if sqliteCoreDataJoinColumn(strings.ToUpper(column), suffix) {
			candidates = append(candidates, column)
		}
	}
	switch len(candidates) {
	case 0:
		return "", nil
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("ambiguous sqlite album join %s columns in %s: %s", role, table, strings.Join(candidates, ", "))
	}
}

func sqliteCoreDataJoinColumn(upper, suffix string) bool {
	if strings.HasPrefix(upper, "Z_FOK_") {
		return false
	}
	if !strings.HasPrefix(upper, "Z_") || !strings.HasSuffix(upper, suffix) {
		return false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(upper, "Z_"), suffix)
	if number == "" {
		return false
	}
	for _, r := range number {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func sqliteColumnNames(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+store.QuoteIdent(table)+`)`)
	if err != nil {
		return nil, fmt.Errorf("inspect sqlite table %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	columns := []string{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return columns, nil
}

type sqliteAssetRow struct {
	pk                 int64
	uuid               string
	kind               int64
	kindSubtype        int64
	creationDate       sql.NullFloat64
	modificationDate   sql.NullFloat64
	addedDate          sql.NullFloat64
	timezoneName       string
	width              int64
	height             int64
	duration           float64
	favorite           int64
	hidden             int64
	burstIdentifier    string
	latitude           sql.NullFloat64
	longitude          sql.NullFloat64
	horizontalAccuracy sql.NullFloat64
	uti                string
	filename           string
	originalFilename   string
	cameraMake         string
	cameraModel        string
	lensModel          string
	focalLengthMM      sql.NullFloat64
	focalLength35MM    sql.NullFloat64
	aperture           sql.NullFloat64
	shutterSpeed       sql.NullFloat64
	iso                sql.NullFloat64
}

func sqliteCamera(row sqliteAssetRow) *Camera {
	camera := &Camera{
		Make:            strings.TrimSpace(row.cameraMake),
		Model:           strings.TrimSpace(row.cameraModel),
		LensModel:       strings.TrimSpace(row.lensModel),
		FocalLengthMM:   nullFloat(row.focalLengthMM),
		FocalLength35MM: nullFloat(row.focalLength35MM),
		Aperture:        nullFloat(row.aperture),
		ShutterSpeed:    nullFloat(row.shutterSpeed),
		ISO:             nullIntFromFloat(row.iso),
	}
	if camera.Make == "" && camera.Model == "" && camera.LensModel == "" &&
		camera.FocalLengthMM == nil && camera.FocalLength35MM == nil &&
		camera.Aperture == nil && camera.ShutterSpeed == nil && camera.ISO == nil {
		return nil
	}
	return camera
}

func nullFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	v := value.Float64
	return &v
}

func nullIntFromFloat(value sql.NullFloat64) *int64 {
	if !value.Valid {
		return nil
	}
	v := int64(math.Round(value.Float64))
	return &v
}

func sqliteMediaType(kind int64) MediaType {
	switch kind {
	case 0:
		return "image"
	case 1:
		return "video"
	default:
		return MediaType(fmt.Sprintf("kind:%d", kind))
	}
}

// sqliteResourceKind names the ZRESOURCETYPE codes we know; the typed source
// field retains every raw code independently of this readable projection.
func sqliteResourceKind(code int64) ResourceKind {
	switch code {
	case 0:
		return ResourceKindPhoto
	case 1:
		return ResourceKindVideo
	default:
		return ResourceKindUnknown
	}
}

// humanUTI keeps real type identifiers (public.jpeg) and drops Photos' numeric
// compact codes, which mean nothing to a reader; the raw code stays in evidence.
func humanUTI(uti string) string {
	if strings.ContainsAny(uti, ".") {
		return uti
	}
	return ""
}

func sqliteAvailability(local, remote bool) ResourceAvailability {
	switch {
	case local:
		return ResourceAvailabilityLocal
	case remote:
		return ResourceAvailabilityRemote
	default:
		return ResourceAvailabilityUnknown
	}
}

func validLocation(latitude, longitude float64) bool {
	if !(latitude >= -90 && latitude <= 90 && longitude >= -180 && longitude <= 180) {
		return false
	}
	return latitude != 0 || longitude != 0
}

func coreDataTime(value sql.NullFloat64) string {
	if !value.Valid {
		return ""
	}
	seconds := int64(value.Float64)
	nanoseconds := int64((value.Float64 - float64(seconds)) * float64(time.Second))
	return time.Unix(978307200+seconds, nanoseconds).UTC().Format(time.RFC3339Nano)
}
