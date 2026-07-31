package archive

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	updateSource         = "calendar"
	updateEntity         = "update"
	updateStatus         = "status"
	updateRunID          = "run_id"
	updateLastUpdate     = "last_update_at"
	updateSourceModified = "source_modified_at"
	completeState        = "complete"
)

type Store struct {
	store *store.Store
	path  string
	owned bool
}

// DefaultPaths is the one archive path layout, from trawlkit/config. The base
// dir is the fleet-wide state root, ~/.opentrawl/calendar.
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
	return &Store{store: st, path: path}, nil
}

func ensureCurrentSchema(ctx context.Context, st *store.Store) error {
	_, err := st.DB().ExecContext(ctx, schema+state.Schema+shortref.Schema)
	return err
}

func UseExisting(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
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
