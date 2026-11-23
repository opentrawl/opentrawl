package beeperstore

const beeperArchiveSchemaSQL = `
create table if not exists beeper_accounts (
	account_id text primary key,
	network text not null,
	status text not null default '',
	status_text text not null default '',
	login_id text not null default '',
	self_user_id text not null default '',
	self_full_name text not null default '',
	self_username text not null default '',
	updated_at_unix integer not null default 0
);

create table if not exists beeper_chats (
	chat_id text primary key,
	local_chat_id text not null default '',
	account_id text not null default '',
	network text not null default '',
	title text not null default '',
	chat_type text not null default 'single',
	description text not null default '',
	is_archived integer not null default 0,
	is_muted integer not null default 0,
	is_pinned integer not null default 0,
	is_read_only integer not null default 0,
	is_low_priority integer not null default 0,
	unread_count integer not null default 0,
	last_activity_unix integer not null default 0,
	updated_at_unix integer not null default 0
);

create index if not exists idx_beeper_chats_network on beeper_chats(network);
create index if not exists idx_beeper_chats_activity on beeper_chats(last_activity_unix desc);

create table if not exists beeper_chat_participants (
	chat_id text not null,
	user_id text not null,
	full_name text not null default '',
	username text not null default '',
	phone_number text not null default '',
	email text not null default '',
	is_self integer not null default 0,
	is_admin integer not null default 0,
	is_pending integer not null default 0,
	is_network_bot integer not null default 0,
	primary key (chat_id, user_id)
);

create index if not exists idx_beeper_participants_user on beeper_chat_participants(user_id);

create table if not exists beeper_messages (
	rowid integer primary key autoincrement,
	message_id text not null unique,
	chat_id text not null,
	account_id text not null default '',
	network text not null default '',
	sort_key text not null default '',
	sender_id text not null default '',
	sender_name text not null default '',
	is_sender integer not null default 0,
	timestamp_unix integer not null default 0,
	edited_timestamp_unix integer not null default 0,
	message_type text not null default '',
	text text not null default '',
	linked_message_id text not null default '',
	is_deleted integer not null default 0,
	is_hidden integer not null default 0,
	media_kind text not null default '',
	media_title text not null default '',
	media_mime_type text not null default '',
	media_url text not null default '',
	media_size_bytes integer not null default 0,
	media_transcript text not null default '',
	links_summary text not null default '',
	reactions_summary text not null default ''
);

create index if not exists idx_beeper_messages_chat_ts on beeper_messages(chat_id, timestamp_unix, rowid);
create index if not exists idx_beeper_messages_ts on beeper_messages(timestamp_unix desc, rowid desc);
create index if not exists idx_beeper_messages_sender on beeper_messages(sender_id);
create index if not exists idx_beeper_messages_network on beeper_messages(network);

create table if not exists beeper_chat_sync_state (
	chat_id text primary key,
	newest_sort_key text not null default '',
	newest_message_id text not null default '',
	oldest_cursor text not null default '',
	backfill_complete integer not null default 0,
	last_synced_activity_unix integer not null default 0,
	last_synced_at_unix integer not null default 0
);

create virtual table if not exists beeper_messages_fts using fts5(
	text,
	chat,
	sender,
	network,
	media
);
`
