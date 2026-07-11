package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type DevelopmentCacheSelection struct {
	SourceLibraryID string
	SnapshotID      string
	Assets          []DevelopmentCacheAsset
}

type DevelopmentCacheAsset struct {
	AssetID  string
	CacheKey string
	Request  photos.OriginalRequest
}

// SelectDevelopmentCacheAssets reads one explicit source from its latest
// complete snapshot. Later partial and failed audit snapshots cannot add work.
func SelectDevelopmentCacheAssets(ctx context.Context, databasePath, sourceLibraryID string) (DevelopmentCacheSelection, error) {
	sourceLibraryID = strings.TrimSpace(sourceLibraryID)
	if strings.TrimSpace(databasePath) == "" {
		return DevelopmentCacheSelection{}, errors.New("Photos archive path is required")
	}
	if sourceLibraryID == "" {
		return DevelopmentCacheSelection{}, errors.New("source library ID is required")
	}
	db, err := store.OpenReadOnly(ctx, databasePath)
	if err != nil {
		return DevelopmentCacheSelection{}, err
	}
	defer func() { _ = db.Close() }()

	selection := DevelopmentCacheSelection{SourceLibraryID: sourceLibraryID}
	err = db.DB().QueryRowContext(ctx, `
select id
from crawl_snapshot
where source_library_id = ? and completeness_state = 'complete'
order by completed_at desc, id desc
limit 1
`, sourceLibraryID).Scan(&selection.SnapshotID)
	if errors.Is(err, sql.ErrNoRows) {
		return DevelopmentCacheSelection{}, errors.New("source has no complete Photos snapshot")
	}
	if err != nil {
		return DevelopmentCacheSelection{}, fmt.Errorf("select latest complete Photos snapshot: %w", err)
	}

	rows, err := db.DB().QueryContext(ctx, `
select a.id, a.local_identifier, a.creation_date, a.modification_date, a.width, a.height,
       r.id, r.resource_type, r.uti, r.original_filename, r.local_path, r.file_size
from asset a
join crawl_seen_asset seen
  on seen.source_library_id = a.source_library_id and seen.asset_id = a.id
join asset_resource r on r.asset_id = a.id
where a.source_library_id = ?
  and a.source_state = 'current'
  and a.media_type = 'image'
  and seen.last_seen_snapshot_id = ?
  and lower(trim(r.resource_type)) in ('photo', 'local_original')
order by a.id, r.id
`, sourceLibraryID, selection.SnapshotID)
	if err != nil {
		return DevelopmentCacheSelection{}, fmt.Errorf("select current Photos originals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var current *DevelopmentCacheAsset
	var currentAssetID string
	for rows.Next() {
		var assetID, localIdentifier, creationDate, modificationDate string
		var width, height int64
		var resourceID, resourceType, uti, originalFilename, localPath string
		var fileSize int64
		if err := rows.Scan(
			&assetID, &localIdentifier, &creationDate, &modificationDate, &width, &height,
			&resourceID, &resourceType, &uti, &originalFilename, &localPath, &fileSize,
		); err != nil {
			return DevelopmentCacheSelection{}, fmt.Errorf("scan current Photos original: %w", err)
		}
		if assetID != currentAssetID {
			selection.Assets = append(selection.Assets, DevelopmentCacheAsset{
				AssetID: assetID,
				Request: photos.OriginalRequest{
					SourceLibraryID:  sourceLibraryID,
					ModificationDate: modificationDate,
					Query: photos.OriginalExportQuery{
						LocalIdentifier: localIdentifier,
						CreationDate:    creationDate,
						Width:           width,
						Height:          height,
					},
				},
			})
			current = &selection.Assets[len(selection.Assets)-1]
			currentAssetID = assetID
		}
		if strings.EqualFold(strings.TrimSpace(resourceType), "photo") && current.Request.Query.OriginalFilename == "" {
			current.Request.Query.OriginalFilename = originalFilename
			current.Request.Query.OriginalUTI = uti
		}
		if strings.EqualFold(strings.TrimSpace(resourceType), "local_original") && strings.TrimSpace(localPath) != "" {
			current.Request.PackageCandidates = append(current.Request.PackageCandidates, photos.LocalMediaCandidate{
				Path:  localPath,
				Class: "original",
				Size:  fileSize,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return DevelopmentCacheSelection{}, fmt.Errorf("read current Photos originals: %w", err)
	}

	selected := selection.Assets[:0]
	for _, asset := range selection.Assets {
		if asset.Request.Query.OriginalFilename == "" || (!photos.IsOriginalUTI(asset.Request.Query.OriginalUTI) && !photos.IsOriginalExtension(filepath.Ext(asset.Request.Query.OriginalFilename))) {
			continue
		}
		asset.CacheKey = filepath.Base(photos.OriginalCachePath("", asset.Request.SourceLibraryID, asset.Request.ModificationDate, asset.Request.Query))
		selected = append(selected, asset)
	}
	selection.Assets = selected
	return selection, nil
}
