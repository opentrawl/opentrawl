package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/store"
)

type archiveColumnMigration struct {
	table      string
	column     string
	definition string
}

var archiveColumnMigrations = []archiveColumnMigration{
	{table: "model_observation", column: "stale_since", definition: "text"},
	{table: "model_observation", column: "stale_reason", definition: "text"},
	{table: "model_observation", column: "superseded_at", definition: "text"},
	{table: "place_observation", column: "stale_since", definition: "text"},
	{table: "place_observation", column: "stale_reason", definition: "text"},
	{table: "place_observation", column: "superseded_at", definition: "text"},
}

func openArchive(ctx context.Context, path string) (*store.Store, error) {
	db, err := store.Open(ctx, store.Options{
		Path:          path,
		Schema:        Schema,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return nil, err
	}
	if err := ensureArchiveMigrations(ctx, db.DB()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func openExistingArchive(ctx context.Context, path string) (*store.Store, error) {
	db, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	needsMigration, err := archiveMigrationsRequired(ctx, db.DB())
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if !needsMigration {
		return db, nil
	}
	_ = db.Close()

	writable, err := openArchive(ctx, path)
	if err != nil {
		return nil, err
	}
	_ = writable.Close()
	return store.OpenReadOnly(ctx, path)
}

func ensureArchiveMigrations(ctx context.Context, db *sql.DB) error {
	return ensureArchiveMigrationsBeforeAlter(ctx, db, nil)
}

func ensureArchiveMigrationsBeforeAlter(ctx context.Context, db *sql.DB, beforeAlter func(archiveColumnMigration) error) error {
	for _, migration := range archiveColumnMigrations {
		exists, err := tableColumnExists(ctx, db, migration.table, migration.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if beforeAlter != nil {
			if err := beforeAlter(migration); err != nil {
				return err
			}
		}
		if err := alterArchiveColumn(ctx, db, migration); err != nil {
			return err
		}
	}
	return nil
}

func archiveMigrationsRequired(ctx context.Context, db *sql.DB) (bool, error) {
	for _, migration := range archiveColumnMigrations {
		exists, err := tableColumnExists(ctx, db, migration.table, migration.column)
		if err != nil {
			return false, err
		}
		if !exists {
			return true, nil
		}
	}
	return false, nil
}

func alterArchiveColumn(ctx context.Context, db *sql.DB, migration archiveColumnMigration) error {
	query := "alter table " + store.QuoteIdent(migration.table) +
		" add column " + store.QuoteIdent(migration.column) + " " + migration.definition
	if _, err := db.ExecContext(ctx, query); err != nil {
		if !isDuplicateColumnError(err) {
			return fmt.Errorf("migrate %s.%s: %w", migration.table, migration.column, err)
		}
		exists, existsErr := tableColumnExists(ctx, db, migration.table, migration.column)
		if existsErr != nil {
			return fmt.Errorf("verify duplicate migration %s.%s: %w", migration.table, migration.column, existsErr)
		}
		if exists {
			return nil
		}
		return fmt.Errorf("migrate %s.%s: %w", migration.table, migration.column, err)
	}
	return nil
}

func isDuplicateColumnError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}

func tableColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "pragma table_info("+store.QuoteIdent(table)+")")
	if err != nil {
		return false, fmt.Errorf("inspect %s columns: %w", table, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return false, fmt.Errorf("scan %s columns: %w", table, err)
		}
		if strings.EqualFold(name, column) {
			return true, nil
		}
	}
	return false, rows.Err()
}
