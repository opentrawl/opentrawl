package archive

import (
	"context"
	"database/sql"
)

type crawlStatements struct {
	previousFingerprint *sql.Stmt
	asset               *sql.Stmt
	resource            *sql.Stmt
	album               *sql.Stmt
	location            *sql.Stmt
	fts                 *sql.Stmt
	queue               *sql.Stmt
	seen                *sql.Stmt
}

func prepareCrawlStatements(ctx context.Context, tx *sql.Tx) (*crawlStatements, error) {
	stmts := &crawlStatements{}
	prepares := []struct {
		target **sql.Stmt
		query  string
	}{
		{&stmts.previousFingerprint, `
select source_fingerprint from crawl_seen_asset
where source_library_id = ? and asset_id = ?
`},
		{&stmts.asset, `
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst, camera_make, camera_model, lens_model, focal_length_mm, focal_length_35mm, aperture, shutter_speed, iso, source_library_id, metadata_json)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  local_identifier = excluded.local_identifier,
  media_type = excluded.media_type,
  media_subtypes = excluded.media_subtypes,
  creation_date = excluded.creation_date,
  modification_date = excluded.modification_date,
  added_date = excluded.added_date,
  timezone_name = excluded.timezone_name,
  width = excluded.width,
  height = excluded.height,
  duration_seconds = excluded.duration_seconds,
  favorite = excluded.favorite,
  hidden = excluded.hidden,
  burst_identifier = excluded.burst_identifier,
  represents_burst = excluded.represents_burst,
  camera_make = excluded.camera_make,
  camera_model = excluded.camera_model,
  lens_model = excluded.lens_model,
  focal_length_mm = excluded.focal_length_mm,
  focal_length_35mm = excluded.focal_length_35mm,
  aperture = excluded.aperture,
  shutter_speed = excluded.shutter_speed,
  iso = excluded.iso,
  source_library_id = excluded.source_library_id,
  metadata_json = excluded.metadata_json
`},
		{&stmts.resource, `
insert into asset_resource(id, asset_id, resource_type, uti, original_filename, local_path, file_size, sha256, available_locally, needs_download)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`},
		{&stmts.album, `
insert into album_membership(id, asset_id, album_id, album_title, album_kind)
values (?, ?, ?, ?, ?)
`},
		{&stmts.location, `
insert into location_observation(id, asset_id, latitude, longitude, altitude, horizontal_accuracy, source, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?)
`},
		{&stmts.fts, `insert into asset_fts(id, title, body) values (?, ?, ?)`},
		{&stmts.queue, `
insert into classification_queue(id, asset_id, source_library_id, state, reason, needs_download, updated_at)
values (?, ?, ?, ?, ?, ?, ?)
on conflict(asset_id) do update set
  source_library_id = excluded.source_library_id,
  state = case
    when classification_queue.state = 'failed_download' then classification_queue.state
    else excluded.state
  end,
  reason = case
    when classification_queue.state = 'failed_download' then classification_queue.reason
    else excluded.reason
  end,
  needs_download = excluded.needs_download,
  updated_at = case
    when classification_queue.state = 'failed_download' then classification_queue.updated_at
    else excluded.updated_at
  end
`},
		{&stmts.seen, `
insert into crawl_seen_asset(source_library_id, asset_id, first_seen_snapshot_id, last_seen_snapshot_id, source_fingerprint, last_seen_at)
values (?, ?, ?, ?, ?, ?)
on conflict(source_library_id, asset_id) do update set
  last_seen_snapshot_id = excluded.last_seen_snapshot_id,
  source_fingerprint = excluded.source_fingerprint,
  last_seen_at = excluded.last_seen_at
`},
	}
	for _, prepare := range prepares {
		stmt, err := tx.PrepareContext(ctx, prepare.query)
		if err != nil {
			stmts.close()
			return nil, err
		}
		*prepare.target = stmt
	}
	return stmts, nil
}

func (s *crawlStatements) close() {
	if s == nil {
		return
	}
	for _, stmt := range []*sql.Stmt{s.previousFingerprint, s.asset, s.resource, s.album, s.location, s.fts, s.queue, s.seen} {
		if stmt != nil {
			_ = stmt.Close()
		}
	}
}
