package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/openclaw/crawlkit/store"
)

func Open(ctx context.Context, paths Paths, rowID string) (OpenResult, error) {
	rowID = normalizeRef(rowID)
	if rowID == "" {
		return OpenResult{}, errors.New("ref is required")
	}
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		return OpenResult{}, err
	}
	defer db.Close()

	asset, err := oneRow(ctx, db.DB(), `
select id, media_type, creation_date, timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier
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
select id, latitude, longitude, altitude, horizontal_accuracy, source, evidence_id
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
select observation_type, value_text, value_json, model_id, prompt_version, evidence_id
from model_observation
where asset_id = ?
  and observation_type in ('`+modelObservationCardSummary+`', '`+modelObservationCardDescription+`', '`+modelObservationCardOCR+`', '`+modelObservationCardUncertainty+`')
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
select observation_type, value_text, value_json, provider, cache_status, tier, distance_meters
from place_observation
where asset_id = ?
order by case observation_type
  when 'venue' then 1
  when 'address' then 2
  else 3
end, distance_meters, id
`, rowID)
		if err != nil {
			return OpenResult{}, err
		}
	}
	return newOpenResult(asset, resources, locations, albums, modelObservations, placeObservations), nil
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
