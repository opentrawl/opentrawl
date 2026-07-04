package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

type SyncState struct {
	Kind         string
	Cursor       string
	LastSyncAt   time.Time
	LastResult   string
	CoverageNote string
}

type SyncStateUpdate struct {
	Kind         string
	Cursor       string
	LastSyncAt   time.Time
	LastResult   string
	CoverageNote string
}

type LivePage struct {
	Tweets      []Tweet
	Roles       []Role
	Profiles    []Profile
	States      []SyncStateUpdate
	SpendMonth  string
	SpendMicros int64
	SyncedAt    time.Time
}

func (s *Store) SyncState(ctx context.Context, kind string) (SyncState, error) {
	var state SyncState
	var lastSyncAt string
	err := s.db.QueryRowContext(ctx, `select kind,cursor,last_sync_at,last_result,coverage_note
from sync_state where kind = ?`, kind).Scan(&state.Kind, &state.Cursor, &lastSyncAt, &state.LastResult, &state.CoverageNote)
	if err == sql.ErrNoRows {
		return SyncState{Kind: kind}, nil
	}
	if err != nil {
		return SyncState{}, err
	}
	state.LastSyncAt = parseStoredTime(lastSyncAt)
	return state, nil
}

func (s *Store) CommitLivePage(ctx context.Context, page LivePage) error {
	now := page.SyncedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return s.base.WithTx(ctx, func(tx *sql.Tx) error {
		if err := upsertProfiles(ctx, tx, page.Profiles, now); err != nil {
			return err
		}
		if err := upsertTweets(ctx, tx, page.Tweets, now); err != nil {
			return err
		}
		if err := upsertRoles(ctx, tx, page.Roles, now); err != nil {
			return err
		}
		for _, state := range page.States {
			if state.LastSyncAt.IsZero() {
				state.LastSyncAt = now
			}
			if err := upsertSyncState(ctx, tx, state); err != nil {
				return err
			}
		}
		if page.SpendMicros > 0 {
			return addSpend(ctx, tx, page.SpendMonth, page.SpendMicros, now)
		}
		return nil
	})
}

func (s *Store) AddSpend(ctx context.Context, month string, micros int64, at time.Time) error {
	if micros <= 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return s.base.WithTx(ctx, func(tx *sql.Tx) error {
		return addSpend(ctx, tx, month, micros, at)
	})
}

func (s *Store) SpendMicros(ctx context.Context, month string) (int64, error) {
	state, err := s.SyncState(ctx, "spend:"+month)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(state.Cursor) == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(state.Cursor, 10, 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func (s *Store) SetAuthTokenValid(ctx context.Context, valid bool, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	cursor := "false"
	if valid {
		cursor = "true"
	}
	return s.CommitLivePage(ctx, LivePage{SyncedAt: at, States: []SyncStateUpdate{{
		Kind:       "auth:token_valid",
		Cursor:     cursor,
		LastResult: cursor,
	}}})
}

func (s *Store) HasRole(ctx context.Context, tweetID, role string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `select 1 from tweet_roles where tweet_id = ? and role = ? limit 1`, tweetID, role).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// StalestAuthored bounds metric-refresh spend: only tweets from the last 90
// days (engagement on older tweets has settled) whose counts are missing or
// more than 7 days old, stalest first. At a daily sync this converges to a
// weekly rotation over recent tweets rather than $6/month of perpetual
// re-lookups across the whole archive.
func (s *Store) StalestAuthored(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	now := time.Now().UTC()
	createdSince := formatUTC(now.AddDate(0, 0, -90))
	staleBefore := formatUTC(now.AddDate(0, 0, -7))
	rows, err := s.db.QueryContext(ctx, `select t.id from tweets t
join tweet_roles r on r.tweet_id = t.id and r.role = 'authored'
where t.created_at >= ?
and (t.metrics_fetched_at is null or t.metrics_fetched_at = '' or t.metrics_fetched_at <= ?)
order by case when t.metrics_fetched_at is null or t.metrics_fetched_at = '' then 0 else 1 end,
t.metrics_fetched_at asc, t.created_at desc, t.id desc
limit ?`, createdSince, staleBefore, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func upsertSyncState(ctx context.Context, tx *sql.Tx, state SyncStateUpdate) error {
	if strings.TrimSpace(state.Kind) == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx, `insert into sync_state(kind,cursor,last_sync_at,last_result,coverage_note)
values(?,?,?,?,?)
on conflict(kind) do update set
cursor=excluded.cursor,
last_sync_at=excluded.last_sync_at,
last_result=excluded.last_result,
coverage_note=excluded.coverage_note`,
		state.Kind, state.Cursor, formatUTC(state.LastSyncAt), state.LastResult, state.CoverageNote)
	return err
}

func addSpend(ctx context.Context, tx *sql.Tx, month string, micros int64, at time.Time) error {
	kind := "spend:" + strings.TrimSpace(month)
	if kind == "spend:" {
		return nil
	}
	var current int64
	var raw string
	err := tx.QueryRowContext(ctx, `select cursor from sync_state where kind = ?`, kind).Scan(&raw)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if raw != "" {
		current, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
	}
	return upsertSyncState(ctx, tx, SyncStateUpdate{
		Kind:       kind,
		Cursor:     strconv.FormatInt(current+micros, 10),
		LastSyncAt: at,
		LastResult: "ok",
	})
}
