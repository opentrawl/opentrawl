package archive

const schemaVersion = 1

const schema = `
create table if not exists messages (
  id text primary key,
  thread_id text not null,
  time text not null,
  time_unix integer not null,
  from_name text not null default '',
  from_address text not null default '',
  to_address text not null default '',
  subject text not null default '',
  body text not null default '',
  labels_json text not null default '[]'
);

create virtual table if not exists messages_fts using fts5(
  id unindexed,
  subject,
  body
);

create index if not exists idx_messages_time on messages(time_unix desc, id);
create index if not exists idx_messages_from_address on messages(from_address);
`
