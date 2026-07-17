package archive

const schema = `
create table if not exists people (
  id text primary key,
  name text not null,
  sort_name text not null default '',
  aka_json text not null default '[]',
  tags_json text not null default '[]',
  avatar_json text not null default '{}',
  accounts_json text not null default '{}',
  sources_json text not null default '{}',
  apple_json text not null default '{}',
  google_json text not null default '{}',
  body text not null default '',
  annotation text not null default '',
  annotation_stated_at text not null default '',
  created_at text not null,
  updated_at text not null
);

create table if not exists person_avatars (
  person_id text primary key references people(id) on delete cascade,
  data blob not null,
  mime text not null default '',
  sha256 text not null,
  source text not null default '',
  updated_at text not null
);

create table if not exists contact_values (
  person_id text not null references people(id) on delete cascade,
  kind text not null check(kind in ('email', 'phone', 'address')),
  position integer not null,
  value text not null default '',
  label text not null default '',
  source text not null default '',
  primary_value integer not null default 0,
  primary key(person_id, kind, position)
);

create table if not exists identifiers (
  person_id text not null references people(id) on delete cascade,
  kind text not null check(kind in ('email', 'phone', 'handle')),
  value text not null,
  primary key(kind, value, person_id)
);

create table if not exists source_contacts (
  source text not null,
  source_id text not null,
  person_id text not null references people(id) on delete cascade,
  contact_json text not null,
  synced_at text not null,
  primary key(source, source_id)
);

create table if not exists source_contact_group_overrides (
  source text not null,
  source_id text not null,
  person_id text not null references people(id) on delete cascade,
  primary key(source, source_id)
);

create table if not exists notes (
  id text primary key,
  person_id text not null references people(id) on delete cascade,
  occurred_at text not null,
  captured_at text not null,
  kind text not null default '',
  source text not null default '',
  account text not null default '',
  external_id text not null default '',
  direction text not null default '',
  confidence text not null default '',
  topics_json text not null default '[]',
  follow_up_at text not null default '',
  privacy text not null default '',
  body text not null default ''
);

create index if not exists idx_contact_values_person on contact_values(person_id, kind, position);
create index if not exists idx_identifiers_person on identifiers(person_id);
create index if not exists idx_source_contacts_person on source_contacts(person_id);
create index if not exists idx_notes_person on notes(person_id, occurred_at);

create virtual table if not exists people_fts using fts5(
  person_id unindexed,
  names,
  aliases,
  identifiers,
  body,
  tags
);
`
