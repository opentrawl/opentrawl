package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/openclaw/crawlkit/config"
	"github.com/openclaw/crawlkit/shortref"
	"github.com/openclaw/crawlkit/state"
	"github.com/openclaw/crawlkit/store"
)

const (
	syncSource         = "calcrawl"
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
}

// DefaultPaths is the one archive path layout, from crawlkit/config. The base
// dir is the fleet-wide state root, ~/.opentrawl/calcrawl (TRAWL-99).
func DefaultPaths() config.Paths {
	paths, _ := config.App{Name: "calcrawl", BaseDir: "~/.opentrawl/calcrawl"}.DefaultPaths()
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
	st, err := store.Open(ctx, store.Options{
		Path:          path,
		Schema:        schema + shortref.Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return nil, err
	}
	if err := state.EnsureSchema(ctx, st.DB()); err != nil {
		_ = st.Close()
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
	st, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	if version != SchemaVersion {
		_ = st.Close()
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaOutdated, version, SchemaVersion)
	}
	return &Store{store: st, path: path}, nil
}

func OpenExistingWritable(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	check, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	version, err := check.SchemaVersion(ctx)
	_ = check.Close()
	if err != nil {
		return nil, err
	}
	if version != SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaOutdated, version, SchemaVersion)
	}
	return Open(ctx, path)
}

func (s *Store) Close() error {
	if s == nil || s.store == nil {
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
