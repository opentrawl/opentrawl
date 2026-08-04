package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	db, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer func() { _ = db.Close() }()
	return open(ctx, db, rowID)
}

// OpenWithStore opens a record from the runner-owned read-only Photos store.
func OpenWithStore(ctx context.Context, db *store.Store, rowID string) (OpenResult, error) {
	if err := validateReadStore(ctx, db); err != nil {
		return OpenResult{}, err
	}
	return open(ctx, db, rowID)
}

func open(ctx context.Context, db *store.Store, rowID string) (OpenResult, error) {
	rowID = AssetID(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("ref is required")
	}
	asset, err := oneRow(ctx, db.DB(), `
select id, media_type, printf('kind_subtype:%d', photos_sqlite_kind_subtype) as media_subtypes, creation_date, timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier,
       camera_make, camera_model, lens_model, focal_length_mm, focal_length_35mm, aperture, shutter_speed, iso,
       source_state, coalesce(first_missing_at, '') as first_missing_at, coalesce(source_deleted_at, '') as source_deleted_at,
       seen.source_fingerprint
from asset
join crawl_seen_asset seen on seen.asset_id=asset.id and seen.source_library_id=asset.source_library_id
where asset.id = ?
`, rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenResult{}, fmt.Errorf("asset not found: %s", rowID)
	}
	if err != nil {
		return OpenResult{}, err
	}
	resources, err := rows(ctx, db.DB(), `
select resource_type_projection as resource_type,
       uti_projection as uti,
       availability_projection as availability,
       original_filename, file_size, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type_projection, original_filename
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	locations, err := rows(ctx, db.DB(), `
select id, latitude, longitude, altitude, horizontal_accuracy
from location_observation
where asset_id = ?
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	albums, err := rows(ctx, db.DB(), `
select album_title, printf('generic_album:%d:%d', photos_sqlite_album_kind, photos_sqlite_album_subtype) as album_kind
from album_membership
where asset_id = ?
order by album_title, photos_sqlite_album_kind, photos_sqlite_album_subtype
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	result := newOpenResult(asset, resources, locations, albums)
	currentLocationEvidence, found, err := LoadCurrentPhotoLocationEvidence(ctx, db, PhotoAssetID(rowID))
	if err != nil {
		return OpenResult{}, err
	}
	if found {
		result.Mechanical.CaptureLocationEvidence = currentPhotoCaptureLocationEvidenceFromOutcome(currentLocationEvidence)
	}
	if outcomeDescription, found, err := openCurrentRenderedPhotoMediaOutcome(ctx, db, rowID); err != nil {
		return OpenResult{}, err
	} else if found {
		result.Mechanical.Flags = append(result.Mechanical.Flags, outcomeDescription)
	}
	if rowString(asset, "media_type") != string(PhotoMediaKindImage) {
		result.Mechanical.Flags = append(result.Mechanical.Flags, "This Photos item is not a still image.")
	}
	return result, nil
}

func openCurrentRenderedPhotoMediaOutcome(ctx context.Context, db *store.Store, assetID string) (string, bool, error) {
	outcome, found, err := LoadCurrentRenderedPhotoMediaOutcome(ctx, db, PhotoAssetID(assetID))
	if err != nil || !found || outcome.GetUnavailable() == nil {
		return "", false, err
	}
	description := strings.TrimSpace(outcome.GetUnavailable().GetReason().GetHumanDescription())
	return description, description != "", nil
}

func compactOpenText(values []string) []string {
	compacted := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			compacted = append(compacted, value)
		}
	}
	return compacted
}
