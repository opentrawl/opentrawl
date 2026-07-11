package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/developmentcache"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

type syntheticRunnerCache struct {
	request photos.OriginalRequest
	hit     photos.OriginalResolution
}

func (c *syntheticRunnerCache) Lookup(_ context.Context, request photos.OriginalRequest) (photos.OriginalResolution, bool, error) {
	if request.Query.LocalIdentifier != c.request.Query.LocalIdentifier {
		return photos.OriginalResolution{}, false, errors.New("unexpected synthetic runner lookup")
	}
	return c.hit, true, nil
}

func (*syntheticRunnerCache) MissSize(photos.OriginalRequest) (int64, bool, error) {
	return 0, false, errors.New("checked runner hit must not reserve a miss")
}

func (*syntheticRunnerCache) ResolvePackage(context.Context, photos.OriginalRequest, int64) (photos.OriginalResolution, error) {
	return photos.OriginalResolution{}, errors.New("checked runner hit must not resolve")
}

func (*syntheticRunnerCache) Usage(context.Context) (photos.DevelopmentCacheUsage, error) {
	return photos.DevelopmentCacheUsage{}, errors.New("checked runner hit must not scan usage")
}

func TestInternalRunnerExecutesSyntheticWiringThroughMainPath(t *testing.T) {
	root := canonicalTempDir(t)
	archivePath := filepath.Join(root, "photos.db")
	sourceRoot := filepath.Join(root, "Synthetic Library.photoslibrary")
	cacheRoot := filepath.Join(root, "external-cache")
	stateDir := filepath.Join(root, "state")
	statePath := filepath.Join(stateDir, "checkpoint.sqlite")
	for _, path := range []string{sourceRoot, cacheRoot, stateDir} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(archivePath, []byte("synthetic read-only archive boundary"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceID := photos.SourceLibraryID(sourceRoot)
	request := photos.OriginalRequest{
		SourceLibraryID:  sourceID,
		ModificationDate: "2026-07-11T10:05:00Z",
		Query: photos.OriginalExportQuery{
			LocalIdentifier:  "runner-checked-hit",
			OriginalFilename: "runner-checked-hit.heic",
			OriginalUTI:      "public.heic",
		},
	}
	cacheKey := filepath.Base(photos.OriginalCachePath("", request.SourceLibraryID, request.ModificationDate, request.Query))
	selection := archive.DevelopmentCacheSelection{
		SourceLibraryID: sourceID,
		SnapshotID:      "synthetic-complete-snapshot",
		Assets:          []archive.DevelopmentCacheAsset{{AssetID: "synthetic-runner-asset", CacheKey: cacheKey, Request: request}},
	}
	cache := &syntheticRunnerCache{
		request: request,
		hit: photos.OriginalResolution{
			Path:   filepath.Join(cacheRoot, cacheKey),
			Source: photos.OriginalSourceDevelopmentCache,
			Size:   23,
			SHA256: "synthetic-runner-sha256",
		},
	}
	args := []string{
		"--archive", archivePath,
		"--cache-root", cacheRoot,
		"--source-root", sourceRoot,
		"--state", statePath,
		"--source-library-id", sourceID,
		"--batch-size", "1",
		"--capacity-bytes", "2000",
		"--free-space-floor-bytes", "500",
		"--network", "disabled",
	}
	dependencies := runnerDependencies{
		newCache: func(commandConfig) (developmentcache.Cache, error) {
			return cache, nil
		},
		openCheckpoint: developmentcache.OpenCheckpoint,
		selectAssets: func(context.Context, string, string) (archive.DevelopmentCacheSelection, error) {
			return selection, nil
		},
		availableBytes: func(string) (int64, error) { return 10_000, nil },
		clock: func() time.Time {
			return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
		},
	}
	var stdout, stderr bytes.Buffer
	exit := run(context.Background(), args, &stdout, &stderr, dependencies)
	t.Logf("boundary=internal_runner_wiring input_args=%q output_stdout=%q output_stderr=%q output_exit=%d", args, stdout.String(), stderr.String(), exit)
	wantStdout := fmt.Sprintf("{\"source_library_id\":%q,\"snapshot_id\":\"synthetic-complete-snapshot\",\"selected\":1,\"completed\":1,\"reused\":1,\"restart_reused\":0,\"installed\":0,\"coalesced\":0,\"stop_reason\":\"batch_complete\"}\n", sourceID)
	wantStderr := "{\"phase\":\"selection\",\"outcome\":\"ok\",\"duration_millis\":0}\n" +
		"{\"phase\":\"checkpoint_read\",\"outcome\":\"ok\",\"duration_millis\":0}\n" +
		"{\"phase\":\"cache_lookup\",\"asset_id\":\"synthetic-runner-asset\",\"outcome\":\"checked_hit\",\"duration_millis\":0}\n" +
		"{\"phase\":\"checkpoint\",\"asset_id\":\"synthetic-runner-asset\",\"outcome\":\"ok\",\"duration_millis\":0}\n"
	if exit != 0 || stdout.String() != wantStdout || stderr.String() != wantStderr {
		t.Fatalf("exit = %d stdout = %q stderr = %q", exit, stdout.String(), stderr.String())
	}
}

func TestInternalRunnerRejectsImplicitNetworkPolicyBeforeExecution(t *testing.T) {
	args := []string{
		"--archive", "/synthetic/archive/photos.db",
		"--cache-root", "/synthetic/external/originals",
		"--source-root", "/synthetic/library.photoslibrary",
		"--state", "/synthetic/state/checkpoint.sqlite",
		"--source-library-id", "synthetic-source",
		"--batch-size", "1",
		"--capacity-bytes", "2000",
		"--free-space-floor-bytes", "500",
	}
	var stdout, stderr bytes.Buffer
	executed := false
	exit := run(context.Background(), args, &stdout, &stderr, runnerDependencies{
		newCache: func(commandConfig) (developmentcache.Cache, error) {
			executed = true
			return nil, nil
		},
		openCheckpoint: developmentcache.OpenCheckpoint,
		selectAssets: func(context.Context, string, string) (archive.DevelopmentCacheSelection, error) {
			return archive.DevelopmentCacheSelection{}, nil
		},
		availableBytes: func(string) (int64, error) { return 0, nil },
	})
	t.Logf("boundary=internal_runner_policy input_args=%q output_stdout=%q output_stderr=%q output_exit=%d executed=%t", args, stdout.String(), stderr.String(), exit, executed)
	if exit != 2 || executed || stderr.String() != "--network must be explicitly set to disabled or enabled\n" {
		t.Fatalf("exit = %d executed = %t stdout = %q stderr = %q", exit, executed, stdout.String(), stderr.String())
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	return root
}
