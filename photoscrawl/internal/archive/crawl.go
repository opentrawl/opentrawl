package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	crawlconfig "github.com/openclaw/crawlkit/config"
	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
)

type SyncOptions struct {
	LibraryPath string
	Provider    photos.Provider
	Now         func() time.Time
}

type SyncResult struct {
	Database              string `json:"database"`
	Provider              string `json:"provider"`
	SnapshotID            string `json:"snapshot_id"`
	SourceLibraryID       string `json:"source_library_id"`
	AssetsSeen            int    `json:"assets_seen"`
	AssetsNew             int    `json:"assets_new"`
	AssetsChanged         int    `json:"assets_changed"`
	AssetsUnchanged       int    `json:"assets_unchanged"`
	ResourcesSeen         int    `json:"resources_seen"`
	AlbumMembershipsSeen  int    `json:"album_memberships_seen"`
	LocationsSeen         int    `json:"locations_seen"`
	QueuedForClassify     int    `json:"queued_for_classify"`
	QueuedNeedsDownload   int    `json:"queued_needs_download"`
	PreviouslySeenMissing int    `json:"previously_seen_missing"`
}

func Sync(ctx context.Context, paths Paths, opts SyncOptions) (SyncResult, error) {
	if opts.Provider == nil {
		return SyncResult{}, errors.New("photos provider is required")
	}
	libraryPath := crawlconfig.ExpandHome(strings.TrimSpace(opts.LibraryPath))
	if libraryPath == "" {
		return SyncResult{}, errors.New("library path is required")
	}
	absLibraryPath, err := filepath.Abs(libraryPath)
	if err != nil {
		return SyncResult{}, err
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	startedAt := now().UTC()
	snapshot, err := opts.Provider.Snapshot(ctx, absLibraryPath)
	if err != nil {
		return SyncResult{}, err
	}
	completedAt := now().UTC()
	if snapshot.Provider == "" {
		snapshot.Provider = "unknown"
	}
	if snapshot.LibraryPath == "" {
		snapshot.LibraryPath = absLibraryPath
	}
	if err := photos.AttachLocalMediaPaths(&snapshot, absLibraryPath); err != nil {
		return SyncResult{}, fmt.Errorf("resolve local Photos media paths: %w", err)
	}

	db, err := store.Open(ctx, store.Options{
		Path:          paths.Database,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return SyncResult{}, err
	}
	defer func() { _ = db.Close() }()

	importer := syncImporter{
		ctx:         ctx,
		snapshot:    snapshot,
		libraryPath: absLibraryPath,
		startedAt:   startedAt,
		completedAt: completedAt,
	}
	if err := db.WithTx(ctx, importer.run); err != nil {
		return SyncResult{}, err
	}
	importer.result.Database = paths.Database
	return importer.result, nil
}

type syncImporter struct {
	ctx         context.Context
	snapshot    photos.LibrarySnapshot
	libraryPath string
	startedAt   time.Time
	completedAt time.Time
	stmts       *crawlStatements
	result      SyncResult
}

func (c *syncImporter) run(tx *sql.Tx) error {
	ctx := c.ctx
	sourceID := stableID("source_library", c.libraryPath)
	snapshotID := stableID("crawl_snapshot", sourceID, c.completedAt.Format(time.RFC3339Nano), c.sourceFingerprint())

	resourceCount, albumCount, locationCount := snapshotCounts(c.snapshot)
	metadataJSON, err := jsonText(map[string]any{
		"provider":             c.snapshot.Provider,
		"authorization_status": c.snapshot.AuthorizationStatus,
		"snapshot_metadata":    c.snapshot.Metadata,
	})
	if err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values (?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  library_path = excluded.library_path,
  snapshot_path = excluded.snapshot_path,
  snapshot_created_at = excluded.snapshot_created_at,
  photos_version = excluded.photos_version,
  metadata_json = excluded.metadata_json
`, sourceID, c.libraryPath, "sqlite:crawl_snapshot/"+snapshotID, c.completedAt.Format(time.RFC3339Nano), c.snapshot.PhotosVersion, metadataJSON); err != nil {
		return fmt.Errorf("upsert source library: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
insert into crawl_snapshot(id, source_library_id, started_at, completed_at, provider, asset_count, resource_count, album_membership_count, location_count, metadata_json)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, snapshotID, sourceID, c.startedAt.Format(time.RFC3339Nano), c.completedAt.Format(time.RFC3339Nano), c.snapshot.Provider, len(c.snapshot.Assets), resourceCount, albumCount, locationCount, metadataJSON); err != nil {
		return fmt.Errorf("insert sync snapshot: %w", err)
	}

	c.result = SyncResult{
		Provider:             c.snapshot.Provider,
		SnapshotID:           snapshotID,
		SourceLibraryID:      sourceID,
		AssetsSeen:           len(c.snapshot.Assets),
		ResourcesSeen:        resourceCount,
		AlbumMembershipsSeen: albumCount,
		LocationsSeen:        locationCount,
	}
	stmts, err := prepareCrawlStatements(ctx, tx)
	if err != nil {
		return err
	}
	defer stmts.close()
	c.stmts = stmts

	for _, asset := range c.snapshot.Assets {
		if strings.TrimSpace(asset.LocalIdentifier) == "" {
			continue
		}
		assetID := stableID("asset", sourceID, asset.LocalIdentifier)
		fingerprint, err := assetFingerprint(asset)
		if err != nil {
			return err
		}
		previousFingerprint, seenBefore, err := c.previousAssetFingerprint(ctx, sourceID, assetID)
		if err != nil {
			return err
		}
		switch {
		case !seenBefore:
			c.result.AssetsNew++
		case previousFingerprint != fingerprint:
			c.result.AssetsChanged++
		default:
			c.result.AssetsUnchanged++
			if err := c.upsertSeenAsset(ctx, sourceID, assetID, snapshotID, fingerprint); err != nil {
				return err
			}
			continue
		}
		if err := c.upsertAsset(ctx, tx, sourceID, snapshotID, assetID, fingerprint, seenBefore, asset); err != nil {
			return err
		}
	}

	var missing int
	if err := tx.QueryRowContext(ctx, `
select count(*) from crawl_seen_asset
where source_library_id = ? and last_seen_snapshot_id <> ?
`, sourceID, snapshotID).Scan(&missing); err != nil {
		return fmt.Errorf("count missing seen assets: %w", err)
	}
	c.result.PreviouslySeenMissing = missing

	cursor := state.NewCursor(tx)
	if err := cursor.Set(ctx, c.snapshot.Provider, "source_library", sourceID, snapshotID); err != nil {
		return err
	}
	if err := rebuildShortRefsInTx(ctx, tx); err != nil {
		return fmt.Errorf("rebuild short refs: %w", err)
	}

	return nil
}

func (c *syncImporter) upsertAsset(ctx context.Context, tx *sql.Tx, sourceID, snapshotID, assetID, fingerprint string, seenBefore bool, asset photos.Asset) error {
	metadataJSON, err := jsonText(asset.Metadata)
	if err != nil {
		return err
	}
	camera := assetCameraValues(asset.Camera)
	if _, err := c.stmts.asset.ExecContext(ctx, assetID, asset.LocalIdentifier, asset.MediaType, asset.MediaSubtypes, asset.CreationDate, asset.ModificationDate, asset.AddedDate, asset.TimezoneName, asset.Width, asset.Height, asset.DurationSeconds, boolInt(asset.Favorite), boolInt(asset.Hidden), asset.BurstIdentifier, boolInt(asset.RepresentsBurst), camera.make, camera.model, camera.lensModel, nullableFloat(camera.focalLengthMM), nullableFloat(camera.focalLength35MM), nullableFloat(camera.aperture), nullableFloat(camera.shutterSpeed), nullableInt(camera.iso), sourceID, metadataJSON); err != nil {
		return fmt.Errorf("upsert asset %s: %w", assetID, err)
	}

	if seenBefore {
		if err := resetAssetDerivedRows(ctx, tx, assetID); err != nil {
			return err
		}
	}
	for i, resource := range asset.Resources {
		if err := c.insertResource(ctx, assetID, i, resource); err != nil {
			return err
		}
	}
	for _, album := range asset.Albums {
		if err := c.insertAlbum(ctx, assetID, album); err != nil {
			return err
		}
	}
	if asset.Location != nil {
		if err := c.insertLocation(ctx, assetID, asset.LocalIdentifier, *asset.Location); err != nil {
			return err
		}
	}
	if err := c.insertFTS(ctx, tx, assetID, asset); err != nil {
		return err
	}
	if err := c.upsertClassifyQueue(ctx, tx, sourceID, assetID, asset); err != nil {
		return err
	}
	return c.upsertSeenAsset(ctx, sourceID, assetID, snapshotID, fingerprint)
}

func (c *syncImporter) previousAssetFingerprint(ctx context.Context, sourceID, assetID string) (string, bool, error) {
	var fingerprint string
	err := c.stmts.previousFingerprint.QueryRowContext(ctx, sourceID, assetID).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read previous asset state: %w", err)
	}
	return fingerprint, true, nil
}

func snapshotCounts(snapshot photos.LibrarySnapshot) (resources, albums, locations int) {
	for _, asset := range snapshot.Assets {
		resources += len(asset.Resources)
		albums += len(asset.Albums)
		if asset.Location != nil {
			locations++
		}
	}
	return resources, albums, locations
}

func assetFingerprint(asset photos.Asset) (string, error) {
	data, err := json.Marshal(asset)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (c *syncImporter) sourceFingerprint() string {
	hash := sha256.New()
	for _, asset := range c.snapshot.Assets {
		hash.Write([]byte(asset.LocalIdentifier))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func jsonText(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullableFloat(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

type assetCamera struct {
	make            string
	model           string
	lensModel       string
	focalLengthMM   *float64
	focalLength35MM *float64
	aperture        *float64
	shutterSpeed    *float64
	iso             *int64
}

func assetCameraValues(camera *photos.Camera) assetCamera {
	if camera == nil {
		return assetCamera{}
	}
	return assetCamera{
		make:            strings.TrimSpace(camera.Make),
		model:           strings.TrimSpace(camera.Model),
		lensModel:       strings.TrimSpace(camera.LensModel),
		focalLengthMM:   camera.FocalLengthMM,
		focalLength35MM: camera.FocalLength35MM,
		aperture:        camera.Aperture,
		shutterSpeed:    camera.ShutterSpeed,
		iso:             camera.ISO,
	}
}
