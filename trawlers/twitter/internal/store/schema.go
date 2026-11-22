package store

const schemaSQL = `
create table if not exists tweets (
	id text primary key,
	created_at text not null,
	author_id text,
	author_handle text,
	author_name text,
	text text not null default '',
	in_reply_to_id text,
	conversation_id text,
	quoted_tweet_id text,
	like_count integer not null default 0,
	retweet_count integer not null default 0,
	reply_count integer not null default 0,
	view_count integer not null default 0,
	quote_count integer not null default 0,
	bookmark_count integer not null default 0,
	has_media integer not null default 0,
	raw_json text,
	first_source text not null check(first_source in ('archive', 'live')),
	metrics_fetched_at text
);

create table if not exists tweet_roles (
	tweet_id text not null references tweets(id) on delete cascade,
	role text not null check(role in ('authored', 'like', 'bookmark', 'mention', 'reply_context')),
	first_seen_at text not null,
	last_seen_at text not null,
	primary key(tweet_id, role)
);

create table if not exists profiles (
	author_id text primary key,
	handle text,
	display_name text,
	last_seen_at text not null
);

create index if not exists idx_tweets_created_at on tweets(created_at);
create index if not exists idx_tweets_in_reply_to on tweets(in_reply_to_id);
create index if not exists idx_tweets_conversation_id on tweets(conversation_id);
create index if not exists idx_tweet_roles_role on tweet_roles(role, tweet_id);
create index if not exists idx_profiles_handle on profiles(handle);

create virtual table if not exists tweets_fts using fts5(
	text,
	author_handle,
	author_name,
	content='tweets',
	content_rowid='rowid'
);

create trigger if not exists tweets_fts_ai after insert on tweets begin
	insert into tweets_fts(rowid, text, author_handle, author_name)
	values (new.rowid, new.text, new.author_handle, new.author_name);
end;

create trigger if not exists tweets_fts_ad after delete on tweets begin
	insert into tweets_fts(tweets_fts, rowid, text, author_handle, author_name)
	values ('delete', old.rowid, old.text, old.author_handle, old.author_name);
end;

create trigger if not exists tweets_fts_au after update on tweets begin
	insert into tweets_fts(tweets_fts, rowid, text, author_handle, author_name)
	values ('delete', old.rowid, old.text, old.author_handle, old.author_name);
	insert into tweets_fts(rowid, text, author_handle, author_name)
	values (new.rowid, new.text, new.author_handle, new.author_name);
end;

create table if not exists dm_conversations (
	id text primary key,
	inbox_kind text not null default 'accepted' check(inbox_kind in ('accepted', 'request')),
	conversation_kind text not null default 'direct' check(conversation_kind in ('direct', 'group')),
	title text not null default '',
	participant_author_id text not null default '',
	unread_count integer not null default 0,
	needs_reply integer not null default 0,
	last_message_at text not null default ''
);

create table if not exists dm_conversation_participants (
	conversation_id text not null references dm_conversations(id) on delete cascade,
	author_id text not null,
	primary key(conversation_id, author_id)
);

create table if not exists dm_messages (
	id text primary key,
	conversation_id text not null references dm_conversations(id) on delete cascade,
	created_at text not null,
	sender_author_id text not null default '',
	direction text not null default 'inbound' check(direction in ('inbound', 'outbound')),
	text text not null default '',
	media_count integer not null default 0,
	raw_json text
);

create index if not exists idx_dm_conversations_last_message on dm_conversations(last_message_at desc, id);
create index if not exists idx_dm_conversations_participant on dm_conversations(participant_author_id);
create index if not exists idx_dm_conversation_participants_author on dm_conversation_participants(author_id);
create index if not exists idx_dm_messages_conversation_time on dm_messages(conversation_id, created_at desc, id desc);
create index if not exists idx_dm_messages_created_at on dm_messages(created_at desc, id desc);
create index if not exists idx_dm_messages_sender on dm_messages(sender_author_id);

create virtual table if not exists dm_messages_fts using fts5(
	text,
	content='dm_messages',
	content_rowid='rowid'
);

create trigger if not exists dm_messages_fts_ai after insert on dm_messages begin
	insert into dm_messages_fts(rowid, text)
	values (new.rowid, new.text);
end;

create trigger if not exists dm_messages_fts_ad after delete on dm_messages begin
	insert into dm_messages_fts(dm_messages_fts, rowid, text)
	values ('delete', old.rowid, old.text);
end;

create trigger if not exists dm_messages_fts_au after update on dm_messages begin
	insert into dm_messages_fts(dm_messages_fts, rowid, text)
	values ('delete', old.rowid, old.text);
	insert into dm_messages_fts(rowid, text)
	values (new.rowid, new.text);
end;
`
