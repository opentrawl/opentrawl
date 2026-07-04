package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"
)

var ErrTweetNotFound = errors.New("tweet not found")

type Store struct {
	base *ckstore.Store
	db   *sql.DB
	path string
}

type Tweet struct {
	ID               string    `json:"id"`
	CreatedAt        time.Time `json:"created_at,omitzero"`
	AuthorID         string    `json:"author_id,omitempty"`
	AuthorHandle     string    `json:"author_handle,omitempty"`
	AuthorName       string    `json:"author_name,omitempty"`
	Text             string    `json:"text"`
	InReplyToID      string    `json:"in_reply_to_id,omitempty"`
	ConversationID   string    `json:"conversation_id,omitempty"`
	QuotedTweetID    string    `json:"quoted_tweet_id,omitempty"`
	LikeCount        int64     `json:"like_count"`
	RetweetCount     int64     `json:"retweet_count"`
	ReplyCount       int64     `json:"reply_count"`
	ViewCount        int64     `json:"view_count"`
	QuoteCount       int64     `json:"quote_count"`
	BookmarkCount    int64     `json:"bookmark_count"`
	HasMedia         bool      `json:"has_media"`
	RawJSON          string    `json:"raw_json,omitempty"`
	FirstSource      string    `json:"first_source"`
	MetricsFetchedAt time.Time `json:"metrics_fetched_at,omitzero"`
}

type Role struct {
	TweetID     string
	Role        string
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

type Profile struct {
	AuthorID    string
	Handle      string
	DisplayName string
	LastSeenAt  time.Time
}

type ImportBatch struct {
	Tweets          []Tweet
	Roles           []Role
	Profiles        []Profile
	CoverageThrough time.Time
	ImportedAt      time.Time
}

type ImportStats struct {
	Tweets              int       `json:"tweets"`
	Authored            int       `json:"authored"`
	LikesSeen           int       `json:"likes_seen"`
	Profiles            int       `json:"profiles"`
	NoteTweetsMerged    int       `json:"note_tweets_merged"`
	NoteTweetsUnmatched int       `json:"note_tweets_unmatched"`
	LikesWithoutText    int       `json:"likes_without_text"`
	StartedAt           time.Time `json:"started_at"`
	FinishedAt          time.Time `json:"finished_at"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	base, err := ckstore.Open(ctx, ckstore.Options{Path: path, MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		return nil, err
	}
	s := &Store{base: base, db: base.DB(), path: base.Path()}
	if err := s.migrate(ctx); err != nil {
		_ = base.Close()
		return nil, err
	}
	return s, nil
}

func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	base, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	return &Store{base: base, db: base.DB(), path: base.Path()}, nil
}

func (s *Store) Close() error { return s.base.Close() }
func (s *Store) Path() string { return s.path }

func (s *Store) migrate(ctx context.Context) error {
	current, err := userVersion(ctx, s.db)
	if err != nil {
		return err
	}
	if current > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, schemaVersion)
	}
	if current == schemaVersion {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if current < 1 {
		if _, err := tx.ExecContext(ctx, schemaSQL); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func userVersion(ctx context.Context, db *sql.DB) (int, error) {
	var version int
	err := db.QueryRowContext(ctx, "pragma user_version").Scan(&version)
	return version, err
}

func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	return userVersion(ctx, s.db)
}

func (s *Store) ImportArchive(ctx context.Context, batch ImportBatch) (ImportStats, error) {
	now := batch.ImportedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	stats := ImportStats{StartedAt: now, FinishedAt: now}
	err := s.base.WithTx(ctx, func(tx *sql.Tx) error {
		if err := upsertProfiles(ctx, tx, batch.Profiles, now); err != nil {
			return err
		}
		if err := upsertTweets(ctx, tx, batch.Tweets, now); err != nil {
			return err
		}
		if err := upsertRoles(ctx, tx, batch.Roles, now); err != nil {
			return err
		}
		coverage := ""
		if !batch.CoverageThrough.IsZero() {
			coverage = formatUTC(batch.CoverageThrough)
		}
		_, err := tx.ExecContext(ctx, `insert into sync_state(kind,cursor,last_sync_at,last_result,coverage_note)
values('archive_import',?,?,'ok','')
on conflict(kind) do update set cursor=excluded.cursor,last_sync_at=excluded.last_sync_at,last_result=excluded.last_result`,
			coverage, formatUTC(now))
		return err
	})
	if err != nil {
		return ImportStats{}, err
	}
	stats.Tweets = len(batch.Tweets)
	stats.Profiles = len(batch.Profiles)
	for _, role := range batch.Roles {
		switch role.Role {
		case "authored":
			stats.Authored++
		case "like":
			stats.LikesSeen++
		}
	}
	return stats, nil
}

func upsertProfiles(ctx context.Context, tx *sql.Tx, profiles []Profile, now time.Time) error {
	for _, p := range profiles {
		if strings.TrimSpace(p.AuthorID) == "" {
			continue
		}
		lastSeen := p.LastSeenAt
		if lastSeen.IsZero() {
			lastSeen = now
		}
		_, err := tx.ExecContext(ctx, `insert into profiles(author_id,handle,display_name,last_seen_at)
values(?,?,?,?)
on conflict(author_id) do update set
handle=coalesce(nullif(excluded.handle,''), profiles.handle),
display_name=coalesce(nullif(excluded.display_name,''), profiles.display_name),
last_seen_at=excluded.last_seen_at`,
			p.AuthorID, p.Handle, p.DisplayName, formatUTC(lastSeen))
		if err != nil {
			return err
		}
	}
	return nil
}

func rollback(tx *sql.Tx) { _ = tx.Rollback() }
