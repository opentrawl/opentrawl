package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

func upsertTweets(ctx context.Context, tx *sql.Tx, tweets []Tweet, now time.Time) error {
	for _, t := range tweets {
		if strings.TrimSpace(t.ID) == "" {
			continue
		}
		createdAt := t.CreatedAt
		if createdAt.IsZero() {
			createdAt = parseStoredTime(UnknownTimeRFC3339)
		}
		source := strings.TrimSpace(t.FirstSource)
		if source == "" {
			source = "archive"
		}
		metricsFetchedAt := ""
		if !t.MetricsFetchedAt.IsZero() {
			metricsFetchedAt = formatUTC(t.MetricsFetchedAt)
		}
		_, err := tx.ExecContext(ctx, `insert into tweets(
id,created_at,author_id,author_handle,author_name,text,in_reply_to_id,conversation_id,
quoted_tweet_id,like_count,retweet_count,reply_count,view_count,quote_count,bookmark_count,
has_media,raw_json,first_source,metrics_fetched_at)
values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
on conflict(id) do update set
created_at=case when excluded.created_at <> ? then excluded.created_at else tweets.created_at end,
author_id=coalesce(nullif(excluded.author_id,''), tweets.author_id),
author_handle=coalesce(nullif(excluded.author_handle,''), tweets.author_handle),
author_name=coalesce(nullif(excluded.author_name,''), tweets.author_name),
text=coalesce(nullif(excluded.text,''), tweets.text),
in_reply_to_id=coalesce(nullif(excluded.in_reply_to_id,''), tweets.in_reply_to_id),
conversation_id=coalesce(nullif(excluded.conversation_id,''), tweets.conversation_id),
quoted_tweet_id=coalesce(nullif(excluded.quoted_tweet_id,''), tweets.quoted_tweet_id),
like_count=case when excluded.metrics_fetched_at is not null and excluded.metrics_fetched_at <> '' then excluded.like_count else max(tweets.like_count, excluded.like_count) end,
retweet_count=case when excluded.metrics_fetched_at is not null and excluded.metrics_fetched_at <> '' then excluded.retweet_count else max(tweets.retweet_count, excluded.retweet_count) end,
reply_count=case when excluded.metrics_fetched_at is not null and excluded.metrics_fetched_at <> '' then excluded.reply_count else max(tweets.reply_count, excluded.reply_count) end,
view_count=case when excluded.metrics_fetched_at is not null and excluded.metrics_fetched_at <> '' then excluded.view_count else max(tweets.view_count, excluded.view_count) end,
quote_count=case when excluded.metrics_fetched_at is not null and excluded.metrics_fetched_at <> '' then excluded.quote_count else max(tweets.quote_count, excluded.quote_count) end,
bookmark_count=case when excluded.metrics_fetched_at is not null and excluded.metrics_fetched_at <> '' then excluded.bookmark_count else max(tweets.bookmark_count, excluded.bookmark_count) end,
has_media=max(tweets.has_media, excluded.has_media),
raw_json=coalesce(excluded.raw_json, tweets.raw_json),
metrics_fetched_at=coalesce(nullif(excluded.metrics_fetched_at,''), tweets.metrics_fetched_at)`,
			t.ID, formatUTC(createdAt), t.AuthorID, t.AuthorHandle, t.AuthorName, t.Text,
			t.InReplyToID, t.ConversationID, t.QuotedTweetID, t.LikeCount, t.RetweetCount,
			t.ReplyCount, t.ViewCount, t.QuoteCount, t.BookmarkCount, boolInt(t.HasMedia),
			nullableString(t.RawJSON), source, nullableString(metricsFetchedAt), UnknownTimeRFC3339)
		if err != nil {
			return err
		}
	}
	return nil
}

func upsertRoles(ctx context.Context, tx *sql.Tx, roles []Role, now time.Time) error {
	for _, role := range roles {
		if strings.TrimSpace(role.TweetID) == "" || strings.TrimSpace(role.Role) == "" {
			continue
		}
		firstSeen := role.FirstSeenAt
		if firstSeen.IsZero() {
			firstSeen = now
		}
		lastSeen := role.LastSeenAt
		if lastSeen.IsZero() {
			lastSeen = firstSeen
		}
		_, err := tx.ExecContext(ctx, `insert into tweet_roles(tweet_id,role,first_seen_at,last_seen_at)
values(?,?,?,?)
on conflict(tweet_id, role) do update set last_seen_at=excluded.last_seen_at`,
			role.TweetID, role.Role, formatUTC(firstSeen), formatUTC(lastSeen))
		if err != nil {
			return err
		}
	}
	return nil
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
