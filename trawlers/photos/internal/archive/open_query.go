package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	db, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer func() { _ = db.Close() }()
	return open(ctx, db, rowID, "")
}

// OpenWithStore opens a record from the runner-owned read-only Photos store.
func OpenWithStore(ctx context.Context, db *store.Store, rowID string) (OpenResult, error) {
	return OpenWithStoreFocused(ctx, db, rowID, "")
}

func OpenWithStoreFocused(ctx context.Context, db *store.Store, rowID, anchorID string) (OpenResult, error) {
	if err := validateReadStore(ctx, db); err != nil {
		return OpenResult{}, err
	}
	return open(ctx, db, rowID, anchorID)
}

func open(ctx context.Context, db *store.Store, rowID, anchorID string) (OpenResult, error) {
	rowID = normalizeRef(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("ref is required")
	}
	asset, err := oneRow(ctx, db.DB(), `
select id, media_type, creation_date, timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier,
       camera_make, camera_model, lens_model, focal_length_mm, focal_length_35mm, aperture, shutter_speed, iso,
       source_state, coalesce(first_missing_at, '') as first_missing_at, coalesce(source_deleted_at, '') as source_deleted_at
from asset
where id = ?
`, rowID)
	if errors.Is(err, sql.ErrNoRows) {
		return OpenResult{}, fmt.Errorf("asset not found: %s", rowID)
	}
	if err != nil {
		return OpenResult{}, err
	}
	resources, err := rows(ctx, db.DB(), `
select resource_type, uti, original_filename, file_size, available_locally, needs_download
from asset_resource
where asset_id = ?
order by resource_type, original_filename
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
select album_title, album_kind
from album_membership
where asset_id = ?
order by album_title, album_kind
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	modelObservations, err := rows(ctx, db.DB(), `
select observation_type, value_text, value_json, model_id, prompt_version,
       coalesce(stale_since, '') as stale_since,
       coalesce(stale_reason, '') as stale_reason
from model_observation
where asset_id = ?
  and observation_type in ('`+modelObservationCardSummary+`', '`+modelObservationCardDescription+`', '`+modelObservationCardOCR+`', '`+modelObservationCardUncertainty+`')
  and superseded_at is null
order by case observation_type
  when '`+modelObservationCardSummary+`' then 1
  when '`+modelObservationCardDescription+`' then 2
  when '`+modelObservationCardOCR+`' then 3
  when '`+modelObservationCardUncertainty+`' then 4
  else 5
end, id
`, rowID)
	if err != nil {
		return OpenResult{}, err
	}
	placeObservations := []map[string]any{}
	if ok, err := tableExists(ctx, db.DB(), "place_observation"); err != nil {
		return OpenResult{}, err
	} else if ok {
		placeObservations, err = rows(ctx, db.DB(), `
select observation_type, value_text, value_json, provider, cache_status, tier, distance_meters,
       coalesce(stale_since, '') as stale_since,
       coalesce(stale_reason, '') as stale_reason
from place_observation
where asset_id = ?
  and superseded_at is null
order by case observation_type
  when 'known_place' then 1
  when 'venue' then 2
  when 'address' then 3
  else 4
end, distance_meters, id
`, rowID)
		if err != nil {
			return OpenResult{}, err
		}
	}
	metadataObservations, err := rows(ctx, db.DB(), `
select id, label
from metadata_observation
where asset_id = ?
order by observation_type, label, id
limit ?
`, rowID, maximumOpenSignals+1)
	if err != nil {
		return OpenResult{}, err
	}
	truncated := len(metadataObservations) > maximumOpenSignals
	if len(metadataObservations) > maximumOpenSignals {
		metadataObservations = metadataObservations[:maximumOpenSignals]
	}
	if anchorID != "" && !hasMetadataAnchor(metadataObservations, anchorID) {
		metadataID, ok := metadataIDForAnchor(anchorID)
		if !ok {
			return OpenResult{}, fmt.Errorf("invalid requested metadata anchor: %s", anchorID)
		}
		focused, err := oneRow(ctx, db.DB(), `
select id, label
from metadata_observation
where asset_id = ? and id = ?
`, rowID, metadataID)
		if errors.Is(err, sql.ErrNoRows) {
			return OpenResult{}, fmt.Errorf("requested metadata anchor not found: %s", anchorID)
		}
		if err != nil {
			return OpenResult{}, err
		}
		if len(metadataObservations) == maximumOpenSignals {
			metadataObservations = append(metadataObservations[:maximumOpenSignals-1], focused)
		} else {
			metadataObservations = append(metadataObservations, focused)
		}
	}
	result := newOpenResult(asset, resources, locations, albums, modelObservations, placeObservations, metadataObservations)
	result.Mechanical.SignalsTruncated = truncated
	return result, nil
}

func hasMetadataAnchor(rows []map[string]any, anchorID string) bool {
	for _, row := range rows {
		if metadataAnchorID(rowString(row, "id")) == anchorID {
			return true
		}
	}
	return false
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx, `
select count(*)
from sqlite_master
where type = 'table' and name = ?
`, name).Scan(&count)
	return count > 0, err
}
