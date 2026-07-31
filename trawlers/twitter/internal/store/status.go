package store

import (
	"context"
	"database/sql"
	"time"
)

type Status struct {
	Authored         int
	RepliesToMe      int
	Bookmarks        int
	LikesSeen        int
	Tweets           int
	OldestTweet      time.Time
	NewestTweet      time.Time
	LastImportAt     time.Time
	LastLiveUpdate   time.Time
	LiveUpdateResult string
	CoverageThrough  time.Time
	SpendMonth       string
	SpendMicros      int64
	TokenValid       bool
	FTSTweets        int
	FTSRows          int
	IntegrityText    string
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
	bookmarkPass, err := s.UpdateState(ctx, "bookmark_pass")
	if err != nil {
		return out, err
	}
	if bookmarkPass.Cursor != "" {
		if err := s.db.QueryRowContext(ctx, `select count(*) from tweet_roles where role = 'bookmark' and last_seen_at = ?`, bookmarkPass.Cursor).Scan(&out.Bookmarks); err != nil {
			return out, err
		}
	} else if err := s.db.QueryRowContext(ctx, `select count(*) from tweet_roles where role = 'bookmark'`).Scan(&out.Bookmarks); err != nil {
		return out, err
	}
	archiveImport, err := s.UpdateState(ctx, "archive_import")
	if err != nil {
		return out, err
	}
	out.LastImportAt = archiveImport.LastUpdateAt
	if archiveImport.Cursor != "" {
		out.CoverageThrough = parseStoredTime(archiveImport.Cursor)
	}
	liveUpdate, err := s.UpdateState(ctx, "live_update")
	if err != nil {
		return out, err
	}
	out.LastLiveUpdate = liveUpdate.LastUpdateAt
	out.LiveUpdateResult = liveUpdate.LastResult
	tokenState, err := s.UpdateState(ctx, "auth:token_valid")
	if err != nil {
		return out, err
	}
	out.TokenValid = tokenState.Cursor == "true"
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
