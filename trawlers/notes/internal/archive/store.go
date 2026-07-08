package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/openclaw/crawlkit/config"
	"github.com/openclaw/crawlkit/store"
)

var ErrSchemaOutdated = errors.New("archive schema predates this version")

type Store struct {
	store *store.Store
	path  string
	owned bool
}

func DefaultPaths() config.Paths {
	paths, _ := config.App{Name: AppID, BaseDir: "~/.opentrawl/" + AppID}.DefaultPaths()
	return paths
}

func Exists(path string) bool {
	if path == "" {
		path = DefaultPaths().DBPath
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		path = DefaultPaths().DBPath
	}
	st, err := store.Open(ctx, store.Options{Path: path, Schema: schema, SchemaVersion: SchemaVersion})
	if err != nil {
		return nil, err
	}
	return &Store{store: st, path: path, owned: true}, nil
}

func Use(ctx context.Context, st *store.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	if _, err := st.DB().ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := st.EnsureSchemaVersion(ctx, SchemaVersion); err != nil {
		return nil, err
	}
	return &Store{store: st, path: path}, nil
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
	if s == nil || s.store == nil || !s.owned {
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

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
