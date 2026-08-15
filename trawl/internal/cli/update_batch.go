package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/opentrawl/opentrawl/trawl/internal/densesearch"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
)

const (
	updateBatchLockName                       = "update.lock"
	semanticSearchArchiveBoundaryLockFileName = "semantic-search-archive-boundary.lock"
)

type updatePhase int

const (
	updatePhaseBuilding updatePhase = iota + 1
	updatePhaseFinalising
)

func (r *Runtime) runUpdateBatch(
	trawlers []InstalledTrawler,
	trawlerArguments []string,
	allInstalledTrawlers []InstalledTrawler,
	started func([]InstalledTrawler),
	progress func(InstalledTrawler, updatePhase),
) (*federation.FederatedTrawlerArchiveUpdateOperation, error) {
	trawlers = canonicalUpdateTrawlers(trawlers)
	lock, err := acquireUpdateBatchLock(r.stateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	semanticSearchRefreshIntents, err := r.requireSemanticSearchRefreshBeforeArchiveUpdates(r.ctx, trawlers)
	if err != nil {
		return nil, err
	}
	if started != nil {
		started(trawlers)
	}

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	results := make([]*federation.TrawlerArchiveUpdateResult, len(trawlers))
	failures := make([]*federation.TrawlerOperationFailure, len(trawlers))
	skipped := make([]*federation.TrawlerSkippedFromOperation, len(trawlers))
	peopleArchiveUpdateFailures := make([]*federation.PeopleArchiveUpdateFailureAfterTrawlerArchiveUpdate, len(trawlers))
	var waitForTrawlers sync.WaitGroup
	waitForTrawlers.Add(len(trawlers))
	for index, trawler := range trawlers {
		index, trawler := index, trawler
		go func() {
			defer waitForTrawlers.Done()
			if progress != nil {
				progress(trawler, updatePhaseBuilding)
			}
			results[index], failures[index], skipped[index] = r.updateTrawler(ctx, trawler, trawlerArguments)
		}()
	}
	waitForTrawlers.Wait()

	for index, trawler := range trawlers {
		if results[index] == nil {
			continue
		}
		if progress != nil {
			progress(trawler, updatePhaseFinalising)
		}
		if err := r.reconcileTrawlerPeopleContext(ctx, trawler, allInstalledTrawlers); err != nil {
			r.logInfo(
				"trawler_people_update_failed",
				trawlerField(trawler)+" error="+logQuote(cklog.InternalErrorLogMessage(err)),
			)
			peopleArchiveUpdateFailures[index] = &federation.PeopleArchiveUpdateFailureAfterTrawlerArchiveUpdate{
				SuccessfullyUpdatedTrawler:            trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
				SuccessfullyUpdatedTrawlerDisplayName: trawlerHumanName(trawler),
			}
		}
	}

	operation := &federation.FederatedTrawlerArchiveUpdateOperation{}
	for index := range trawlers {
		if results[index] != nil {
			operation.TrawlerArchiveUpdateResults = append(operation.TrawlerArchiveUpdateResults, results[index])
		}
		if failures[index] != nil {
			operation.OperationFailures = append(operation.OperationFailures, failures[index])
		}
		if skipped[index] != nil {
			operation.TrawlersSkippedFromOperation = append(operation.TrawlersSkippedFromOperation, skipped[index])
		}
		if peopleArchiveUpdateFailures[index] != nil {
			operation.PeopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate = append(
				operation.PeopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate,
				peopleArchiveUpdateFailures[index],
			)
		}
	}
	operation.Outcome = federatedOperationOutcome(
		len(operation.TrawlerArchiveUpdateResults),
		len(operation.OperationFailures)+len(operation.PeopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate),
		len(operation.TrawlersSkippedFromOperation),
	)
	r.restoreSemanticSearchFreshnessForProvenUnchangedArchives(ctx, semanticSearchRefreshIntents, operation)
	return operation, nil
}

func (r *Runtime) requireSemanticSearchRefreshBeforeArchiveUpdates(
	ctx context.Context,
	trawlers []InstalledTrawler,
) ([]densesearch.SourceRefreshIntent, error) {
	var searchableRegisteredTrawlers []string
	for _, trawler := range trawlers {
		if trawler.Trawler == nil {
			continue
		}
		if _, searchable := trawler.Trawler.(trawlkit.SearchableRecordExporter); searchable {
			searchableRegisteredTrawlers = append(searchableRegisteredTrawlers, installedTrawlerIdentityText(trawler))
		}
	}
	return densesearch.RequireSourceRefreshesBeforeUpdates(ctx, r.stateRoot, searchableRegisteredTrawlers)
}

func (r *Runtime) restoreSemanticSearchFreshnessForProvenUnchangedArchives(
	ctx context.Context,
	intents []densesearch.SourceRefreshIntent,
	operation *federation.FederatedTrawlerArchiveUpdateOperation,
) {
	var unchangedRegisteredTrawlers []string
	for _, result := range operation.GetTrawlerArchiveUpdateResults() {
		if archiveUpdateReportProvesNoSearchableRecordChanged(result.GetTrawlerArchiveUpdateReport()) {
			unchangedRegisteredTrawlers = append(
				unchangedRegisteredTrawlers,
				trawlkit.RegisteredTrawlerIdentityText(result.GetRegisteredTrawler()),
			)
		}
	}
	if err := densesearch.RestoreRefreshIntentsForUnchangedSources(
		ctx,
		r.stateRoot,
		intents,
		unchangedRegisteredTrawlers,
	); err != nil {
		r.logInfo(
			"semantic_search_unchanged_refresh_restore_failed",
			"error_type="+logQuote(errorTypeName(err)),
		)
	}
}

func archiveUpdateReportProvesNoSearchableRecordChanged(report *updatecontract.TrawlerArchiveUpdateReport) bool {
	if report == nil || report.ArchiveRecordCountAddedByThisUpdate == nil ||
		report.ArchiveRecordCountUpdatedByThisUpdate == nil ||
		report.ArchiveRecordCountRemovedByThisUpdate == nil {
		return false
	}
	return report.GetArchiveRecordCountAddedByThisUpdate() == 0 &&
		report.GetArchiveRecordCountUpdatedByThisUpdate() == 0 &&
		report.GetArchiveRecordCountRemovedByThisUpdate() == 0
}

func federatedOperationOutcome(successes, failures, skipped int) federation.OperationOutcome {
	if successes > 0 && failures == 0 && skipped == 0 {
		return federation.OperationOutcome_OPERATION_OUTCOME_COMPLETE
	}
	if successes > 0 || failures == 0 && skipped > 0 {
		return federation.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	}
	return federation.OperationOutcome_OPERATION_OUTCOME_FAILED
}

func canonicalUpdateTrawlers(trawlers []InstalledTrawler) []InstalledTrawler {
	canonical := make([]InstalledTrawler, 0, len(trawlers))
	seen := make(map[string]struct{}, len(trawlers))
	for _, trawler := range trawlers {
		registeredTrawlerIdentityText := installedTrawlerIdentityText(trawler)
		if _, exists := seen[registeredTrawlerIdentityText]; exists {
			continue
		}
		seen[registeredTrawlerIdentityText] = struct{}{}
		canonical = append(canonical, trawler)
	}
	return canonical
}

type updateBatchLock struct {
	file *os.File
}

func acquireUpdateBatchLock(stateRoot string) (*updateBatchLock, error) {
	lock, err := acquireUpdateBatchLockWithoutWaiting(stateRoot)
	if err == nil {
		return lock, nil
	}
	var alreadyUpdating updateAlreadyRunningError
	if !errors.As(err, &alreadyUpdating) {
		return nil, err
	}
	semanticSearchBoundaryActive, boundaryErr := semanticSearchArchiveBoundaryIsActive(stateRoot)
	if boundaryErr != nil || !semanticSearchBoundaryActive {
		return nil, err
	}
	return acquireUpdateBatchLockWaiting(stateRoot)
}

func acquireUpdateBatchLockWithoutWaiting(stateRoot string) (*updateBatchLock, error) {
	return acquireUpdateBatchLockWithOperation(stateRoot, syscall.LOCK_EX|syscall.LOCK_NB)
}

func acquireUpdateBatchLockWaiting(stateRoot string) (*updateBatchLock, error) {
	return acquireUpdateBatchLockWithOperation(stateRoot, syscall.LOCK_EX)
}

func acquireUpdateBatchLockWithOperation(stateRoot string, operation int) (*updateBatchLock, error) {
	root, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create OpenTrawl state: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(root, updateBatchLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open update lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), operation); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, updateAlreadyRunningError{}
		}
		return nil, fmt.Errorf("lock update: %w", err)
	}
	return &updateBatchLock{file: file}, nil
}

func acquireSemanticSearchArchiveBoundaryLock(stateRoot string) (*updateBatchLock, error) {
	root, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(root, semanticSearchArchiveBoundaryLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &updateBatchLock{file: file}, nil
}

func semanticSearchArchiveBoundaryIsActive(stateRoot string) (bool, error) {
	root, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return false, err
	}
	file, err := os.OpenFile(filepath.Join(root, semanticSearchArchiveBoundaryLockFileName), os.O_CREATE|os.O_RDWR, 0o600)
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

func (lock *updateBatchLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	return lock.file.Close()
}

type updateAlreadyRunningError struct{}

func (updateAlreadyRunningError) Error() string { return "OpenTrawl is already updating." }

func (updateAlreadyRunningError) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{
		Code:    "already_updating",
		Message: "OpenTrawl is already updating.",
	}
}
