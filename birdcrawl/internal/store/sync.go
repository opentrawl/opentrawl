package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"

	ckstate "github.com/openclaw/crawlkit/state"
)

// Sync state lives in the one crawlkit state.Store (TRAWL-82), keyed by
// birdcrawl's old five-column sync_state "kind": each kind becomes three
// canonical rows (cursor, last_result, coverage_note) sharing that kind as
// entity_id, so the exact shape SyncState/SyncStateUpdate already expose
// survives the move untouched. See migrateLegacySyncState in store.go for
// the one-time copy off the old table.
const (
	stateSourceName         = "birdcrawl"
	stateEntityCursor       = "sync"
	stateEntityLastResult   = "sync_last_result"
	stateEntityCoverageNote = "sync_coverage_note"
)

// queryExecer is the read/write surface crawlkit/state.Store needs. Both
// *sql.DB and *sql.Tx satisfy it, so the same sync-state helpers work for a
// plain read and for a write inside an existing transaction (addSpend must
// read the running total with the write's own tx, not a second connection,
// or it would deadlock against ckstore's single-connection pool).
type queryExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

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
	return syncStateWithin(ctx, s.db, kind)
}

func syncStateWithin(ctx context.Context, q queryExecer, kind string) (SyncState, error) {
	st := ckstate.New(q)
	cursor, ok, err := st.Get(ctx, stateSourceName, stateEntityCursor, kind)
	if err != nil {
		return SyncState{}, err
	}
	if !ok {
		return SyncState{Kind: kind}, nil
	}
	result, _, err := st.Get(ctx, stateSourceName, stateEntityLastResult, kind)
	if err != nil {
		return SyncState{}, err
	}
	note, _, err := st.Get(ctx, stateSourceName, stateEntityCoverageNote, kind)
	if err != nil {
		return SyncState{}, err
	}
	return SyncState{
		Kind:         kind,
		Cursor:       cursor.Value,
		LastSyncAt:   cursor.UpdatedAt,
		LastResult:   result.Value,
		CoverageNote: note.Value,
	}, nil
}

func (s *Store) CommitLivePage(ctx context.Context, page LivePage) error {
	now := page.SyncedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	err := s.base.WithTx(ctx, func(tx *sql.Tx) error {
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
	if err != nil {
		return err
	}
	if len(tweetFullRefs(page.Tweets)) == 0 {
		return nil
	}
	return s.RebuildShortRefs(ctx)
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
	defer func() { _ = rows.Close() }()
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
	when := state.LastSyncAt
	st := ckstate.NewWithClock(tx, func() time.Time { return when })
	if err := st.Set(ctx, stateSourceName, stateEntityCursor, state.Kind, state.Cursor); err != nil {
		return err
	}
	if err := st.Set(ctx, stateSourceName, stateEntityLastResult, state.Kind, state.LastResult); err != nil {
		return err
	}
	return st.Set(ctx, stateSourceName, stateEntityCoverageNote, state.Kind, state.CoverageNote)
}

func addSpend(ctx context.Context, tx *sql.Tx, month string, micros int64, at time.Time) error {
	kind := "spend:" + strings.TrimSpace(month)
	if kind == "spend:" {
		return nil
	}
	existing, err := syncStateWithin(ctx, tx, kind)
	if err != nil {
		return err
	}
	var current int64
	if raw := strings.TrimSpace(existing.Cursor); raw != "" {
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
