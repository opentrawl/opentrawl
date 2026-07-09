package photos

import (
	"context"
	"database/sql"
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

type SQLiteSnapshotProvider struct {
	SnapshotDir string
}

func (p SQLiteSnapshotProvider) Snapshot(ctx context.Context, libraryPath string) (LibrarySnapshot, error) {
	dbPath := filepath.Join(strings.TrimSpace(libraryPath), "database", "Photos.sqlite")
	if _, err := os.Stat(dbPath); err != nil {
		return LibrarySnapshot{}, fmt.Errorf("open Photos sqlite snapshot: %w", err)
	}
	snapshot, cleanup, err := snapshotPhotosSQLite(ctx, dbPath, p.SnapshotDir)
	if err != nil {
		return LibrarySnapshot{}, fmt.Errorf("snapshot Photos sqlite: %w", err)
	}
	defer cleanup()

	db, err := store.OpenReadOnly(ctx, snapshot.Path)
	if err != nil {
		return LibrarySnapshot{}, fmt.Errorf("open Photos sqlite snapshot: %w", err)
	}
	defer func() { _ = db.Close() }()

	resources, err := sqliteResources(ctx, db.DB())
	if err != nil {
		return LibrarySnapshot{}, err
	}
	albums, albumJoinTable, err := sqliteAlbums(ctx, db.DB())
	if err != nil {
		return LibrarySnapshot{}, err
	}
	assets, err := sqliteAssets(ctx, db.DB(), resources, albums)
	if err != nil {
		return LibrarySnapshot{}, err
	}

	return LibrarySnapshot{
		LibraryPath:   libraryPath,
		Provider:      "photos_sqlite_snapshot",
		PhotosVersion: "unknown",
		Metadata: map[string]any{
			"source":           "Photos.sqlite",
			"snapshot":         "trawlkit_sqlite_copy",
			"database_path":    "database/Photos.sqlite",
			"snapshot_files":   len(snapshot.Files),
			"snapshot_bytes":   snapshot.SizeBytes,
			"album_join_table": albumJoinTable,
			"warning":          "Private Photos Core Data schema; keep this as source evidence, not durable truth.",
		},
		Assets: assets,
	}, nil
}

func snapshotPhotosSQLite(ctx context.Context, sourcePath, destinationDir string) (cache.SQLiteSnapshot, func(), error) {
	cleanup := func() {}
	destination := strings.TrimSpace(destinationDir)
	if destination == "" {
		tmpDir, err := os.MkdirTemp("", "photoscrawl-sqlite-snapshot-*")
		if err != nil {
			return cache.SQLiteSnapshot{}, cleanup, fmt.Errorf("create sqlite snapshot temp dir: %w", err)
		}
		destination = tmpDir
		cleanup = func() { _ = os.RemoveAll(tmpDir) }
	}
	snapshot, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     sourcePath,
		DestinationDir: destination,
		Name:           "Photos.sqlite",
		MaxFileBytes:   maxPhotosSQLiteSnapshotBytes,
	})
	if err != nil {
		cleanup()
		return cache.SQLiteSnapshot{}, func() {}, err
	}
	return snapshot, cleanup, nil
}

func sqliteAssets(ctx context.Context, db *sql.DB, resources map[int64][]Resource, albums map[int64][]AlbumMembership) ([]Asset, error) {
	rows, err := db.QueryContext(ctx, `
select a.Z_PK,
       coalesce(a.ZUUID, ''),
       coalesce(a.ZKIND, -1),
       coalesce(a.ZKINDSUBTYPE, 0),
       cast(a.ZDATECREATED as real),
       cast(a.ZMODIFICATIONDATE as real),
       cast(a.ZADDEDDATE as real),
       coalesce(aa.ZTIMEZONENAME, ''),
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
order by a.ZDATECREATED, a.ZUUID
`)
	if err != nil {
		return nil, fmt.Errorf("query sqlite assets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	assets := []Asset{}
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
		asset := Asset{
			LocalIdentifier:  row.uuid,
			MediaType:        sqliteMediaType(row.kind),
			MediaSubtypes:    fmt.Sprintf("kind_subtype:%d", row.kindSubtype),
			CreationDate:     coreDataTime(row.creationDate),
			ModificationDate: coreDataTime(row.modificationDate),
			AddedDate:        coreDataTime(row.addedDate),
			TimezoneName:     row.timezoneName,
			Width:            row.width,
			Height:           row.height,
			DurationSeconds:  row.duration,
			Favorite:         row.favorite != 0,
			Hidden:           row.hidden != 0,
			BurstIdentifier:  row.burstIdentifier,
			Camera:           sqliteCamera(row),
			Resources:        resources[row.pk],
			Albums:           albums[row.pk],
			Metadata: map[string]any{
				"sqlite_pk":                  row.pk,
				"uniform_type_identifier":    row.uti,
				"filename":                   row.filename,
				"original_filename":          row.originalFilename,
				"schema_source":              "ZASSET",
				"additional_attributes_join": "ZADDITIONALASSETATTRIBUTES",
				"extended_attributes_join":   "ZEXTENDEDATTRIBUTES",
			},
		}
		if row.latitude.Valid && row.longitude.Valid && validLocation(row.latitude.Float64, row.longitude.Float64) {
			var accuracy *float64
			if row.horizontalAccuracy.Valid {
				accuracy = &row.horizontalAccuracy.Float64
			}
			asset.Location = &Location{
				Latitude:           row.latitude.Float64,
				Longitude:          row.longitude.Float64,
				HorizontalAccuracy: accuracy,
			}
		}
		assets = append(assets, asset)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return assets, nil
}

func sqliteResources(ctx context.Context, db *sql.DB) (map[int64][]Resource, error) {
	rows, err := db.QueryContext(ctx, `
select r.ZASSET,
       coalesce(r.ZRESOURCETYPE, -1),
       coalesce(r.ZCOMPACTUTI, a.ZUNIFORMTYPEIDENTIFIER, ''),
       coalesce(aa.ZORIGINALFILENAME, a.ZFILENAME, ''),
       coalesce(r.ZDATALENGTH, 0),
       coalesce(r.ZSTABLEHASH, r.ZFINGERPRINT, ''),
       coalesce(r.ZLOCALAVAILABILITY, 0),
       coalesce(r.ZREMOTEAVAILABILITY, 0),
       coalesce(r.ZVERSION, 0)
from ZINTERNALRESOURCE r
left join ZASSET a on a.Z_PK = r.ZASSET
left join ZADDITIONALASSETATTRIBUTES aa on aa.ZASSET = a.Z_PK
where r.ZASSET is not null
order by r.ZASSET, r.ZRESOURCETYPE, r.ZVERSION
`)
	if err != nil {
		return nil, fmt.Errorf("query sqlite resources: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64][]Resource{}
	for rows.Next() {
		var assetPK, resourceType, fileSize, localAvailability, remoteAvailability, version int64
		var uti, originalFilename, stableHash string
		if err := rows.Scan(&assetPK, &resourceType, &uti, &originalFilename, &fileSize, &stableHash, &localAvailability, &remoteAvailability, &version); err != nil {
			return nil, err
		}
		availableLocally := localAvailability > 0
		needsDownload := !availableLocally && remoteAvailability > 0
		out[assetPK] = append(out[assetPK], Resource{
			Type:             sqliteResourceKind(resourceType),
			UTI:              humanUTI(uti),
			OriginalFilename: originalFilename,
			Availability:     sqliteAvailability(availableLocally, needsDownload),
			FileSize:         fileSize,
			StableHash:       stableHash,
			AvailableLocally: availableLocally,
			NeedsDownload:    needsDownload,
			Metadata: map[string]any{
				"sqlite_resource_type":    resourceType,
				"sqlite_compact_uti":      uti,
				"sqlite_version":          version,
				"local_availability_raw":  localAvailability,
				"remote_availability_raw": remoteAvailability,
				"schema_source":           "ZINTERNALRESOURCE",
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func sqliteAlbums(ctx context.Context, db *sql.DB) (map[int64][]AlbumMembership, string, error) {
	join, ok, err := sqliteAlbumJoin(ctx, db)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return map[int64][]AlbumMembership{}, "", nil
	}
	query := fmt.Sprintf(`
select m.%s,
       coalesce(g.ZUUID, printf('sqlite_album:%%d', g.Z_PK)),
       coalesce(g.ZTITLE, ''),
       coalesce(g.ZKIND, -1),
       coalesce(g.ZCLOUDALBUMSUBTYPE, 0)
from %s m
join ZGENERICALBUM g on g.Z_PK = m.%s
where coalesce(g.ZTRASHEDSTATE, 0) = 0
order by m.%s, g.ZTITLE
`, store.QuoteIdent(join.AssetColumn), store.QuoteIdent(join.Table), store.QuoteIdent(join.AlbumColumn), store.QuoteIdent(join.AssetColumn))
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, "", fmt.Errorf("query sqlite albums: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := map[int64][]AlbumMembership{}
	for rows.Next() {
		var assetPK, kind, subtype int64
		var albumID, title string
		if err := rows.Scan(&assetPK, &albumID, &title, &kind, &subtype); err != nil {
			return nil, "", err
		}
		out[assetPK] = append(out[assetPK], AlbumMembership{
			AlbumID:    albumID,
			AlbumTitle: title,
			AlbumKind:  fmt.Sprintf("generic_album:%d:%d", kind, subtype),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	return out, join.Table, nil
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

func sqliteMediaType(kind int64) string {
	switch kind {
	case 0:
		return "image"
	case 1:
		return "video"
	default:
		return fmt.Sprintf("kind:%d", kind)
	}
}

// sqliteResourceKind names the ZRESOURCETYPE codes we know; unknown codes stay
// empty here and remain available as sqlite_resource_type in the evidence.
func sqliteResourceKind(code int64) string {
	switch code {
	case 0:
		return "photo"
	case 1:
		return "video"
	default:
		return ""
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

func sqliteAvailability(local, remote bool) string {
	switch {
	case local:
		return "local"
	case remote:
		return "remote"
	default:
		return "unknown"
	}
}

func validLocation(latitude, longitude float64) bool {
	if latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return false
	}
	return latitude != 0 || longitude != 0
}

func coreDataTime(value sql.NullFloat64) string {
	if !value.Valid {
		return ""
	}
	return time.Unix(978307200+int64(value.Float64), 0).UTC().Format(time.RFC3339)
}
