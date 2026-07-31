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
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

const updateBatchLockName = "update.lock"

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
	return operation, nil
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
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, updateAlreadyRunningError{}
		}
		return nil, fmt.Errorf("lock update: %w", err)
	}
	return &updateBatchLock{file: file}, nil
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
