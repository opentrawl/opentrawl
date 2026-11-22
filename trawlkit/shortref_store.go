package trawlkit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const shortReferenceWireBatchSize = 5000

type coreShortReferenceStoreOpenMode int

const (
	coreShortReferenceStoreReadWrite coreShortReferenceStoreOpenMode = iota
	coreShortReferenceStoreReadOnlyIfPresent
	coreShortReferenceStoreReadOnlyWithoutWritingFilesIfPresent
)

// openCoreShortReferenceStore opens the host-owned short-reference database for
// one trawler, migrating legacy rows verbatim from the trawler's archive
// database on first population so existing links stay identical.
func openCoreShortReferenceStore(
	ctx context.Context,
	paths TrawlerArchivePaths,
	mode coreShortReferenceStoreOpenMode,
) (*store.Store, error) {
	indexPath := strings.TrimSpace(paths.TrawlerShortReferenceIndexPath)
	if indexPath == "" {
		return nil, nil
	}
	_, statErr := os.Stat(indexPath)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("stat core short reference index: %w", statErr)
	}
	if errors.Is(statErr, os.ErrNotExist) && mode != coreShortReferenceStoreReadWrite {
		return nil, nil
	}
	switch mode {
	case coreShortReferenceStoreReadOnlyWithoutWritingFilesIfPresent:
		coreStore, err := store.OpenReadOnlyWithoutWritingFiles(ctx, indexPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("open core short reference index read-only: %w", err)
		}
		return coreStore, nil
	case coreShortReferenceStoreReadOnlyIfPresent:
		coreStore, err := store.OpenReadOnlyWithSharedTrawlerArchiveFileSetLock(ctx, indexPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("open core short reference index read-only: %w", err)
		}
		return coreStore, nil
	case coreShortReferenceStoreReadWrite:
	default:
		return nil, fmt.Errorf("unknown core short reference store open mode %d", mode)
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o700); err != nil {
		return nil, fmt.Errorf("create core short reference directory: %w", err)
	}
	coreStore, err := store.Open(ctx, store.Options{Path: indexPath})
	if err != nil {
		return nil, fmt.Errorf("open core short reference index: %w", err)
	}
	if err := shortref.EnsureSchema(ctx, coreStore.DB()); err != nil {
		_ = coreStore.Close()
		return nil, err
	}
	var existingCount int
	if err := coreStore.DB().QueryRowContext(ctx, `select count(*) from short_refs`).Scan(&existingCount); err != nil {
		_ = coreStore.Close()
		return nil, fmt.Errorf("count core short references: %w", err)
	}
	if existingCount == 0 {
		if err := migrateLegacyShortReferencesVerbatim(ctx, coreStore, paths.TrawlerArchivePath); err != nil {
			_ = coreStore.Close()
			return nil, err
		}
	}
	return coreStore, nil
}

func migrateLegacyShortReferencesVerbatim(
	ctx context.Context,
	coreStore *store.Store,
	legacyArchivePath string,
) error {
	legacyArchivePath = strings.TrimSpace(legacyArchivePath)
	if legacyArchivePath == "" || legacyArchivePath == ":memory:" || strings.HasPrefix(legacyArchivePath, "file:") {
		return nil
	}
	if _, err := os.Stat(legacyArchivePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("stat legacy archive for short reference migration: %w", err)
	}
	legacyStore, err := store.OpenReadOnlyWithoutWritingFiles(ctx, legacyArchivePath)
	if err != nil {
		// Non-SQLite or unreadable archives simply start with an empty core index.
		return nil
	}
	defer func() { _ = legacyStore.Close() }()

	var hasShortRefsTable int
	err = legacyStore.DB().QueryRowContext(
		ctx,
		`select 1 from sqlite_master where type = 'table' and name = 'short_refs'`,
	).Scan(&hasShortRefsTable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return nil
	}

	type legacyShortRefRow struct {
		alias        string
		fullRef      string
		canonicalRef string
	}
	rows, err := legacyStore.DB().QueryContext(
		ctx,
		`select alias, full_ref, canonical_ref from short_refs`,
	)
	if err != nil {
		return fmt.Errorf("read legacy short references: %w", err)
	}
	var copied []legacyShortRefRow
	for rows.Next() {
		var row legacyShortRefRow
		if err := rows.Scan(&row.alias, &row.fullRef, &row.canonicalRef); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan legacy short reference: %w", err)
		}
		copied = append(copied, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate legacy short references: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close legacy short references: %w", err)
	}
	if len(copied) == 0 {
		return nil
	}
	return coreStore.WithTx(ctx, func(tx *sql.Tx) error {
		statement, err := tx.PrepareContext(ctx, `
insert or ignore into short_refs(alias, full_ref, canonical_ref, tombstoned)
values (?, ?, ?, 0)
`)
		if err != nil {
			return fmt.Errorf("prepare short reference migration: %w", err)
		}
		defer func() { _ = statement.Close() }()
		for _, row := range copied {
			if _, err := statement.ExecContext(ctx, row.alias, row.fullRef, row.canonicalRef); err != nil {
				return fmt.Errorf("copy legacy short reference: %w", err)
			}
		}
		return nil
	})
}

func publishShortReferenceCandidatesToCoreStore(
	ctx context.Context,
	coreStore *store.Store,
	candidates []ShortReferenceAssignmentCandidate,
	completeSnapshot bool,
) error {
	if coreStore == nil {
		return errors.New("core short reference store is not open")
	}
	return coreStore.WithTx(ctx, func(tx *sql.Tx) error {
		if completeSnapshot {
			return ReplaceShortReferencesForCompleteArchiveRecordSnapshotUsingCallerOwnedSQLTransaction(ctx, tx, candidates)
		}
		return AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, tx, candidates)
	})
}
