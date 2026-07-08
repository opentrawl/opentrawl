package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/config"
	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/state"
	ckstore "github.com/openclaw/crawlkit/store"
)

const (
	sourceName          = "gogcrawl"
	syncEntityType      = "sync"
	lastStartedEntityID = "last_started_at"
	lastDoneEntityID    = "last_completed_at"
)

var ErrSchemaMismatch = errors.New("archive schema version does not match this gogcrawl version")

type Store struct {
	store *ckstore.Store
	path  string
	owned bool
}

// DefaultPaths is the one archive path layout, from crawlkit/config. The base
// dir is the fleet-wide state root, ~/.opentrawl/gogcrawl (TRAWL-99).
func DefaultPaths() config.Paths {
	paths, _ := (config.App{Name: "gogcrawl", BaseDir: "~/.opentrawl/gogcrawl"}).DefaultPaths()
	return paths
}

func DefaultPath() string {
	return DefaultPaths().DBPath
}

func Exists(path string) bool {
	if path == "" {
		path = DefaultPath()
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	st, err := ckstore.Open(ctx, ckstore.Options{
		Path:          path,
		Schema:        schema + state.Schema + shortref.Schema,
		SchemaVersion: schemaVersion,
	})
	if err != nil {
		return nil, err
	}
	return &Store{store: st, path: path, owned: true}, nil
}

func Use(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	if _, err := st.DB().ExecContext(ctx, schema+state.Schema+shortref.Schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := st.EnsureSchemaVersion(ctx, schemaVersion); err != nil {
		return nil, err
	}
	return &Store{store: st, path: path}, nil
}

func UseExisting(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	if version != schemaVersion {
		return nil, ErrSchemaMismatch
	}
	return &Store{store: st, path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.store == nil {
		return nil
	}
	if !s.owned {
		return nil
	}
	return s.store.Close()
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) InsertMessages(ctx context.Context, messages []Message) (InsertResult, error) {
	result := InsertResult{Seen: len(messages)}
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		for _, msg := range messages {
			inserted, err := insertMessage(ctx, tx, msg)
			if err != nil {
				return err
			}
			if inserted {
				result.Inserted++
			}
		}
		return nil
	})
	return result, err
}

func (s *Store) CountMessages(ctx context.Context) (int64, error) {
	return countTable(ctx, s.store.DB(), "messages")
}

func (s *Store) MarkSyncStarted(ctx context.Context, when time.Time) error {
	return state.New(s.store.DB()).Set(ctx, sourceName, syncEntityType, lastStartedEntityID, when.UTC().Format(time.RFC3339))
}

func (s *Store) MarkSyncCompleted(ctx context.Context, when time.Time) error {
	return state.New(s.store.DB()).Set(ctx, sourceName, syncEntityType, lastDoneEntityID, when.UTC().Format(time.RFC3339))
}

func (s *Store) SyncMarkers(ctx context.Context) (SyncMarkers, error) {
	stateStore := state.New(s.store.DB())
	started, hasStarted, err := stateStore.Get(ctx, sourceName, syncEntityType, lastStartedEntityID)
	if err != nil {
		return SyncMarkers{}, err
	}
	done, hasDone, err := stateStore.Get(ctx, sourceName, syncEntityType, lastDoneEntityID)
	if err != nil {
		return SyncMarkers{}, err
	}
	markers := SyncMarkers{HasCompleted: hasDone}
	if hasDone {
		markers.LastCompletedAt = recordTime(done)
	}
	if hasStarted && (!hasDone || recordTime(started).After(recordTime(done))) {
		markers.PreviousRunIncomplete = true
	}
	return markers, nil
}

func recordTime(rec state.Record) time.Time {
	parsed, err := time.Parse(time.RFC3339, rec.Value)
	if err == nil {
		return parsed
	}
	return rec.UpdatedAt
}

func insertMessage(ctx context.Context, tx *sql.Tx, msg Message) (bool, error) {
	result, err := insertMessageWithTiming(ctx, tx, msg)
	return result.Inserted, err
}

type insertMessageResult struct {
	Inserted     bool
	IndexElapsed time.Duration
}

func insertMessageWithTiming(ctx context.Context, tx *sql.Tx, msg Message) (insertMessageResult, error) {
	timeText, timeUnix := "", int64(0)
	if when := msg.Time; !when.IsZero() {
		timeText = formatArchiveTime(when)
		timeUnix = when.Unix()
	}
	var existed int
	if err := tx.QueryRowContext(ctx, `select count(*) from messages where id = ?`, msg.ID).Scan(&existed); err != nil {
		return insertMessageResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `delete from messages_fts where id = ?`, msg.ID); err != nil {
		return insertMessageResult{}, fmt.Errorf("delete message fts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `delete from attachments where message_id = ?`, msg.ID); err != nil {
		return insertMessageResult{}, fmt.Errorf("delete attachments: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `delete from message_participants where message_id = ?`, msg.ID); err != nil {
		return insertMessageResult{}, fmt.Errorf("delete message participants: %w", err)
	}
	_, err := tx.ExecContext(ctx, `
insert into messages(
  id, thread_id, history_id, internal_date_ms, time, time_unix, from_name, from_address, to_address, cc_address, subject, body, labels_json
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
on conflict(id) do update set
  thread_id = excluded.thread_id,
  history_id = excluded.history_id,
  internal_date_ms = excluded.internal_date_ms,
  time = excluded.time,
  time_unix = excluded.time_unix,
  from_name = excluded.from_name,
  from_address = excluded.from_address,
  to_address = excluded.to_address,
  cc_address = excluded.cc_address,
  subject = excluded.subject,
  body = excluded.body,
  labels_json = excluded.labels_json
`, msg.ID, msg.ThreadID, msg.HistoryID, msg.InternalDateMS, timeText, timeUnix, msg.FromName, msg.FromAddress, msg.ToAddress, msg.CcAddress, msg.Subject, msg.Body, labelsJSON(msg.Labels))
	if err != nil {
		return insertMessageResult{}, fmt.Errorf("insert message: %w", err)
	}
	indexStarted := time.Now()
	if _, err := tx.ExecContext(ctx, `insert into messages_fts(id, subject, body) values (?, ?, ?)`, msg.ID, msg.Subject, msg.Body); err != nil {
		return insertMessageResult{}, fmt.Errorf("insert message fts: %w", err)
	}
	indexElapsed := time.Since(indexStarted)
	for _, attachment := range msg.Attachments {
		if _, err := tx.ExecContext(ctx, `
insert into attachments(message_id, filename, mime_type, size_bytes)
values (?, ?, ?, ?)
`, msg.ID, attachment.Filename, attachment.MIMEType, attachment.Size); err != nil {
			return insertMessageResult{}, fmt.Errorf("insert attachment: %w", err)
		}
	}
	if err := insertMessageParticipants(ctx, tx, msg); err != nil {
		return insertMessageResult{}, err
	}
	return insertMessageResult{Inserted: existed == 0, IndexElapsed: indexElapsed}, nil
}
