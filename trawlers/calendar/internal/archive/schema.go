package archive

const schema = `
create table if not exists calendars (
  calendar_id text primary key,
  source_row_id integer not null,
  title text not null,
  type integer not null default 0,
  external_id text not null default '',
  store_id integer not null default 0,
  account_name text not null default '',
  account_type integer not null default 0,
  account_disabled integer not null default 0,
  meaning text default '',
  meaning_stated_at text default '',
  sync_run_id text not null default ''
);

create table if not exists events (
  event_uid text primary key,
  source_row_id integer not null,
  uuid text not null default '',
  unique_identifier text not null default '',
  calendar_id text not null,
  calendar_title text not null,
  calendar_type integer not null default 0,
  calendar_external_id text not null default '',
  account_name text not null default '',
  account_type integer not null default 0,
  start_time text not null,
  end_time text not null,
  start_unix integer not null,
  end_unix integer not null,
  all_day integer not null default 0,
  summary text not null default '',
  description text not null default '',
  status text not null default '',
  url text not null default '',
  has_recurrences integer not null default 0,
  availability integer,
  organizer_name text not null default '',
  organizer_email text not null default '',
  organizer_phone text not null default '',
  location_title text not null default '',
  location_address text not null default '',
  attendees_json text not null default '[]',
  participants_text text not null default '',
  fingerprint text not null default '',
  sync_run_id text not null default '',
  foreign key(calendar_id) references calendars(calendar_id)
);

create table if not exists participants (
  event_uid text not null,
  position integer not null,
  display_name text not null default '',
  email text not null default '',
  phone_number text not null default '',
  address text not null default '',
  rsvp_status text not null default '',
  role text not null default '',
  is_self integer not null default 0,
  comment text not null default '',
  sync_run_id text not null default '',
  primary key(event_uid, position),
  foreign key(event_uid) references events(event_uid)
);

create table if not exists locations (
  event_uid text primary key,
  title text not null default '',
  address text not null default '',
  sync_run_id text not null default '',
  foreign key(event_uid) references events(event_uid)
);

create index if not exists idx_events_start on events(start_unix, event_uid);
create index if not exists idx_events_calendar on events(calendar_id, event_uid);
create index if not exists idx_participants_phone on participants(phone_number);

create virtual table if not exists events_fts using fts5(
  event_uid unindexed,
  summary,
  description,
  location,
  participants
);
`
