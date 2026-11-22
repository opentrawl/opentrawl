package archive

// A message row is kept when Apple Mail no longer has it; deleted_from_source_at
// is then set. Only one live row exists for each Apple Mail row identifier.
const schema = `
create table if not exists apple_mail_accounts (
  apple_mail_account_identifier text primary key,
  apple_mail_account_display_name text not null default '',
  apple_mail_account_user_name text not null default '',
  apple_mail_account_type_identifier text not null default ''
);

create table if not exists messages (
  id integer primary key autoincrement,
  apple_mail_account_identifier text not null,
  apple_mail_mailbox_url text not null,
  apple_mail_mailbox_name text not null,
  apple_mail_message_row_identifier integer not null,
  emlx_file_path text not null,
  emlx_file_byte_count integer not null,
  emlx_file_modification_time_ns integer not null,
  emlx_file_is_partial integer not null,
  raw_email_original_sha256 text not null,
  email_parser_revision text not null,
  rfc5322_message_identifier text not null default '',
  in_reply_to_message_identifiers text not null default '',
  referenced_message_identifiers text not null default '',
  reply_to_address text not null default '',
  delivered_to_address text not null default '',
  mailing_list_identifier text not null default '',
  from_name text not null default '',
  from_address text not null default '',
  to_address text not null default '',
  cc_address text not null default '',
  subject text not null default '',
  body text not null default '',
  time text not null,
  time_unix integer not null,
  is_unread integer not null default 0,
  deleted_from_source_at text not null default ''
);

create table if not exists attachments (
  id integer primary key autoincrement,
  message_id integer not null references messages(id) on delete cascade,
  filename text not null default '',
  mime_type text not null default '',
  size_bytes integer
);

create table if not exists message_participants (
  message_id integer not null references messages(id) on delete cascade,
  role text not null,
  name text not null default '',
  address text not null default '',
  display_name text not null default '',
  participant_key text not null,
  primary key(message_id, role, participant_key, name, address)
);

create virtual table if not exists messages_fts using fts5(
  id unindexed,
  subject,
  body,
  participants
);

create unique index if not exists idx_messages_live_apple_mail_message_row_identifier
  on messages(apple_mail_message_row_identifier) where deleted_from_source_at = '';
create index if not exists idx_messages_time on messages(time_unix desc, id);
create index if not exists idx_messages_from_address on messages(from_address);
create index if not exists idx_attachments_message_id on attachments(message_id);
create index if not exists idx_message_participants_message_id on message_participants(message_id);
create index if not exists idx_message_participants_key on message_participants(participant_key);
`
