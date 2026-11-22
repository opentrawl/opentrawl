package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"github.com/opentrawl/opentrawl/trawlkit/unifiedindex"
)

type coreShortReferenceStoreOpenMode int

const (
	coreShortReferenceStoreReadWrite coreShortReferenceStoreOpenMode = iota
	coreShortReferenceStoreReadOnlyIfPresent
	coreShortReferenceStoreReadOnlyWithoutWritingFilesIfPresent
)

// openCoreShortReferenceStore opens the host-owned unified index database
// (<stateRoot>/index.db) and ensures any pre-existing short references for the
// selected trawler (from Wave B's shortrefs/<id>.db or legacy archive DBs) are
// migrated verbatim on first read-write open.
func openCoreShortReferenceStore(
	ctx context.Context,
	paths resolvedTrawlerArchivePaths,
	mode coreShortReferenceStoreOpenMode,
) (*store.Store, error) {
	stateRoot := strings.TrimSpace(paths.StateRoot)
	if stateRoot == "" {
		return nil, nil
	}
	switch mode {
	case coreShortReferenceStoreReadOnlyWithoutWritingFilesIfPresent:
		return unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, true)
	case coreShortReferenceStoreReadOnlyIfPresent:
		return unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	case coreShortReferenceStoreReadWrite:
	default:
		return nil, fmt.Errorf("unknown core short reference store open mode %d", mode)
	}
	coreStore, err := unifiedindex.OpenUnifiedIndexReadWrite(ctx, stateRoot)
	if err != nil {
		return nil, err
	}
	trawlerID := RegisteredTrawlerIdentityText(paths.RegisteredTrawler)
	if trawlerID != "" {
		if err := unifiedindex.EnsureTrawlerShortReferencesMigratedVerbatim(
			ctx,
			coreStore.DB(),
			stateRoot,
			trawlerID,
			paths.TrawlerArchivePath,
		); err != nil {
			_ = coreStore.Close()
			return nil, err
		}
	}
	return coreStore, nil
}

func publishIndexChangeBatchesToCoreStore(
	ctx context.Context,
	coreStore *store.Store,
	stateRoot string,
	trawlerID string,
	trawlerDisplayName string,
	legacyArchivePath string,
	batches []*indexcontract.ArchiveIndexChangeBatch,
) error {
	if coreStore == nil {
		return errors.New("core unified index store is not open")
	}
	return unifiedindex.CommitTrawlerIndexChangeBatches(
		ctx,
		coreStore.DB(),
		stateRoot,
		trawlerID,
		trawlerDisplayName,
		legacyArchivePath,
		batches,
	)
}

func readTrawlerIndexCursorFromStateRoot(ctx context.Context, stateRoot string, trawlerID string) string {
	coreStore, err := unifiedindex.OpenUnifiedIndexReadOnly(ctx, stateRoot, false)
	if err != nil || coreStore == nil {
		return ""
	}
	defer func() { _ = coreStore.Close() }()
	cursor, _, err := unifiedindex.ReadTrawlerIndexCursor(ctx, coreStore.DB(), trawlerID)
	if err != nil {
		return ""
	}
	return cursor
}

// SyncTrawlerArchiveIndex runs one trawler's ArchiveIndexChangeExporter
// strictly read-only against its source archive and commits the exported
// batches atomically into <stateRoot>/index.db. When forceFullRebuild is false
// and the trawler already has a committed cursor, only incremental changes are
// exported. Returns (exported, error); exported is false if the trawler does
// not implement ArchiveIndexChangeExporter or has no archive file on disk.
func SyncTrawlerArchiveIndex(
	ctx context.Context,
	stateRoot string,
	trawler Trawler,
	forceFullRebuild bool,
) (bool, error) {
	if external, ok := trawler.(ExternalPluginExecutor); ok {
		return external.ExecuteExternalPluginSyncArchiveIndex(ctx, stateRoot, forceFullRebuild)
	}
	exporter, ok := trawler.(ArchiveIndexChangeExporter)
	if !ok {
		return false, nil
	}
	decl := trawler.RegisteredTrawlerDeclaration()
	paths, err := resolveTrawlerArchivePaths(stateRoot, decl)
	if err != nil {
		return false, err
	}
	if _, statErr := os.Stat(paths.TrawlerArchivePath); errors.Is(statErr, os.ErrNotExist) {
		return false, nil
	} else if statErr != nil {
		return false, fmt.Errorf("stat %s archive: %w", RegisteredTrawlerIdentityText(decl.RegisteredTrawler), statErr)
	}
	if err := trawler.LoadTrawlerConfiguration(paths.TrawlerConfigurationPath); err != nil {
		return false, err
	}
	archiveStore, err := store.OpenReadOnlyWithoutWritingFiles(ctx, paths.TrawlerArchivePath)
	if err != nil {
		return false, fmt.Errorf("open %s archive read-only: %w", RegisteredTrawlerIdentityText(decl.RegisteredTrawler), err)
	}
	defer func() { _ = archiveStore.Close() }()

	coreStore, err := openCoreShortReferenceStore(ctx, paths, coreShortReferenceStoreReadWrite)
	if err != nil {
		return false, err
	}
	defer func() { _ = coreStore.Close() }()

	trawlerID := RegisteredTrawlerIdentityText(decl.RegisteredTrawler)
	displayName := strings.TrimSpace(decl.RegisteredTrawlerDisplayName)
	if displayName == "" {
		displayName = trawlerID
	}

	sinceCursor := ""
	if !forceFullRebuild {
		cursor, hasIndexed, cursorErr := unifiedindex.ReadTrawlerIndexCursor(ctx, coreStore.DB(), trawlerID)
		if cursorErr != nil {
			return false, cursorErr
		}
		if hasIndexed {
			sinceCursor = cursor
		}
	}

	req := &TrawlerCommandExecutionRequest{
		OpenedTrawlerArchiveStore:     archiveStore,
		TrawlerArchivePaths:           paths.TrawlerArchivePaths,
		coreShortReferenceStore:       coreStore,
		registeredTrawlerIdentityText: trawlerID,
		registeredTrawlerDisplayName:  displayName,
		stateRoot:                     paths.StateRoot,
	}

	var batches []*indexcontract.ArchiveIndexChangeBatch
	if err := exporter.ExportArchiveIndexChanges(ctx, req, sinceCursor, func(batch *indexcontract.ArchiveIndexChangeBatch) error {
		if batch != nil {
			batches = append(batches, batch)
		}
		return nil
	}); err != nil {
		return false, err
	}
	if len(batches) == 0 {
		return true, nil
	}
	if err := publishIndexChangeBatchesToCoreStore(
		ctx,
		coreStore,
		paths.StateRoot,
		trawlerID,
		displayName,
		paths.TrawlerArchivePath,
		batches,
	); err != nil {
		return false, err
	}
	return true, nil
}
