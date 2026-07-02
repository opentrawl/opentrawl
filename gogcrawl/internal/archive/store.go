package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/openclaw/crawlkit/config"
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
}

func DefaultPath() string {
	paths, err := DefaultPaths()
	if err != nil {
		return filepath.Join(".gogcrawl", "gogcrawl.db")
	}
	return paths.DBPath
}

func DefaultPaths() (config.Paths, error) {
	return (config.App{Name: "gogcrawl", BaseDir: "~/.gogcrawl"}).DefaultPaths()
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
		Schema:        schema + state.Schema,
		SchemaVersion: schemaVersion,
	})
	if err != nil {
		return nil, err
	}
	return &Store{store: st, path: path}, nil
}

func OpenExisting(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	st, err := ckstore.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if version != schemaVersion {
		_ = st.Close()
		return nil, ErrSchemaMismatch
	}
	return &Store{store: st, path: path}, nil
}

func (s *Store) Close() error {
	if s == nil || s.store == nil {
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
	// A rare message arrives with no readable date; archive it dateless
	// ('' and 0, rendered as no time on read) rather than losing it or
	// halting the crawl.
	timeText, timeUnix := "", int64(0)
	if when := msg.Time; !when.IsZero() {
		timeText = formatArchiveTime(when)
		timeUnix = when.Unix()
	}
	result, err := tx.ExecContext(ctx, `
insert or ignore into messages(
  id, thread_id, time, time_unix, from_name, from_address, to_address, subject, body, labels_json
) values (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, msg.ID, msg.ThreadID, timeText, timeUnix, msg.FromName, msg.FromAddress, msg.ToAddress, msg.Subject, msg.Body, labelsJSON(msg.Labels))
	if err != nil {
		return false, fmt.Errorf("insert message: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `insert into messages_fts(id, subject, body) values (?, ?, ?)`, msg.ID, msg.Subject, msg.Body); err != nil {
		return false, fmt.Errorf("insert message fts: %w", err)
	}
	return true, nil
}
