package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

var ErrSchemaOutdated = errors.New("archive schema predates this version")

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
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		return nil, err
	}
	if version != SchemaVersion {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrSchemaOutdated, version, SchemaVersion)
	}
	return &Store{store: st, path: path}, nil
}

func ensureCurrentSchema(ctx context.Context, st *ckstore.Store) error {
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
	if err := db.QueryRowContext(ctx, `select count(*) from sqlite_master where type = 'table' and name = 'people'`).Scan(&count); err != nil {
		return false, err
	}
	return count == 0, nil
}

func migrateSchema(ctx context.Context, st *ckstore.Store, current int) error {
	return st.WithTx(ctx, func(tx *sql.Tx) error {
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

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
