package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	sourcewire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/source"
	"github.com/opentrawl/opentrawl/trawlkit"
	crawlconfig "github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/proto"
)

const sourceAssetImportBatchSize = 256

type UpdateOptions struct {
	LibraryPath    string
	Provider       photos.Provider
	Now            func() time.Time
	ReportProgress func(photos.SnapshotProgress)
}

type UpdateResult struct {
	Database              string                           `json:"database"`
	Provider              photos.SnapshotProvider          `json:"provider"`
	SnapshotID            string                           `json:"snapshot_id"`
	SourceLibraryID       string                           `json:"source_library_id"`
	SnapshotCompleteness  photos.SnapshotCompletenessState `json:"snapshot_completeness"`
	AssetsSeen            int                              `json:"assets_seen"`
	AssetsNew             int                              `json:"assets_new"`
	AssetsChanged         int                              `json:"assets_changed"`
	AssetsUnchanged       int                              `json:"assets_unchanged"`
	ResourcesSeen         int                              `json:"resources_seen"`
	AlbumMembershipsSeen  int                              `json:"album_memberships_seen"`
	LocationsSeen         int                              `json:"locations_seen"`
	PreviouslySeenMissing int                              `json:"previously_seen_missing"`
	Duration              time.Duration                    `json:"-"`
}

func Update(ctx context.Context, paths Paths, opts UpdateOptions) (UpdateResult, error) {
	db, err := openArchive(ctx, paths.Database)
	if err != nil {
		return UpdateResult{}, err
	}
	defer func() { _ = db.Close() }()
	return UpdateWithStore(ctx, db, paths, opts)
}

func UpdateWithStore(ctx context.Context, db *store.Store, paths Paths, opts UpdateOptions) (UpdateResult, error) {
	if db == nil {
		return UpdateResult{}, errors.New("archive store is not open")
	}
	if err := prepareStore(ctx, db); err != nil {
		return UpdateResult{}, err
	}
	if opts.Provider == nil {
		return UpdateResult{}, errors.New("photos provider is required")
	}
	libraryPath := crawlconfig.ExpandHome(strings.TrimSpace(opts.LibraryPath))
	if libraryPath == "" {
		return UpdateResult{}, errors.New("library path is required")
	}
	absLibraryPath, err := filepath.Abs(libraryPath)
	if err != nil {
		return UpdateResult{}, err
	}
	if strings.TrimSpace(paths.CacheDir) == "" {
		return UpdateResult{}, errors.New("Photos archive cache directory is required")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	startedAt := now().UTC()
	sourceSnapshot, err := opts.Provider.OpenSnapshot(ctx, photos.SnapshotRequest{
		LibraryPath:    absLibraryPath,
		WorkingRoot:    filepath.Join(paths.CacheDir, "source-snapshots"),
		ReportProgress: opts.ReportProgress,
	})
	if err != nil {
		return UpdateResult{}, err
	}
	defer func() { _ = sourceSnapshot.Close() }()

	description := sourceSnapshot.Description()
	sourceID, err := photos.SourceLibraryID(description.LibraryDatabaseUUID)
	if err != nil {
		return UpdateResult{}, fmt.Errorf("identify Photos source library: %w", err)
	}
	snapshotID := stableID("crawl_snapshot", sourceID, startedAt.Format(time.RFC3339Nano), absLibraryPath)
	importer := updateImporter{
		ctx:            ctx,
		database:       db,
		description:    description,
		libraryPath:    absLibraryPath,
		sourceID:       sourceID,
		snapshotID:     snapshotID,
		startedAt:      startedAt,
		completedAt:    startedAt,
		reportProgress: opts.ReportProgress,
		result: UpdateResult{
			Provider:             description.Provider,
			SnapshotID:           snapshotID,
			SourceLibraryID:      sourceID,
			SnapshotCompleteness: photos.SnapshotPartial,
		},
	}
	if err := importer.begin(); err != nil {
		return UpdateResult{}, err
	}
	receipt, err := sourceSnapshot.ReadAssetBatches(ctx, sourceAssetImportBatchSize, importer.importBatch)
	if err != nil {
		return importer.result, errors.Join(err, importer.discardStagedSourceSnapshot())
	}
	if err := receipt.Completeness.Validate(); err != nil {
		return importer.result, errors.Join(
			fmt.Errorf("validate snapshot completeness: %w", err),
			importer.discardStagedSourceSnapshot(),
		)
	}
	importer.completedAt = now().UTC()
	importer.result.Duration = importer.completedAt.Sub(importer.startedAt)
	if !receipt.Completeness.Complete() {
		return importer.result, errors.Join(
			&SnapshotIncompleteError{State: string(receipt.Completeness.State)},
			importer.recordReceipt(receipt),
			importer.discardStagedSourceSnapshot(),
		)
	}
	if err := importer.finish(receipt); err != nil {
		return importer.result, errors.Join(err, importer.discardStagedSourceSnapshot())
	}
	importer.result.Database = paths.Database
	return importer.result, nil
}

type updateImporter struct {
	ctx            context.Context
	database       *store.Store
	description    photos.SnapshotDescription
	libraryPath    string
	sourceID       string
	snapshotID     string
	startedAt      time.Time
	completedAt    time.Time
	stmts          *crawlStatements
	reportProgress func(photos.SnapshotProgress)
	result         UpdateResult
}

func (importer *updateImporter) begin() error {
	return importer.database.WithTx(importer.ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(importer.ctx, `
insert into source_library(id, photos_library_database_uuid, configured_library_path, snapshot_path, snapshot_created_at)
values (?, ?, ?, null, null)
on conflict(id) do nothing
`, importer.sourceID, importer.description.LibraryDatabaseUUID, importer.libraryPath); err != nil {
			return fmt.Errorf("upsert source library: %w", err)
		}
		if _, err := tx.ExecContext(importer.ctx, `delete from crawl_staged_asset where source_library_id = ?`, importer.sourceID); err != nil {
			return fmt.Errorf("discard previous staged Photos source assets: %w", err)
		}
		_, err := tx.ExecContext(importer.ctx, `
insert into crawl_snapshot(
  id, source_library_id, started_at, completed_at, provider,
  expected_active_asset_count, expected_unique_asset_identifier_count,
  database_snapshot_file_count, database_snapshot_bytes, album_join_table,
  asset_count, resource_count, album_membership_count, location_count,
  completeness_state, database_copy_completed, resource_queries_completed, album_queries_completed, asset_query_completed
)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, ?, 1, 0, 0, 0)
`, importer.snapshotID, importer.sourceID, importer.startedAt.Format(time.RFC3339Nano), importer.startedAt.Format(time.RFC3339Nano), importer.description.Provider,
			importer.description.ExpectedActiveAssetCount, importer.description.ExpectedUniqueAssetIdentifierCount,
			importer.description.DatabaseSnapshotFileCount, importer.description.DatabaseSnapshotBytes, importer.description.AlbumJoinTable,
			photos.SnapshotPartial)
		if err != nil {
			return fmt.Errorf("insert Photos source snapshot receipt: %w", err)
		}
		return nil
	})
}

func (importer *updateImporter) importBatch(assets []photos.Asset) error {
	if len(assets) == 0 {
		return nil
	}
	resultBeforeBatch := importer.result
	err := importer.database.WithTx(importer.ctx, func(tx *sql.Tx) error {
		for _, asset := range assets {
			if strings.TrimSpace(asset.LocalIdentifier) == "" {
				return errors.New("Photos source asset local identifier is required")
			}
			if err := importer.stageAsset(tx, asset); err != nil {
				return err
			}
		}
		_, updateProgressError := tx.ExecContext(importer.ctx, `
update crawl_snapshot
set asset_count = ?, resource_count = ?, album_membership_count = ?, location_count = ?
where id = ?
`, importer.result.AssetsSeen, importer.result.ResourcesSeen, importer.result.AlbumMembershipsSeen, importer.result.LocationsSeen, importer.snapshotID)
		return updateProgressError
	})
	if err != nil {
		importer.result = resultBeforeBatch
	}
	return err
}

func (importer *updateImporter) stageAsset(tx *sql.Tx, asset photos.Asset) error {
	assetID := stableID("asset", importer.sourceID, asset.LocalIdentifier)
	fingerprint, err := assetFingerprint(asset)
	if err != nil {
		return err
	}
	previousFingerprint, seenBefore, err := previousAssetFingerprintFromTransaction(importer.ctx, tx, importer.sourceID, assetID)
	if err != nil {
		return err
	}
	importer.result.AssetsSeen++
	importer.result.ResourcesSeen += len(asset.Resources)
	importer.result.AlbumMembershipsSeen += len(asset.Albums)
	if asset.Location != nil {
		importer.result.LocationsSeen++
	}
	switch {
	case !seenBefore:
		importer.result.AssetsNew++
	case previousFingerprint != fingerprint:
		importer.result.AssetsChanged++
	default:
		importer.result.AssetsUnchanged++
	}
	var encodedSourceAsset []byte
	if !seenBefore || previousFingerprint != fingerprint {
		encodedSourceAsset, err = proto.MarshalOptions{Deterministic: true}.Marshal(sourceAssetProto(asset))
		if err != nil {
			return fmt.Errorf("marshal staged Photos source asset: %w", err)
		}
	}
	if _, err := tx.ExecContext(importer.ctx, `
insert into crawl_staged_asset(snapshot_id, source_library_id, asset_id, source_fingerprint, source_asset_proto)
values (?, ?, ?, ?, ?)
`, importer.snapshotID, importer.sourceID, assetID, fingerprint, encodedSourceAsset); err != nil {
		return fmt.Errorf("stage Photos source asset: %w", err)
	}
	return nil
}

func (importer *updateImporter) publishChangedStagedAsset(tx *sql.Tx, assetID string, asset photos.Asset) error {
	_, seenBefore, err := importer.previousAssetFingerprint(importer.ctx, importer.sourceID, assetID)
	if err != nil {
		return err
	}
	if err := importer.upsertAsset(importer.ctx, tx, assetID, seenBefore, asset); err != nil {
		return err
	}
	return nil
}

func (importer *updateImporter) recordReceipt(receipt photos.SnapshotReceipt) error {
	return importer.database.WithTx(importer.ctx, func(tx *sql.Tx) error {
		return importer.updateReceipt(importer.ctx, tx, receipt)
	})
}

func (importer *updateImporter) finish(receipt photos.SnapshotReceipt) error {
	if importer.reportProgress != nil {
		importer.reportProgress(photos.SnapshotProgress{
			Phase:              photos.SnapshotProgressPublishingAssets,
			ExpectedAssetCount: importer.result.AssetsNew + importer.result.AssetsChanged,
		})
	}
	return importer.database.WithTx(importer.ctx, func(tx *sql.Tx) error {
		statements, err := prepareCrawlStatements(importer.ctx, tx)
		if err != nil {
			return err
		}
		defer statements.close()
		importer.stmts = statements
		if err := importer.publishStagedAssets(tx); err != nil {
			return err
		}
		if err := importer.publishStagedAssetObservations(tx); err != nil {
			return err
		}
		if err := markStagedAssetsPresent(importer.ctx, tx, importer.sourceID, importer.snapshotID); err != nil {
			return err
		}
		missing, err := markMissingAssetsDeleted(importer.ctx, tx, importer.sourceID, importer.snapshotID, importer.completedAt)
		if err != nil {
			return err
		}
		importer.result.PreviouslySeenMissing = missing
		if err := importer.updateReceipt(importer.ctx, tx, receipt); err != nil {
			return err
		}
		shortReferences, err := readFinalPhotoShortReferences(importer.ctx, tx)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(importer.ctx, `
update source_library
set configured_library_path = ?, snapshot_path = ?, snapshot_created_at = ?
where id = ?
`, importer.libraryPath, "sqlite:crawl_snapshot/"+importer.snapshotID, importer.completedAt.Format(time.RFC3339Nano), importer.sourceID); err != nil {
			return fmt.Errorf("publish Photos source library snapshot: %w", err)
		}
		cursor := state.NewCursor(tx)
		if err := cursor.Set(importer.ctx, string(importer.description.Provider), "source_library", importer.sourceID, importer.snapshotID); err != nil {
			return err
		}
		if err := trawlkit.ReplaceShortReferencesForCompleteArchiveRecordSnapshotUsingCallerOwnedSQLTransaction(importer.ctx, tx, shortReferences); err != nil {
			return fmt.Errorf("publish Photos short references: %w", err)
		}
		if _, err := tx.ExecContext(importer.ctx, `delete from crawl_staged_asset where snapshot_id = ?`, importer.snapshotID); err != nil {
			return fmt.Errorf("remove published staged Photos source assets: %w", err)
		}
		return nil
	})
}

func (importer *updateImporter) publishStagedAssets(tx *sql.Tx) error {
	lastAssetID := ""
	publishedAssets := 0
	for {
		rows, err := tx.QueryContext(importer.ctx, `
select asset_id, source_asset_proto
from crawl_staged_asset
where snapshot_id = ? and asset_id > ? and source_asset_proto is not null
order by asset_id
limit ?
`, importer.snapshotID, lastAssetID, sourceAssetImportBatchSize)
		if err != nil {
			return fmt.Errorf("read staged Photos source assets: %w", err)
		}
		stagedAssets := make([]stagedSourceAsset, 0, sourceAssetImportBatchSize)
		for rows.Next() {
			var stagedAsset stagedSourceAsset
			if err := rows.Scan(&stagedAsset.assetID, &stagedAsset.encodedSourceAsset); err != nil {
				_ = rows.Close()
				return fmt.Errorf("read staged Photos source asset: %w", err)
			}
			stagedAssets = append(stagedAssets, stagedAsset)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close staged Photos source assets: %w", err)
		}
		if len(stagedAssets) == 0 {
			return nil
		}
		for _, stagedAsset := range stagedAssets {
			var sourceAsset sourcewire.SourceAsset
			if err := proto.Unmarshal(stagedAsset.encodedSourceAsset, &sourceAsset); err != nil {
				return fmt.Errorf("decode staged Photos source asset: %w", err)
			}
			asset := photosAssetFromSourceAssetProto(&sourceAsset)
			if stableID("asset", importer.sourceID, asset.LocalIdentifier) != stagedAsset.assetID {
				return errors.New("staged Photos source asset identity does not match its typed content")
			}
			if err := importer.publishChangedStagedAsset(tx, stagedAsset.assetID, asset); err != nil {
				return err
			}
			publishedAssets++
		}
		if importer.reportProgress != nil {
			importer.reportProgress(photos.SnapshotProgress{
				Phase:               photos.SnapshotProgressPublishingAssets,
				CompletedAssetCount: publishedAssets,
				ExpectedAssetCount:  importer.result.AssetsNew + importer.result.AssetsChanged,
			})
		}
		lastAssetID = stagedAssets[len(stagedAssets)-1].assetID
	}
}

func (importer *updateImporter) publishStagedAssetObservations(tx *sql.Tx) error {
	_, err := tx.ExecContext(importer.ctx, `
insert into crawl_seen_asset(source_library_id, asset_id, first_seen_snapshot_id, last_seen_snapshot_id, source_fingerprint, last_seen_at)
select source_library_id, asset_id, snapshot_id, snapshot_id, source_fingerprint, ?
from crawl_staged_asset
where snapshot_id = ?
on conflict(source_library_id, asset_id) do update set
  last_seen_snapshot_id = excluded.last_seen_snapshot_id,
  source_fingerprint = excluded.source_fingerprint,
  last_seen_at = excluded.last_seen_at
`, importer.completedAt.Format(time.RFC3339Nano), importer.snapshotID)
	if err != nil {
		return fmt.Errorf("publish Photos source asset observations: %w", err)
	}
	return nil
}

type stagedSourceAsset struct {
	assetID            string
	encodedSourceAsset []byte
}

func (importer *updateImporter) discardStagedSourceSnapshot() error {
	cleanupContext := context.WithoutCancel(importer.ctx)
	return importer.database.WithTx(cleanupContext, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(cleanupContext, `delete from crawl_staged_asset where snapshot_id = ?`, importer.snapshotID)
		if err != nil {
			return fmt.Errorf("discard staged Photos source snapshot: %w", err)
		}
		return nil
	})
}

func (importer *updateImporter) updateReceipt(ctx context.Context, tx *sql.Tx, receipt photos.SnapshotReceipt) error {
	_, err := tx.ExecContext(ctx, `
update crawl_snapshot
set completed_at = ?, asset_count = ?, resource_count = ?, album_membership_count = ?, location_count = ?,
    completeness_state = ?, database_copy_completed = ?, resource_queries_completed = ?, album_queries_completed = ?, asset_query_completed = ?
where id = ?
`, importer.completedAt.Format(time.RFC3339Nano), receipt.AssetCount, receipt.ResourceCount, receipt.AlbumMembershipCount, receipt.LocationCount,
		receipt.Completeness.State, boolInt(receipt.Completeness.DatabaseCopyCompleted), boolInt(receipt.Completeness.ResourceQueriesCompleted), boolInt(receipt.Completeness.AlbumQueriesCompleted), boolInt(receipt.Completeness.AssetQueryCompleted), importer.snapshotID)
	if err != nil {
		return fmt.Errorf("update Photos source snapshot receipt: %w", err)
	}
	importer.result.SnapshotCompleteness = receipt.Completeness.State
	return nil
}

func readFinalPhotoShortReferences(ctx context.Context, transaction *sql.Tx) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	rows, err := transaction.QueryContext(ctx, `select id from asset order by id`)
	if err != nil {
		return nil, fmt.Errorf("read final Photos archive assets for short reference assignment: %w", err)
	}
	defer func() { _ = rows.Close() }()
	candidates := []trawlkit.ShortReferenceAssignmentCandidate{}
	for rows.Next() {
		var assetID string
		if err := rows.Scan(&assetID); err != nil {
			return nil, err
		}
		candidates = append(candidates, trawlkit.ShortReferenceAssignmentCandidate{
			StableRecordReferenceUsedForShortReferenceAssignment: trawlkit.NewCanonicalArchiveRecordReference(AssetRef(assetID)),
		})
	}
	return candidates, rows.Err()
}

func (importer *updateImporter) upsertAsset(ctx context.Context, tx *sql.Tx, assetID string, seenBefore bool, asset photos.Asset) error {
	previousCaptureLocationInput, err := loadStoredSourceCaptureLocationInput(ctx, tx, assetID)
	if err != nil {
		return err
	}
	camera := assetCameraValues(asset.Camera)
	if _, err := importer.stmts.asset.ExecContext(ctx,
		assetID, asset.PhotosSQLiteAssetPrimaryKey, asset.LocalIdentifier, asset.MediaType, asset.PhotosSQLiteKind, asset.PhotosSQLiteKindSubtype,
		asset.CreationDate, asset.ModificationDate, asset.AddedDate, asset.TimezoneName,
		asset.Width, asset.Height, asset.DurationSeconds, boolInt(asset.Favorite), boolInt(asset.Hidden), asset.BurstIdentifier, boolInt(asset.RepresentsBurst),
		camera.make, camera.model, camera.lensModel, nullableFloat(camera.focalLengthMM), nullableFloat(camera.focalLength35MM), nullableFloat(camera.aperture), nullableFloat(camera.shutterSpeed), nullableInt(camera.iso),
		asset.UniformTypeIdentifier, asset.Filename, asset.OriginalFilename, importer.sourceID,
	); err != nil {
		return fmt.Errorf("upsert asset %s: %w", assetID, err)
	}
	if seenBefore {
		if previousCaptureLocationInput != sourceCaptureLocationInputFromAsset(asset) {
			if err := invalidateAssetLocationCompositionForChangedCaptureInput(ctx, tx, assetID); err != nil {
				return err
			}
		}
		if err := deleteAssetSourceChildRowsBeforeReplacement(ctx, tx, assetID); err != nil {
			return err
		}
	}
	for _, resource := range asset.Resources {
		if err := importer.insertResource(ctx, assetID, resource); err != nil {
			return err
		}
	}
	for _, album := range asset.Albums {
		if err := importer.insertAlbum(ctx, assetID, album); err != nil {
			return err
		}
	}
	if asset.Location != nil {
		if err := importer.insertLocation(ctx, assetID, asset.LocalIdentifier, *asset.Location); err != nil {
			return err
		}
	}
	if err := importer.insertFTS(ctx, tx, assetID, asset); err != nil {
		return err
	}
	return nil
}

func (importer *updateImporter) previousAssetFingerprint(ctx context.Context, sourceID, assetID string) (string, bool, error) {
	var fingerprint string
	err := importer.stmts.previousFingerprint.QueryRowContext(ctx, sourceID, assetID).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read previous asset state: %w", err)
	}
	return fingerprint, true, nil
}

func previousAssetFingerprintFromTransaction(ctx context.Context, tx *sql.Tx, sourceID, assetID string) (string, bool, error) {
	var fingerprint string
	err := tx.QueryRowContext(ctx, `
select source_fingerprint from crawl_seen_asset
where source_library_id = ? and asset_id = ?
`, sourceID, assetID).Scan(&fingerprint)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read previous asset state while staging Photos source snapshot: %w", err)
	}
	return fingerprint, true, nil
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
