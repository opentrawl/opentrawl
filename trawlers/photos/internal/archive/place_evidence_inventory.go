package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type PlaceEvidenceInventory struct {
	SourceLibraryID string
	Snapshot        PlaceEvidenceSnapshotReceipt
	Assets          []PlaceEvidenceInventoryAsset
}

type PlaceEvidenceSnapshotReceipt struct {
	ID                       string `json:"id"`
	CompletedAt              string `json:"completed_at"`
	CompletenessState        string `json:"completeness_state"`
	CompletenessEvidenceJSON string `json:"completeness_evidence_json"`
}

type PlaceEvidenceInventoryAsset struct {
	AssetID      string
	CreationDate string
	Coordinate   *PlaceEvidenceCoordinate
}

type PlaceEvidenceCoordinate struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type PlaceEvidenceSnapshotIncompleteError struct {
	SourceLibraryID string
}

func (e *PlaceEvidenceSnapshotIncompleteError) Error() string {
	return "source library has no complete Photos snapshot"
}

func ReadPlaceEvidenceInventory(ctx context.Context, archivePath, sourceLibraryID string) (PlaceEvidenceInventory, error) {
	archivePath = strings.TrimSpace(archivePath)
	sourceLibraryID = strings.TrimSpace(sourceLibraryID)
	if archivePath == "" {
		return PlaceEvidenceInventory{}, errors.New("Photos archive path is required")
	}
	if sourceLibraryID == "" {
		return PlaceEvidenceInventory{}, errors.New("source library ID is required")
	}
	db, err := store.OpenReadOnly(ctx, archivePath)
	if err != nil {
		return PlaceEvidenceInventory{}, err
	}
	defer func() { _ = db.Close() }()

	schemaVersion, err := db.SchemaVersion(ctx)
	if err != nil {
		return PlaceEvidenceInventory{}, fmt.Errorf("read Photos archive schema version: %w", err)
	}
	if schemaVersion != SchemaVersion {
		return PlaceEvidenceInventory{}, fmt.Errorf("Photos archive schema is %d, want %d", schemaVersion, SchemaVersion)
	}
	var sourceCount int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from source_library where id = ?`, sourceLibraryID).Scan(&sourceCount); err != nil {
		return PlaceEvidenceInventory{}, fmt.Errorf("read source library: %w", err)
	}
	if sourceCount != 1 {
		return PlaceEvidenceInventory{}, errors.New("source library is missing or ambiguous")
	}

	inventory := PlaceEvidenceInventory{SourceLibraryID: sourceLibraryID}
	err = db.DB().QueryRowContext(ctx, `
select id, completed_at, completeness_state, completeness_evidence_json
from crawl_snapshot
where source_library_id = ? and completeness_state = 'complete'
order by completed_at desc, id desc
limit 1
`, sourceLibraryID).Scan(
		&inventory.Snapshot.ID,
		&inventory.Snapshot.CompletedAt,
		&inventory.Snapshot.CompletenessState,
		&inventory.Snapshot.CompletenessEvidenceJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaceEvidenceInventory{}, &PlaceEvidenceSnapshotIncompleteError{SourceLibraryID: sourceLibraryID}
	}
	if err != nil {
		return PlaceEvidenceInventory{}, fmt.Errorf("select latest complete Photos snapshot: %w", err)
	}

	rows, err := db.DB().QueryContext(ctx, `
select a.id, a.creation_date, location.latitude, location.longitude
from asset a
join crawl_seen_asset seen
  on seen.source_library_id = a.source_library_id and seen.asset_id = a.id
left join location_observation location
  on location.id = (
    select first_location.id
    from location_observation first_location
    where first_location.asset_id = a.id
    order by first_location.id
    limit 1
  )
where a.source_library_id = ?
  and a.source_state = 'current'
  and a.media_type = 'image'
  and seen.last_seen_snapshot_id = ?
order by a.id
`, sourceLibraryID, inventory.Snapshot.ID)
	if err != nil {
		return PlaceEvidenceInventory{}, fmt.Errorf("select current Photos place inventory: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var asset PlaceEvidenceInventoryAsset
		var latitude, longitude sql.NullFloat64
		if err := rows.Scan(&asset.AssetID, &asset.CreationDate, &latitude, &longitude); err != nil {
			return PlaceEvidenceInventory{}, fmt.Errorf("scan current Photos place inventory: %w", err)
		}
		if latitude.Valid && longitude.Valid {
			asset.Coordinate = &PlaceEvidenceCoordinate{Latitude: latitude.Float64, Longitude: longitude.Float64}
		}
		inventory.Assets = append(inventory.Assets, asset)
	}
	if err := rows.Err(); err != nil {
		return PlaceEvidenceInventory{}, fmt.Errorf("read current Photos place inventory: %w", err)
	}
	return inventory, nil
}
