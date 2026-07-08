package archive

const schema = `
create table if not exists notes (
  note_id text primary key,
  title text not null default '',
  folder text not null default '',
  created_at text not null default '',
  modified_at text not null default '',
  last_seen_at text not null default ''
);

create table if not exists note_versions (
  note_id text not null,
  zdata_sha256 text not null,
  zdata blob not null,
  zdata_bytes integer not null,
  text text not null default '',
  text_status text not null,
  unsupported_reason text not null default '',
  source_modified_at text not null default '',
  first_observed_at text not null,
  latest_observed_at text not null,
  primary key (note_id, zdata_sha256)
);

create table if not exists version_observations (
  observation_id integer primary key autoincrement,
  note_id text not null,
  zdata_sha256 text not null,
  source text not null,
  source_detail text not null default '',
  source_sequence integer not null default 0,
  source_modified_at text not null default '',
  observed_at text not null
);

create table if not exists coverage (
  source_class text primary key,
  status text not null,
  zdata_candidates integer not null default 0,
  assigned_note_versions integer not null default 0,
  unassigned_candidates integer not null default 0,
  failure_reason text not null default '',
  next_source text not null default '',
  inspected_at text not null default ''
);

create table if not exists sync_state (
  key text primary key,
  value text not null
);

create virtual table if not exists notes_fts using fts5(
  note_id unindexed,
  zdata_sha256 unindexed,
  title,
  body
);

create index if not exists idx_note_versions_time on note_versions(note_id, source_modified_at, first_observed_at);
create index if not exists idx_version_observations_version on version_observations(note_id, zdata_sha256, observation_id);
`
