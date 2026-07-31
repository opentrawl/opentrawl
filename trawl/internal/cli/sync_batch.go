package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

const syncBatchLockName = "sync.lock"

type syncPhase int

const (
	syncPhaseBuilding syncPhase = iota + 1
	syncPhaseFinalising
)

func (r *Runtime) runSyncBatch(
	trawlers []InstalledTrawler,
	trawlerArguments []string,
	allInstalledTrawlers []InstalledTrawler,
	started func([]InstalledTrawler),
	progress func(InstalledTrawler, syncPhase),
) (*federationv1.FederatedTrawlerArchiveSyncOperation, error) {
	trawlers = canonicalSyncTrawlers(trawlers)
	lock, err := acquireSyncBatchLock(r.stateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lock.Close() }()
	if started != nil {
		started(trawlers)
	}

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	results := make([]*federationv1.TrawlerArchiveSyncResult, len(trawlers))
	failures := make([]*federationv1.TrawlerOperationFailure, len(trawlers))
	skipped := make([]*federationv1.TrawlerSkippedFromOperation, len(trawlers))
	peopleArchiveUpdateFailures := make([]*federationv1.PeopleArchiveUpdateFailureAfterTrawlerArchiveSync, len(trawlers))
	var waitForTrawlers sync.WaitGroup
	waitForTrawlers.Add(len(trawlers))
	for index, trawler := range trawlers {
		index, trawler := index, trawler
		go func() {
			defer waitForTrawlers.Done()
			if progress != nil {
				progress(trawler, syncPhaseBuilding)
			}
			results[index], failures[index], skipped[index] = r.syncTrawler(ctx, trawler, trawlerArguments)
		}()
	}
	waitForTrawlers.Wait()

	for index, trawler := range trawlers {
		if results[index] == nil {
			continue
		}
		if progress != nil {
			progress(trawler, syncPhaseFinalising)
		}
		if err := r.reconcileTrawlerPeopleContext(ctx, trawler, allInstalledTrawlers); err != nil {
			r.logInfo(
				"trawler_people_update_failed",
				trawlerField(trawler)+" error="+logQuote(cklog.InternalErrorLogMessage(err)),
			)
			peopleArchiveUpdateFailures[index] = &federationv1.PeopleArchiveUpdateFailureAfterTrawlerArchiveSync{
				SuccessfullySyncedTrawler:            trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
				SuccessfullySyncedTrawlerDisplayName: trawlerHumanName(trawler),
			}
		}
	}

	operation := &federationv1.FederatedTrawlerArchiveSyncOperation{}
	for index := range trawlers {
		if results[index] != nil {
			operation.TrawlerArchiveSyncResults = append(operation.TrawlerArchiveSyncResults, results[index])
		}
		if failures[index] != nil {
			operation.OperationFailures = append(operation.OperationFailures, failures[index])
		}
		if skipped[index] != nil {
			operation.TrawlersSkippedFromOperation = append(operation.TrawlersSkippedFromOperation, skipped[index])
		}
		if peopleArchiveUpdateFailures[index] != nil {
			operation.PeopleArchiveUpdateFailuresAfterTrawlerArchiveSync = append(
				operation.PeopleArchiveUpdateFailuresAfterTrawlerArchiveSync,
				peopleArchiveUpdateFailures[index],
			)
		}
	}
	operation.Outcome = federatedOperationOutcome(
		len(operation.TrawlerArchiveSyncResults),
		len(operation.OperationFailures)+len(operation.PeopleArchiveUpdateFailuresAfterTrawlerArchiveSync),
		len(operation.TrawlersSkippedFromOperation),
	)
	return operation, nil
}

func federatedOperationOutcome(successes, failures, skipped int) federationv1.OperationOutcome {
	if successes > 0 && failures == 0 && skipped == 0 {
		return federationv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE
	}
	if successes > 0 || failures == 0 && skipped > 0 {
		return federationv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL
	}
	return federationv1.OperationOutcome_OPERATION_OUTCOME_FAILED
}

func canonicalSyncTrawlers(trawlers []InstalledTrawler) []InstalledTrawler {
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

type syncBatchLock struct {
	file *os.File
}

func acquireSyncBatchLock(stateRoot string) (*syncBatchLock, error) {
	root, err := trawlkit.ResolveStateRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create OpenTrawl state: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(root, syncBatchLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open sync lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, syncAlreadyRunningError{}
		}
		return nil, fmt.Errorf("lock sync: %w", err)
	}
	return &syncBatchLock{file: file}, nil
}

func (lock *syncBatchLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	_ = syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	return lock.file.Close()
}

type syncAlreadyRunningError struct{}

func (syncAlreadyRunningError) Error() string { return "OpenTrawl is already syncing." }

func (syncAlreadyRunningError) ErrorDescription() ckoutput.ErrorDescription {
	return ckoutput.ErrorDescription{
		Code:    "already_syncing",
		Message: "OpenTrawl is already syncing.",
	}
}
