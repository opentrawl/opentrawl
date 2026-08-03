package archive

// Porter stemming so a search for "grill" matches cards that say "grilled".
const (
	assetFTSSchema       = `create virtual table if not exists asset_fts using fts5(id unindexed, title, body, tokenize='porter unicode61');`
	observationFTSSchema = `create virtual table if not exists observation_fts using fts5(id unindexed, asset_id unindexed, title, body, tokenize='porter unicode61');`
)

const Schema = `
create table if not exists source_library (
  id text primary key,
  photos_library_database_uuid text not null unique,
  configured_library_path text not null,
  snapshot_path text,
  snapshot_created_at text
);

create table if not exists crawl_snapshot (
  id text primary key,
  source_library_id text not null references source_library(id),
  started_at text not null,
  completed_at text not null,
  provider text not null,
  expected_active_asset_count integer not null,
  expected_unique_asset_identifier_count integer not null,
  database_snapshot_file_count integer not null,
  database_snapshot_bytes integer not null,
  album_join_table text not null,
  asset_count integer not null,
  resource_count integer not null,
  album_membership_count integer not null,
  location_count integer not null,
  completeness_state text not null,
  database_copy_completed integer not null,
  resource_queries_completed integer not null,
  album_queries_completed integer not null,
  asset_query_completed integer not null
);

create table if not exists crawl_seen_asset (
  source_library_id text not null references source_library(id),
  asset_id text not null,
  first_seen_snapshot_id text not null references crawl_snapshot(id),
  last_seen_snapshot_id text not null references crawl_snapshot(id),
  source_fingerprint text not null,
  last_seen_at text not null,
  primary key (source_library_id, asset_id)
);

create table if not exists update_cursor_state (
  source text not null,
  entity_type text not null,
  entity_id text not null,
  cursor text not null,
  updated_at text not null,
  primary key (source, entity_type, entity_id)
);

create table if not exists asset (
  id text primary key,
  photos_sqlite_asset_primary_key integer not null,
  local_identifier text not null unique,
  media_type text not null,
  photos_sqlite_kind integer not null,
  photos_sqlite_kind_subtype integer not null,
  creation_date text not null,
  modification_date text not null,
  added_date text not null,
  timezone_name text not null,
  width integer not null,
  height integer not null,
  duration_seconds real not null,
  favorite integer not null,
  hidden integer not null,
  burst_identifier text not null,
  represents_burst integer not null,
  camera_make text not null,
  camera_model text not null,
  lens_model text not null,
  focal_length_mm real,
  focal_length_35mm real,
  aperture real,
  shutter_speed real,
  iso integer,
  uniform_type_identifier text not null,
  filename text not null,
  original_filename text not null,
  source_library_id text not null references source_library(id),
  source_state text not null default 'current',
  first_missing_at text,
  source_deleted_at text,
  source_state_snapshot_id text not null default ''
);

create table if not exists asset_resource (
  id text primary key,
  asset_id text not null references asset(id),
  photos_sqlite_resource_primary_key integer not null,
  photos_sqlite_resource_type integer not null,
  photos_sqlite_compact_uti text not null,
  photos_sqlite_resource_version integer not null,
  photos_sqlite_local_availability integer not null,
  photos_sqlite_remote_availability integer not null,
  photos_sqlite_stable_hash text not null,
  photos_sqlite_fingerprint text not null,
  resource_type_projection text not null,
  uti_projection text not null,
  availability_projection text not null,
  original_filename text not null,
  file_size integer not null,
  available_locally integer not null,
  needs_download integer not null
);

create table if not exists album_membership (
  id text primary key,
  asset_id text not null references asset(id),
  album_id text not null,
  album_title text not null,
  photos_sqlite_album_kind integer not null,
  photos_sqlite_album_subtype integer not null
);

create table if not exists location_observation (
  id text primary key,
  asset_id text not null references asset(id),
  latitude real not null,
  longitude real not null,
  altitude real,
  horizontal_accuracy real,
  source text not null,
  evidence_id text not null
);

create table if not exists known_place (
  id text primary key,
  label_kind text not null,
  display_name text not null,
  latitude real not null,
  longitude real not null,
  radius_meters real not null default 75,
  valid_from text not null,
  valid_until text not null,
  updated_at text not null,
  unique(label_kind, display_name)
);

create table if not exists configured_known_place_match_outcome (
  asset_id text primary key references asset(id),
  outcome_proto blob not null
);

create table if not exists location_provider_evidence (
  provider_operation integer not null,
  provider_request_sha256 blob not null,
  provider_request_proto blob not null,
  operation_state integer not null check (operation_state between 3 and 6),
  outcome_proto blob not null,
  primary key (provider_operation, provider_request_sha256)
);

create table if not exists photo_location_provider_operation (
  asset_id text not null references asset(id),
  provider_operation integer not null,
  provider_request_sha256 blob,
  operation_request_proto blob not null,
  operation_state integer not null check (operation_state between 3 and 7),
  skipped_outcome_proto blob,
  primary key (asset_id, provider_operation),
  foreign key (provider_operation, provider_request_sha256) references location_provider_evidence(provider_operation, provider_request_sha256),
  check (
    (operation_state = 7 and provider_request_sha256 is null and skipped_outcome_proto is not null)
    or
    (operation_state between 3 and 6 and provider_request_sha256 is not null and skipped_outcome_proto is null)
  )
);

create table if not exists location_provider_transmission_attempt (
  attempt_id integer primary key,
  provider_operation integer not null,
  provider_request_sha256 blob not null,
  provider_request_proto blob not null,
  operation_state integer not null check (operation_state between 2 and 6),
  transmission_started_at text not null,
  response_retained_at text,
  completed_at text
);

create index if not exists location_provider_attempt_request_idx on location_provider_transmission_attempt(provider_operation, provider_request_sha256, attempt_id desc);

create table if not exists current_photo_location_evidence (
  asset_id text primary key references asset(id),
  known_place_configuration_sha256 blob not null,
  outcome_proto blob not null
);

create table if not exists current_rendered_photo_media_evidence (
  asset_id text primary key references asset(id),
  derivation_receipt_proto blob not null,
  current_rendered_still_sha256 blob not null,
  current_rendered_still_uniform_type_identifier text not null,
  current_rendered_still_byte_count integer not null,
  current_rendered_still_pixel_width integer not null,
  current_rendered_still_pixel_height integer not null,
  current_rendered_still_orientation integer not null
);

create table if not exists current_immutable_original_image_facts (
  asset_id text primary key references asset(id),
  outcome_proto blob not null
);

create table if not exists current_photo_foundation_outcome (
  asset_id text primary key references asset(id),
  outcome_proto blob not null
);

` + assetFTSSchema + `
` + observationFTSSchema + `

create index if not exists asset_creation_idx on asset(creation_date);
create index if not exists asset_burst_idx on asset(burst_identifier);
create index if not exists crawl_snapshot_source_idx on crawl_snapshot(source_library_id, completed_at desc);
create index if not exists crawl_seen_asset_snapshot_idx on crawl_seen_asset(last_seen_snapshot_id);
create index if not exists idx_update_cursor_state_updated_at on update_cursor_state(updated_at desc);
create index if not exists resource_asset_idx on asset_resource(asset_id);
create unique index if not exists resource_source_identity_idx on asset_resource(asset_id, photos_sqlite_resource_primary_key);
create index if not exists album_asset_idx on album_membership(asset_id);
create index if not exists location_asset_idx on location_observation(asset_id);
create index if not exists known_place_kind_name_idx on known_place(label_kind, display_name);
`
