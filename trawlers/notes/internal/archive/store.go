package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

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
	openedStore, err := store.Open(ctx, store.Options{
		Path:   path,
		Schema: schema,
	})
	if err != nil {
		return nil, err
	}
	return &Store{store: openedStore, path: path, owned: true}, nil
}

func Use(ctx context.Context, openedStore *store.Store, path string) (*Store, error) {
	if openedStore == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = openedStore.Path()
	}
	if _, err := openedStore.DB().ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("apply current Notes archive schema: %w", err)
	}
	return &Store{store: openedStore, path: path}, nil
}

func UseExisting(ctx context.Context, openedStore *store.Store, path string) (*Store, error) {
	if openedStore == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = openedStore.Path()
	}
	return &Store{store: openedStore, path: path}, nil
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
