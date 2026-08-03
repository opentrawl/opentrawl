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
insert into asset(id, photos_sqlite_asset_primary_key, local_identifier, media_type, photos_sqlite_kind, photos_sqlite_kind_subtype, creation_date, modification_date, added_date, timezone_name, width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst, camera_make, camera_model, lens_model, focal_length_mm, focal_length_35mm, aperture, shutter_speed, iso, uniform_type_identifier, filename, original_filename, source_library_id)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  photos_sqlite_asset_primary_key = excluded.photos_sqlite_asset_primary_key,
  local_identifier = excluded.local_identifier,
  media_type = excluded.media_type,
  photos_sqlite_kind = excluded.photos_sqlite_kind,
  photos_sqlite_kind_subtype = excluded.photos_sqlite_kind_subtype,
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
  uniform_type_identifier = excluded.uniform_type_identifier,
  filename = excluded.filename,
  original_filename = excluded.original_filename,
  source_library_id = excluded.source_library_id
`},
		{&stmts.resource, `
insert into asset_resource(
  id, asset_id,
  photos_sqlite_resource_primary_key, photos_sqlite_resource_type, photos_sqlite_compact_uti,
  photos_sqlite_resource_version, photos_sqlite_local_availability, photos_sqlite_remote_availability,
  photos_sqlite_stable_hash, photos_sqlite_fingerprint,
  resource_type_projection, uti_projection, availability_projection,
  original_filename, file_size, available_locally, needs_download
)
values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`},
		{&stmts.album, `
insert into album_membership(id, asset_id, album_id, album_title, photos_sqlite_album_kind, photos_sqlite_album_subtype)
values (?, ?, ?, ?, ?, ?)
`},
		{&stmts.location, `
insert into location_observation(id, asset_id, latitude, longitude, altitude, horizontal_accuracy, source, evidence_id)
values (?, ?, ?, ?, ?, ?, ?, ?)
`},
		{&stmts.fts, `insert into asset_fts(id, title, body) values (?, ?, ?)`},
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
	for _, stmt := range []*sql.Stmt{s.previousFingerprint, s.asset, s.resource, s.album, s.location, s.fts, s.seen} {
		if stmt != nil {
			_ = stmt.Close()
		}
	}
}
