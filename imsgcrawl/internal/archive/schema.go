package archive

const schemaVersion = 1

const schema = `
create table if not exists handles (
  source_rowid integer primary key,
  handle text not null,
  service text not null,
  uncanonicalized_id text
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
  is_from_me integer not null default 0,
  text text,
  has_attachments integer not null default 0
);

create virtual table if not exists messages_fts using fts5(source_rowid unindexed, text);

create table if not exists sync_state (
  key text primary key,
  value text not null
);

create index if not exists idx_chat_messages_chat on chat_messages(chat_rowid, message_rowid);
create index if not exists idx_chat_messages_message on chat_messages(message_rowid, chat_rowid);
create index if not exists idx_messages_date on messages(date, source_rowid);
`
