package store

// schemaVersion 2 replaced the hand-rolled sync_state(kind, cursor,
// last_sync_at, last_result, coverage_note) table with crawlkit's
// canonical state.Schema. migrateLegacySyncState copies every legacy row
// into the canonical shape before dropping the old table — see store.go.
const schemaVersion = 2

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
`
