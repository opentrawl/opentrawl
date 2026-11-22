package archive

// A message row is kept when Gmail deletes the message or moves it to Spam
// or Trash; deleted_from_source_at is then set. Each row's raw RFC 5322
// bytes are kept once under originals/, named by raw_email_original_sha256.
//
// The two gmail_full_mailbox_listing tables exist only while a full listing
// of the mailbox is in progress, so an interrupted listing resumes without
// fetching a message twice.
const schema = `
create table if not exists messages (
  id text primary key,
  thread_id text not null,
  history_id text not null default '',
  internal_date_ms integer not null default 0,
  raw_email_original_sha256 text not null,
  email_parser_revision text not null,
  rfc5322_message_identifier text not null default '',
  in_reply_to_message_identifiers text not null default '',
  referenced_message_identifiers text not null default '',
  reply_to_address text not null default '',
  delivered_to_address text not null default '',
  mailing_list_identifier text not null default '',
  time text not null,
  time_unix integer not null,
  from_name text not null default '',
  from_address text not null default '',
  to_address text not null default '',
  cc_address text not null default '',
  subject text not null default '',
  body text not null default '',
  labels_json text not null default '[]',
  is_unread integer not null default 0,
  deleted_from_source_at text not null default ''
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
  type text not null default ''
);

create table if not exists gmail_full_mailbox_listing_history_cursor (
  singleton integer primary key check (singleton = 1),
  history_cursor text not null
);

create table if not exists gmail_full_mailbox_listing_messages (
  id text primary key,
  listing_position integer not null unique,
  fetched integer not null default 0 check (fetched in (0, 1)),
  still_in_gmail integer not null default 0 check (still_in_gmail in (0, 1))
);

create virtual table if not exists messages_fts using fts5(
  id unindexed,
  subject,
  body,
  participants
);

create index if not exists idx_messages_time on messages(time_unix desc, id);
create index if not exists idx_messages_from_address on messages(from_address);
create index if not exists idx_messages_unread on messages(is_unread);
create index if not exists idx_messages_deleted_from_source_at on messages(deleted_from_source_at);
create index if not exists idx_attachments_message_id on attachments(message_id);
create index if not exists idx_message_participants_message_id on message_participants(message_id);
create index if not exists idx_message_participants_key on message_participants(participant_key);
`
