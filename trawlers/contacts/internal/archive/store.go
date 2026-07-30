package archive

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("archive path is required")
	}
	st, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		return nil, err
	}
	out, err := Use(ctx, st, path)
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	out.owned = true
	return out, nil
}

func Use(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
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

func UseExisting(ctx context.Context, st *ckstore.Store, path string) (*Store, error) {
	if st == nil {
		return nil, errors.New("archive store is not open")
	}
	if strings.TrimSpace(path) == "" {
		path = st.Path()
	}
	return &Store{store: st, path: path}, nil
}

func ensureCurrentSchema(ctx context.Context, st *ckstore.Store) error {
	_, err := st.DB().ExecContext(ctx, schema+state.Schema+shortref.Schema)
	return err
}

func (s *Store) Close() error {
	if s == nil || s.store == nil || !s.owned {
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

func (s *Store) DB() *sql.DB {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.DB()
}

type database interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (s *Store) database() database {
	if s.tx != nil {
		return s.tx
	}
	return s.store.DB()
}

// withTransaction lets compound archive operations reuse the same Store API
// without allowing nested helpers to commit independently.
func (s *Store) withTransaction(ctx context.Context, fn func(*Store) error) error {
	if s.tx != nil {
		return fn(s)
	}
	return s.store.WithTx(ctx, func(tx *sql.Tx) error {
		scoped := *s
		scoped.tx = tx
		return fn(&scoped)
	})
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
