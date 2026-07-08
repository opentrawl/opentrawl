package archive

const schemaVersion = 2

const schema = `
create table if not exists messages (
  id text primary key,
  thread_id text not null,
  history_id text not null default '',
  internal_date_ms integer not null default 0,
  time text not null,
  time_unix integer not null,
  from_name text not null default '',
  from_address text not null default '',
  to_address text not null default '',
  cc_address text not null default '',
  subject text not null default '',
  body text not null default '',
  labels_json text not null default '[]'
);

create table if not exists attachments (
  id integer primary key autoincrement,
  message_id text not null references messages(id) on delete cascade,
  filename text not null default '',
  mime_type text not null default '',
  size_bytes integer not null default 0
);

create table if not exists message_participants (
  message_id text not null references messages(id) on delete cascade,
  role text not null,
  name text not null default '',
  address text not null default '',
  display_name text not null default '',
  participant_key text not null,
  primary key(message_id, role, participant_key, name, address)
);

create table if not exists gmail_labels (
  id text primary key,
  name text not null default '',
  type text not null default '',
  raw_json text not null default '{}'
);

drop table if exists ingested_shards;

create virtual table if not exists messages_fts using fts5(
  id unindexed,
  subject,
  body
);

create index if not exists idx_messages_time on messages(time_unix desc, id);
create index if not exists idx_messages_from_address on messages(from_address);
create index if not exists idx_attachments_message_id on attachments(message_id);
create index if not exists idx_message_participants_message_id on message_participants(message_id);
create index if not exists idx_message_participants_key on message_participants(participant_key);
`
