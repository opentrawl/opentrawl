package densesearch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
)

const (
	freshnessLedgerFileName = "freshness.sqlite"
	buildingIndexFileName   = "dense.building.sqlite"
	buildLockFileName       = "dense.build.lock"
)

type SourceGeneration uint64

type SourceRequiredGeneration struct {
	RegisteredTrawler  string
	RequiredGeneration SourceGeneration
}

type SourceRefreshIntent struct {
	RegisteredTrawler          string
	PreviousRequiredGeneration SourceGeneration
}

func FreshnessLedgerPath(stateRoot string) string {
	return filepath.Join(stateRoot, indexDirectoryName, freshnessLedgerFileName)
}

func BuildingIndexPath(stateRoot string) string {
	return filepath.Join(stateRoot, indexDirectoryName, buildingIndexFileName)
}

func RequireSourceRefreshesBeforeUpdates(
	ctx context.Context,
	stateRoot string,
	registeredTrawlers []string,
) ([]SourceRefreshIntent, error) {
	registeredTrawlers = canonicalRegisteredTrawlers(registeredTrawlers)
	if len(registeredTrawlers) == 0 {
		return nil, nil
	}
	database, err := openFreshnessLedger(stateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	intents := make([]SourceRefreshIntent, 0, len(registeredTrawlers))
	for _, registeredTrawler := range registeredTrawlers {
		var previousGeneration uint64
		err := transaction.QueryRowContext(ctx, `
			select required_generation from searchable_source_generations
			where registered_trawler = ?`, registeredTrawler).Scan(&previousGeneration)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			_ = transaction.Rollback()
			return nil, err
		}
		if _, err := transaction.ExecContext(ctx, `
			insert into searchable_source_generations(registered_trawler, required_generation)
			values (?, 1)
			on conflict(registered_trawler) do update set
				required_generation = required_generation + 1`, registeredTrawler); err != nil {
			_ = transaction.Rollback()
			return nil, err
		}
		intents = append(intents, SourceRefreshIntent{
			RegisteredTrawler:          registeredTrawler,
			PreviousRequiredGeneration: SourceGeneration(previousGeneration),
		})
	}
	if err := transaction.Commit(); err != nil {
		return nil, err
	}
	return intents, nil
}

func RestoreRefreshIntentsForUnchangedSources(
	ctx context.Context,
	stateRoot string,
	intents []SourceRefreshIntent,
	unchangedRegisteredTrawlers []string,
) error {
	unchanged := make(map[string]struct{}, len(unchangedRegisteredTrawlers))
	for _, registeredTrawler := range canonicalRegisteredTrawlers(unchangedRegisteredTrawlers) {
		unchanged[registeredTrawler] = struct{}{}
	}
	if len(unchanged) == 0 {
		return nil
	}
	database, err := openFreshnessLedger(stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, intent := range intents {
		if _, found := unchanged[intent.RegisteredTrawler]; !found {
			continue
		}
		advancedGeneration := uint64(intent.PreviousRequiredGeneration) + 1
		if intent.PreviousRequiredGeneration == 0 {
			_, err = transaction.ExecContext(ctx, `
				delete from searchable_source_generations
				where registered_trawler = ? and required_generation = 1`, intent.RegisteredTrawler)
		} else {
			_, err = transaction.ExecContext(ctx, `
				update searchable_source_generations set required_generation = ?
				where registered_trawler = ? and required_generation = ?`,
				uint64(intent.PreviousRequiredGeneration), intent.RegisteredTrawler, advancedGeneration)
		}
		if err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	return transaction.Commit()
}

func EnsureSourcesHaveRequiredGenerations(
	ctx context.Context,
	stateRoot string,
	registeredTrawlers []string,
) error {
	registeredTrawlers = canonicalRegisteredTrawlers(registeredTrawlers)
	if len(registeredTrawlers) == 0 {
		return nil
	}
	database, err := openFreshnessLedger(stateRoot)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	for _, registeredTrawler := range registeredTrawlers {
		if _, err := transaction.ExecContext(ctx, `
			insert into searchable_source_generations(registered_trawler, required_generation)
			values (?, 1)
			on conflict(registered_trawler) do nothing`, registeredTrawler); err != nil {
			_ = transaction.Rollback()
			return err
		}
	}
	return transaction.Commit()
}

func RequiredSourceGenerations(
	ctx context.Context,
	stateRoot string,
	registeredTrawlers []string,
) ([]SourceRequiredGeneration, error) {
	database, err := openFreshnessLedger(stateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = database.Close() }()
	registeredTrawlers = canonicalRegisteredTrawlers(registeredTrawlers)
	if len(registeredTrawlers) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(registeredTrawlers))
	arguments := make([]any, len(registeredTrawlers))
	for index, registeredTrawler := range registeredTrawlers {
		placeholders[index] = "?"
		arguments[index] = registeredTrawler
	}
	rows, err := database.QueryContext(ctx, `
		select registered_trawler, required_generation
		from searchable_source_generations
		where registered_trawler in (`+strings.Join(placeholders, ",")+`)
		order by registered_trawler`, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var generations []SourceRequiredGeneration
	for rows.Next() {
		var generation SourceRequiredGeneration
		var requiredGeneration uint64
		if err := rows.Scan(&generation.RegisteredTrawler, &requiredGeneration); err != nil {
			return nil, err
		}
		generation.RequiredGeneration = SourceGeneration(requiredGeneration)
		generations = append(generations, generation)
	}
	return generations, rows.Err()
}

func openFreshnessLedger(stateRoot string) (*sql.DB, error) {
	searchDirectory := filepath.Join(stateRoot, indexDirectoryName)
	if err := os.MkdirAll(searchDirectory, 0o700); err != nil {
		return nil, err
	}
	database, err := sql.Open("sqlite3", FreshnessLedgerPath(stateRoot)+"?_busy_timeout=5000")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`
		pragma journal_mode = wal;
		pragma synchronous = full;
		create table if not exists searchable_source_generations (
			registered_trawler text primary key,
			required_generation integer not null check(required_generation > 0)
		)`); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func canonicalRegisteredTrawlers(registeredTrawlers []string) []string {
	canonical := make([]string, 0, len(registeredTrawlers))
	seen := make(map[string]struct{}, len(registeredTrawlers))
	for _, registeredTrawler := range registeredTrawlers {
		registeredTrawler = strings.TrimSpace(registeredTrawler)
		if registeredTrawler == "" {
			continue
		}
		if _, exists := seen[registeredTrawler]; exists {
			continue
		}
		seen[registeredTrawler] = struct{}{}
		canonical = append(canonical, registeredTrawler)
	}
	return canonical
}

type IndexBuildLock struct {
	file *os.File
}

func AcquireIndexBuildLock(stateRoot string) (*IndexBuildLock, error) {
	searchDirectory := filepath.Join(stateRoot, indexDirectoryName)
	if err := os.MkdirAll(searchDirectory, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(searchDirectory, buildLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("semantic search index is already reconciling")
		}
		return nil, err
	}
	return &IndexBuildLock{file: file}, nil
}

func IndexBuildIsActive(stateRoot string) (bool, error) {
	file, err := os.OpenFile(filepath.Join(stateRoot, indexDirectoryName, buildLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false, err
	}
	defer func() { _ = file.Close() }()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return true, nil
		}
		return false, err
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false, nil
}

func (lock *IndexBuildLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	return lock.file.Close()
}
