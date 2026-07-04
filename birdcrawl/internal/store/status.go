package store

import (
	"context"
	"database/sql"
	"time"
)

type Status struct {
	Authored        int
	RepliesToMe     int
	Bookmarks       int
	LikesSeen       int
	Tweets          int
	OldestTweet     time.Time
	NewestTweet     time.Time
	LastImportAt    time.Time
	LastLiveSync    time.Time
	LiveSyncResult  string
	CoverageThrough time.Time
	SpendMonth      string
	SpendMicros     int64
	TokenValid      bool
	FTSTweets       int
	FTSRows         int
	IntegrityText   string
}

func (s *Store) Status(ctx context.Context) (Status, error) {
	var out Status
	for _, count := range []struct {
		dst *int
		sql string
	}{
		{&out.Tweets, `select count(*) from tweets`},
		{&out.Authored, `select count(*) from tweet_roles where role = 'authored'`},
		{&out.RepliesToMe, `select count(*) from tweet_roles where role = 'mention'`},
		{&out.LikesSeen, `select count(*) from tweet_roles where role = 'like'`},
		{&out.FTSTweets, `select count(*) from tweets`},
		{&out.FTSRows, `select count(*) from tweets_fts`},
	} {
		if err := s.db.QueryRowContext(ctx, count.sql).Scan(count.dst); err != nil {
			return out, err
		}
	}
	var oldest, newest sql.NullString
	if err := s.db.QueryRowContext(ctx, `select min(created_at), max(created_at) from tweets where created_at <> ?`, UnknownTimeRFC3339).Scan(&oldest, &newest); err != nil {
		return out, err
	}
	if oldest.Valid {
		out.OldestTweet = parseStoredTime(oldest.String)
	}
	if newest.Valid {
		out.NewestTweet = parseStoredTime(newest.String)
	}
	var bookmarkPass sql.NullString
	if err := s.db.QueryRowContext(ctx, `select cursor from sync_state where kind = 'bookmark_pass'`).Scan(&bookmarkPass); err != nil && err != sql.ErrNoRows {
		return out, err
	}
	if bookmarkPass.Valid && bookmarkPass.String != "" {
		if err := s.db.QueryRowContext(ctx, `select count(*) from tweet_roles where role = 'bookmark' and last_seen_at = ?`, bookmarkPass.String).Scan(&out.Bookmarks); err != nil {
			return out, err
		}
	} else if err := s.db.QueryRowContext(ctx, `select count(*) from tweet_roles where role = 'bookmark'`).Scan(&out.Bookmarks); err != nil {
		return out, err
	}
	var importAt, importCoverage, liveAt, liveResult, tokenValid sql.NullString
	for _, q := range []struct {
		sql  string
		dsts []any
	}{
		{`select last_sync_at, cursor from sync_state where kind = 'archive_import'`, []any{&importAt, &importCoverage}},
		{`select last_sync_at, last_result from sync_state where kind = 'live_sync'`, []any{&liveAt, &liveResult}},
		{`select cursor from sync_state where kind = 'auth:token_valid'`, []any{&tokenValid}},
	} {
		if err := s.db.QueryRowContext(ctx, q.sql).Scan(q.dsts...); err != nil && err != sql.ErrNoRows {
			return out, err
		}
	}
	if importAt.Valid {
		out.LastImportAt = parseStoredTime(importAt.String)
	}
	if importCoverage.Valid {
		out.CoverageThrough = parseStoredTime(importCoverage.String)
	}
	if liveAt.Valid {
		out.LastLiveSync = parseStoredTime(liveAt.String)
	}
	if liveResult.Valid {
		out.LiveSyncResult = liveResult.String
	}
	if tokenValid.Valid && tokenValid.String == "true" {
		out.TokenValid = true
	}
	out.SpendMonth = time.Now().UTC().Format("2006-01")
	out.SpendMicros, _ = s.SpendMicros(ctx, out.SpendMonth)
	out.IntegrityText, _ = s.Integrity(ctx)
	return out, nil
}

func (s *Store) Integrity(ctx context.Context) (string, error) {
	var result string
	err := s.db.QueryRowContext(ctx, `pragma integrity_check`).Scan(&result)
	return result, err
}

func (s *Store) FTSParity(ctx context.Context) (int, int, error) {
	var tweets, fts int
	if err := s.db.QueryRowContext(ctx, `select count(*) from tweets`).Scan(&tweets); err != nil {
		return 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, `select count(*) from tweets_fts`).Scan(&fts); err != nil {
		return 0, 0, err
	}
	return tweets, fts, nil
}
