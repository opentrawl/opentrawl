package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckstate "github.com/opentrawl/opentrawl/trawlkit/state"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

var ErrTweetNotFound = errors.New("tweet not found")

// ErrSchemaOutdated means this archive predates schemaVersion 2 (the
// sync_state migration) and was opened read-only, so migrate() never ran.
// Migration writes DDL and only runs on a writable Open — status
// deliberately use OpenReadOnly so they never contend for the write lock
// with a running sync. The remedy is the same one sync/import already
// does on every writable open: nothing to run by hand.
var ErrSchemaOutdated = errors.New("archive schema predates this version; run trawl twitter sync")

type Store struct {
	base *ckstore.Store
	db   *sql.DB
	path string
	log  *cklog.Run
	owns bool
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
	return open(ctx, path, nil)
}

func OpenWithLog(ctx context.Context, path string, run *cklog.Run) (*Store, error) {
	return open(ctx, path, run)
}

func open(ctx context.Context, path string, run *cklog.Run) (*Store, error) {
	base, err := ckstore.Open(ctx, ckstore.Options{Path: path, MaxOpenConns: 1, MaxIdleConns: 1})
	if err != nil {
		return nil, err
	}
	s := &Store{base: base, db: base.DB(), path: base.Path(), log: run, owns: true}
	if err := s.migrate(ctx); err != nil {
		_ = base.Close()
		return nil, err
	}
	return s, nil
}

func Use(ctx context.Context, base *ckstore.Store, run *cklog.Run) (*Store, error) {
	if base == nil {
		return nil, errors.New("store is required")
	}
	s := &Store{base: base, db: base.DB(), path: base.Path(), log: run}
	if err := s.migrate(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func OpenReadOnly(ctx context.Context, path string) (*Store, error) {
	base, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	outdated, err := hasLegacySyncState(ctx, base.DB())
	if err != nil {
		_ = base.Close()
		return nil, err
	}
	if outdated {
		_ = base.Close()
		return nil, ErrSchemaOutdated
	}
	return &Store{base: base, db: base.DB(), path: base.Path(), owns: true}, nil
}

func UseExisting(ctx context.Context, base *ckstore.Store, run *cklog.Run) (*Store, error) {
	if base == nil {
		return nil, errors.New("store is required")
	}
	outdated, err := hasLegacySyncState(ctx, base.DB())
	if err != nil {
		return nil, err
	}
	if outdated {
		return nil, ErrSchemaOutdated
	}
	return &Store{base: base, db: base.DB(), path: base.Path(), log: run}, nil
}

// hasLegacySyncState is a cheap, read-only structural check — no write, no
// lock contention with a concurrent sync — that tells a not-yet-migrated
// archive apart from a current one.
func hasLegacySyncState(ctx context.Context, db *sql.DB) (bool, error) {
	var legacyColumn int
	err := db.QueryRowContext(ctx, `select count(*) from pragma_table_info('sync_state') where name = 'kind'`).Scan(&legacyColumn)
	return legacyColumn > 0, err
}

func (s *Store) Close() error {
	if s == nil || !s.owns {
		return nil
	}
	return s.base.Close()
}
func (s *Store) Path() string { return s.path }
func (s *Store) SetLog(run *cklog.Run) {
	s.log = run
}

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
	migrated := 0
	if current < 2 {
		migrated, err = migrateLegacySyncState(ctx, tx, current)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("pragma user_version = %d", schemaVersion)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// Logged only after commit: the version bump and the copied rows are
	// either both durable or both rolled back, so this line is never
	// printed for a migration that did not actually happen.
	if migrated > 0 {
		_ = s.log.Info("sync_state_migrated", fmt.Sprintf("rows=%d", migrated))
	}
	return nil
}

// migrateLegacySyncState is the one-time, value-preserving move off
// birdcrawl's old sync_state(kind, cursor, last_sync_at, last_result,
// coverage_note) table onto trawlkit's canonical state.Schema. It runs
// inside the same transaction as the version bump, so a crash never
// leaves the archive between shapes.
//
// This cannot be a blind drop-and-recreate tombstone like the other
// crawlers' sync-state adoptions: birdcrawl's sync_state holds a real
// financial ledger (the spend:<month> cursor, a monotonic count of X API
// dollars already spent, enforced by the per-request budget guard) and
// pagination cursors whose only re-derivation path is a paid, from-zero
// re-crawl. Neither is data that one sync re-derives, so every row is read and
// rewritten before the old table is dropped. Dropping and recreating this
// migration would lose state that is both valuable and expensive to recover.
//
// currentVersion is the schema version this archive is upgrading FROM. A
// database already at version 0 (brand new) never had the legacy table,
// so there is nothing to read; it goes straight to the canonical schema.
func migrateLegacySyncState(ctx context.Context, tx *sql.Tx, currentVersion int) (int, error) {
	if currentVersion < 1 {
		if _, err := tx.ExecContext(ctx, ckstate.Schema); err != nil {
			return 0, err
		}
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `select kind, cursor, last_sync_at, last_result, coverage_note from sync_state`)
	if err != nil {
		return 0, err
	}
	type legacyRow struct {
		kind, cursor, lastSyncAt, lastResult, coverageNote string
	}
	var legacy []legacyRow
	for rows.Next() {
		var (
			kind                                         string
			cursor, lastSyncAt, lastResult, coverageNote sql.NullString
		)
		if err := rows.Scan(&kind, &cursor, &lastSyncAt, &lastResult, &coverageNote); err != nil {
			_ = rows.Close()
			return 0, err
		}
		legacy = append(legacy, legacyRow{
			kind:         kind,
			cursor:       cursor.String,
			lastSyncAt:   lastSyncAt.String,
			lastResult:   lastResult.String,
			coverageNote: coverageNote.String,
		})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	if _, err := tx.ExecContext(ctx, `drop table sync_state`); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, ckstate.Schema); err != nil {
		return 0, err
	}

	for _, row := range legacy {
		// Canonical state.Store.Get parses updated_at as RFC3339Nano and
		// errors on a blank or malformed string, unlike the old
		// parseStoredTime, which silently treated ANY parse failure
		// (blank or otherwise corrupt) as zero time. A legacy row must
		// still migrate to something parseable, or every future read of
		// that kind would error where it used to succeed with a zero
		// time. UnknownTimeRFC3339 is exactly that: birdcrawl's own
		// existing spelling of "no timestamp" (formatUTC(time.Time{})),
		// and it parses back to the same zero time any unparseable value
		// always decoded to, so no reader observes a behavior change —
		// see TestMigrateLegacySyncStateBlankLastSyncAt.
		updatedAt := row.lastSyncAt
		if _, err := time.Parse(time.RFC3339Nano, updatedAt); err != nil {
			updatedAt = UnknownTimeRFC3339
		}
		for _, cell := range []struct{ entityType, value string }{
			{stateEntityCursor, row.cursor},
			{stateEntityLastResult, row.lastResult},
			{stateEntityCoverageNote, row.coverageNote},
		} {
			if _, err := tx.ExecContext(ctx, `
insert into sync_state(source_name, entity_type, entity_id, value, updated_at)
values (?, ?, ?, ?, ?)
on conflict(source_name, entity_type, entity_id) do update set
  value = excluded.value,
  updated_at = excluded.updated_at`,
				stateSourceName, cell.entityType, row.kind, cell.value, updatedAt); err != nil {
				return 0, err
			}
		}
	}
	return len(legacy), nil
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
		// The old hand-rolled SQL's ON CONFLICT clause never touched
		// coverage_note after the first insert for this kind, so a real
		// historical value can be sitting there; upsertSyncState writes
		// all three canonical rows unconditionally, so it must be told
		// the existing value explicitly or a later import would erase it.
		existing, err := syncStateWithin(ctx, tx, "archive_import")
		if err != nil {
			return err
		}
		return upsertSyncState(ctx, tx, SyncStateUpdate{
			Kind:         "archive_import",
			Cursor:       coverage,
			LastSyncAt:   now,
			LastResult:   "ok",
			CoverageNote: existing.CoverageNote,
		})
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
