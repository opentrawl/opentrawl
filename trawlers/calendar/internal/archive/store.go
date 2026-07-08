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
	"github.com/openclaw/crawlkit/store"
)

const (
	syncSource         = "calendar"
	legacySyncSource   = "calcrawl"
	syncEntity         = "sync"
	syncStatus         = "status"
	syncRunID          = "run_id"
	syncLastSync       = "last_sync_at"
	syncSourceModified = "source_modified_at"
	completeState      = "complete"
)

var ErrSchemaOutdated = errors.New("archive schema predates this version")

type Store struct {
	store *store.Store
	path  string
	owned bool
}

// DefaultPaths is the one archive path layout, from crawlkit/config. The base
// dir is the fleet-wide state root, ~/.opentrawl/calendar (TRAWL-99).
func DefaultPaths() config.Paths {
	paths, _ := config.App{Name: "calendar", BaseDir: "~/.opentrawl/calendar"}.DefaultPaths()
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
	st, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	if err := ensureCurrentSchema(ctx, st); err != nil {
		_ = st.Close()
		return nil, err
	}
	if err := state.EnsureSchema(ctx, st.DB()); err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Store{store: st, path: path, owned: true}, nil
}

func OpenExistingWritable(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	return Open(ctx, path)
}

func Use(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	if err := ensureCurrentSchema(ctx, st); err != nil {
		return nil, err
	}
	if err := state.EnsureSchema(ctx, st.DB()); err != nil {
		return nil, err
	}
	return &Store{store: st, path: path}, nil
}

func ensureCurrentSchema(ctx context.Context, st *store.Store) error {
	current, err := st.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if current > SchemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, SchemaVersion)
	}
	fresh, err := archiveTablesAbsent(ctx, st.DB())
	if err != nil {
		return err
	}
	if _, err := st.DB().ExecContext(ctx, schema+shortref.Schema); err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	if current == SchemaVersion {
		return nil
	}
	if current == 0 && fresh {
		return st.EnsureSchemaVersion(ctx, SchemaVersion)
	}
	return migrateSchema(ctx, st, current)
}

func archiveTablesAbsent(ctx context.Context, db *sql.DB) (bool, error) {
	var count int
	if err := db.QueryRowContext(ctx, `select count(*) from sqlite_master where type = 'table' and name = 'calendars'`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func migrateSchema(ctx context.Context, st *store.Store, current int) error {
	return st.WithTx(ctx, func(tx *sql.Tx) error {
		if current < 3 {
			for _, stmt := range []string{
				`alter table calendars add column meaning text default ''`,
				`alter table calendars add column meaning_stated_at text default ''`,
				`alter table events add column availability integer`,
			} {
				if _, err := tx.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("migrate schema: %w", err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `create table if not exists schema_migrations(version integer not null)`); err != nil {
			return fmt.Errorf("ensure schema_migrations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `delete from schema_migrations`); err != nil {
			return fmt.Errorf("clear schema version: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `insert into schema_migrations(version) values(?)`, SchemaVersion); err != nil {
			return fmt.Errorf("write schema version: %w", err)
		}
		return nil
	})
}

func UseExisting(ctx context.Context, st *store.Store, path string) (*Store, error) {
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
	if version != SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaOutdated, version, SchemaVersion)
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

func (s *Store) DB() *sql.DB {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.DB()
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func NewRunID() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
