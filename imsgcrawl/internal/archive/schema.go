package archive

const schemaVersion = 6

// The leading drop tombstones the old key/value sync_state table so the
// canonical crawlkit state.Schema (appended at Open) can create sync_state in
// its own shape. Only the writable sync path applies this schema, and sync
// fully rewrites sync_state, so dropping on open never loses live state; a
// pre-migration archive re-derives its state in one sync (rules §1.17).
const schema = `
drop table if exists sync_state;

create table if not exists handles (
  source_rowid integer primary key,
  handle text not null,
  service text not null,
  uncanonicalized_id text,
  display_name text
);

create table if not exists chats (
  source_rowid integer primary key,
  guid text not null,
  chat_identifier text,
  service_name text,
  display_name text,
  room_name text,
  is_archived integer not null default 0
);

create table if not exists chat_participants (
  chat_rowid integer not null,
  handle_rowid integer not null,
  primary key (chat_rowid, handle_rowid)
);

create table if not exists chat_messages (
  chat_rowid integer not null,
  message_rowid integer not null,
  primary key (chat_rowid, message_rowid)
);

create table if not exists messages (
  source_rowid integer primary key,
  guid text not null,
  handle_rowid integer not null default 0,
  date integer not null default 0,
  service text,
  account text,
  is_from_me integer not null default 0,
  text text,
  has_attachments integer not null default 0
);

create virtual table if not exists messages_fts using fts5(source_rowid unindexed, text);

create table if not exists contact_mappings (
  kind text not null,
  normalized_handle text not null,
  contact_key text not null default '',
  display_name text not null,
  primary key (kind, normalized_handle)
);

create table if not exists owner_handles (
  kind text not null,
  normalized_handle text not null,
  primary key (kind, normalized_handle)
);

create index if not exists idx_chat_messages_chat on chat_messages(chat_rowid, message_rowid);
create index if not exists idx_chat_messages_message on chat_messages(message_rowid, chat_rowid);
create index if not exists idx_messages_date on messages(date, source_rowid);
`
