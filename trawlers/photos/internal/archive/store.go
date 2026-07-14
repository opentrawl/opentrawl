package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// ArchiveIncompatibleError means an existing archive cannot safely serve a
// read request until sync has updated it.
type ArchiveIncompatibleError struct{}

func (ArchiveIncompatibleError) Error() string {
	return "photos archive is incompatible"
}

type archiveColumnMigration struct {
	table      string
	column     string
	definition string
}

var archiveColumnMigrations = []archiveColumnMigration{
	{table: "crawl_snapshot", column: "completeness_state", definition: "text not null default 'legacy_unknown'"},
	{table: "crawl_snapshot", column: "completeness_evidence_json", definition: "text not null default '{}'"},
	{table: "asset", column: "source_state", definition: "text not null default 'current'"},
	{table: "asset", column: "first_missing_at", definition: "text"},
	{table: "asset", column: "source_deleted_at", definition: "text"},
	{table: "asset", column: "source_state_snapshot_id", definition: "text not null default ''"},
	{table: "asset", column: "first_card_blocked_at", definition: "text"},
	{table: "asset", column: "first_card_blocked_snapshot_id", definition: "text"},
	{table: "model_observation", column: "stale_since", definition: "text"},
	{table: "model_observation", column: "stale_reason", definition: "text"},
	{table: "model_observation", column: "superseded_at", definition: "text"},
	{table: "model_observation", column: "generation_id", definition: "text references model_generation(id)"},
	{table: "place_observation", column: "stale_since", definition: "text"},
	{table: "place_observation", column: "stale_reason", definition: "text"},
	{table: "place_observation", column: "superseded_at", definition: "text"},
	{table: "place_observation", column: "generation_id", definition: "text references model_generation(id)"},
	{table: "paid_call_stage_item", column: "custody_sha256", definition: "text not null default ''"},
	{table: "paid_call_claim", column: "custody_sha256", definition: "text not null default ''"},
}

func openArchive(ctx context.Context, path string) (*store.Store, error) {
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return nil, err
	}
	if err := prepareStore(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// PrepareArchive upgrades a supported older Photos archive before the runner
// opens its request store. It only uses the local OpenTrawl archive.
func PrepareArchive(ctx context.Context, path string) error {
	return prepareArchive(ctx, path, nil)
}

func prepareArchive(ctx context.Context, path string, beforeAlter func(archiveColumnMigration) error) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("photos archive path is required")
	}
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat photos archive: %w", err)
	}
	version, err := peekArchiveVersion(ctx, path)
	if err != nil {
		return err
	}
	if version == SchemaVersion {
		return nil
	}
	if version <= 0 || version > SchemaVersion {
		return ArchiveIncompatibleError{}
	}
	db, err := store.Open(ctx, store.Options{Path: path})
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	return prepareStoreBeforeAlter(ctx, db, beforeAlter)
}

func peekArchiveVersion(ctx context.Context, path string) (int, error) {
	db, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	return db.SchemaVersion(ctx)
}

func openExistingArchive(ctx context.Context, path string) (*store.Store, error) {
	db, err := store.OpenReadOnly(ctx, path)
	if err != nil {
		return nil, err
	}
	if err := validateReadStore(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func validateReadStore(ctx context.Context, db *store.Store) error {
	if db == nil {
		return errors.New("photos read store is required")
	}
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version == SchemaVersion {
		return nil
	}
	return ArchiveIncompatibleError{}
}

func ensureArchiveMigrations(ctx context.Context, db *sql.DB) error {
	return ensureArchiveMigrationsBeforeAlter(ctx, db, nil)
}

type archiveMigrationDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func prepareStore(ctx context.Context, db *store.Store) error {
	return prepareStoreBeforeAlter(ctx, db, nil)
}

func prepareStoreBeforeAlter(ctx context.Context, db *store.Store, beforeAlter func(archiveColumnMigration) error) error {
	if db == nil {
		return errors.New("archive store is not open")
	}
	version, err := db.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if version > SchemaVersion {
		return ArchiveIncompatibleError{}
	}
	return db.WithTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, Schema); err != nil {
			return fmt.Errorf("apply schema: %w", err)
		}
		if err := ensureArchiveMigrationsBeforeAlter(ctx, tx, beforeAlter); err != nil {
			return err
		}
		if _, err := ensureSearchIndexTx(ctx, tx); err != nil {
			return err
		}
		return writeSchemaVersion(ctx, tx)
	})
}

func writeSchemaVersion(ctx context.Context, db archiveMigrationDB) error {
	if _, err := db.ExecContext(ctx, `create table if not exists schema_migrations(version integer not null)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	if _, err := db.ExecContext(ctx, `delete from schema_migrations`); err != nil {
		return fmt.Errorf("clear schema version: %w", err)
	}
	if _, err := db.ExecContext(ctx, `insert into schema_migrations(version) values(?)`, SchemaVersion); err != nil {
		return fmt.Errorf("write schema version: %w", err)
	}
	return nil
}

func ensureArchiveMigrationsBeforeAlter(ctx context.Context, db archiveMigrationDB, beforeAlter func(archiveColumnMigration) error) error {
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
	return migrateFirstCardEligibility(ctx, db)
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
	return firstCardEligibilityMigrationRequired(ctx, db)
}

func alterArchiveColumn(ctx context.Context, db archiveMigrationDB, migration archiveColumnMigration) error {
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

func tableColumnExists(ctx context.Context, db archiveMigrationDB, table, column string) (bool, error) {
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
