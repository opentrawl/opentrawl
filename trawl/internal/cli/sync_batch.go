package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

const syncBatchLockName = "sync.lock"

// runSyncBatch is the one composition path for every sync caller. Acquisition
// is independent, while People reconciliation is deliberately a second,
// ordered phase because every snapshot writes the same People archive.
func (r *Runtime) runSyncBatch(sources []Source, sourceArgs []string, allSources []Source, started func([]Source)) ([]Source, []SyncResult, error) {
	sources = canonicalSyncSources(sources)
	lock, err := acquireSyncBatchLock(r.stateRoot)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = lock.Close() }()
	if started != nil {
		started(sources)
	}

	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	results := runSyncPhases(
		ctx,
		sources,
		func(ctx context.Context, source Source) SyncResult {
			return syncSource(r, ctx, source, sourceArgs)
		},
		func(ctx context.Context, source Source) error {
			return r.reconcileSourcePeopleContext(ctx, source, allSources)
		},
	)
	return sources, results, nil
}

// canonicalSyncSources makes Source.ID the single identity authority for a
// batch. Command aliases may select a source, but they cannot schedule it
// twice. First occurrence defines presentation and result order.
func canonicalSyncSources(sources []Source) []Source {
	canonical := make([]Source, 0, len(sources))
	seen := make(map[string]struct{}, len(sources))
	for _, source := range sources {
		if _, exists := seen[source.ID]; exists {
			continue
		}
		seen[source.ID] = struct{}{}
		canonical = append(canonical, source)
	}
	return canonical
}

func runSyncPhases(
	ctx context.Context,
	sources []Source,
	acquire func(context.Context, Source) SyncResult,
	reconcile func(context.Context, Source) error,
) []SyncResult {
	results := make([]SyncResult, len(sources))
	var acquisitions sync.WaitGroup
	acquisitions.Add(len(sources))
	for index, source := range sources {
		index, source := index, source
		go func() {
			defer acquisitions.Done()
			results[index] = acquire(ctx, source)
		}()
	}
	acquisitions.Wait()

	for index, source := range sources {
		if syncResultFailed(results[index]) {
			continue
		}
		results[index] = withPeopleSyncFailure(results[index], reconcile(ctx, source))
	}
	return results
}

type syncBatchLock struct {
	file *os.File
}

func acquireSyncBatchLock(stateRoot string) (*syncBatchLock, error) {
	root := strings.TrimSpace(stateRoot)
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return nil, fmt.Errorf("resolve home for sync: %w", err)
		}
		root = filepath.Join(home, ".opentrawl")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
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

func (syncAlreadyRunningError) ErrorBody() ckoutput.ErrorBody {
	return ckoutput.ErrorBody{
		Code:    "already_syncing",
		Message: "OpenTrawl is already syncing.",
	}
}
