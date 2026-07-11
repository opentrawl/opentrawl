package developmentcache

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

type syntheticCache struct {
	mu           sync.Mutex
	entries      map[string]photos.OriginalResolution
	missSizes    map[string]int64
	resolveCalls int
	helperCalls  int
	packageError error
}

func (c *syntheticCache) Lookup(_ context.Context, request photos.OriginalRequest) (photos.OriginalResolution, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[syntheticCacheKey(request)]
	return entry, ok, nil
}

func (c *syntheticCache) MissSize(request photos.OriginalRequest) (int64, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	size, ok := c.missSizes[syntheticCacheKey(request)]
	return size, ok, nil
}

func (c *syntheticCache) ResolvePackage(_ context.Context, request photos.OriginalRequest, reservedSize int64) (photos.OriginalResolution, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resolveCalls++
	if c.packageError != nil {
		return photos.OriginalResolution{}, c.packageError
	}
	key := syntheticCacheKey(request)
	entry := photos.OriginalResolution{Path: filepath.Join("synthetic-cache", key), Source: photos.OriginalSourceDevelopmentCache, Size: reservedSize, SHA256: "synthetic-sha256-" + key}
	c.entries[key] = entry
	return entry, nil
}

func (c *syntheticCache) Usage(context.Context) (photos.DevelopmentCacheUsage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	usage := photos.DevelopmentCacheUsage{Files: len(c.entries)}
	for _, entry := range c.entries {
		usage.Bytes += entry.Size
	}
	return usage, nil
}

func TestEngineStopsNetworkDisabledMissBeforeHelper(t *testing.T) {
	asset := syntheticAsset("network-disabled")
	cache := &syntheticCache{entries: map[string]photos.OriginalResolution{}}
	checkpoint := openSyntheticCheckpoint(t)
	result := runSyntheticEngine(t, checkpoint, cache, syntheticSelection(asset), Config{
		SourceLibraryID:     "synthetic-source",
		BatchSize:           1,
		CapacityBytes:       100,
		FreeSpaceFloorBytes: 100,
		AllowNetwork:        false,
	}, 1_000)
	t.Logf("boundary=network_disabled input=%s output=%s resolve_calls=%d helper_calls=%d", jsonValue(t, asset), jsonValue(t, result), cache.resolveCalls, cache.helperCalls)
	if result.StopReason != StopNetworkDisabled || cache.resolveCalls != 0 || cache.helperCalls != 0 {
		t.Fatalf("result = %#v resolve calls = %d helper calls = %d", result, cache.resolveCalls, cache.helperCalls)
	}
}

func TestEngineStopsUnknownNetworkSizeBeforeResolver(t *testing.T) {
	asset := syntheticAsset("network-size-unknown")
	cache := &syntheticCache{entries: map[string]photos.OriginalResolution{}}
	result := runSyntheticEngine(t, openSyntheticCheckpoint(t), cache, syntheticSelection(asset), Config{
		SourceLibraryID:     "synthetic-source",
		BatchSize:           1,
		CapacityBytes:       100,
		FreeSpaceFloorBytes: 10,
		AllowNetwork:        true,
	}, 1_000)
	t.Logf("boundary=unknown_network_size input=%s output=%s resolve_calls=%d helper_calls=%d", jsonValue(t, asset), jsonValue(t, result), cache.resolveCalls, cache.helperCalls)
	if result.StopReason != StopOriginalSizeUnknown || cache.resolveCalls != 0 || cache.helperCalls != 0 {
		t.Fatalf("result = %#v resolve calls = %d helper calls = %d", result, cache.resolveCalls, cache.helperCalls)
	}
}

func TestEngineStopsWhenReservedPackageProofChanges(t *testing.T) {
	asset := syntheticAsset("changed-package-proof")
	cache := &syntheticCache{
		entries:      map[string]photos.OriginalResolution{},
		missSizes:    map[string]int64{asset.CacheKey: 20},
		packageError: photos.ErrDevelopmentOriginalSizeUnknown,
	}
	result := runSyntheticEngine(t, openSyntheticCheckpoint(t), cache, syntheticSelection(asset), Config{
		SourceLibraryID:     "synthetic-source",
		BatchSize:           1,
		CapacityBytes:       100,
		FreeSpaceFloorBytes: 10,
		AllowNetwork:        true,
	}, 1_000)
	t.Logf("boundary=changed_package_proof input={reserved_bytes:20,allow_network:true} output=%s resolve_calls=%d helper_calls=%d", jsonValue(t, result), cache.resolveCalls, cache.helperCalls)
	if result.StopReason != StopOriginalSizeUnknown || result.Installed != 0 || cache.resolveCalls != 1 || cache.helperCalls != 0 {
		t.Fatalf("result = %#v resolve calls = %d helper calls = %d", result, cache.resolveCalls, cache.helperCalls)
	}
}

func TestEngineUsesCheckedSizeAndStopsBeforeCapacityOrFreeSpaceWouldCross(t *testing.T) {
	asset := syntheticAsset("bounded")
	asset.Request.PackageCandidates = []photos.LocalMediaCandidate{{Path: "/synthetic/package/underestimated.heic", Class: "original", Size: 1}}
	for _, test := range []struct {
		name       string
		usageBytes int64
		available  int64
		want       string
	}{
		{name: "capacity", usageBytes: 90, available: 1_000, want: StopCapacityCeiling},
		{name: "free_space", usageBytes: 0, available: 110, want: StopFreeSpaceFloor},
	} {
		t.Run(test.name, func(t *testing.T) {
			cache := &syntheticCache{entries: map[string]photos.OriginalResolution{}, missSizes: map[string]int64{asset.CacheKey: 20}}
			if test.usageBytes > 0 {
				cache.entries["existing"] = photos.OriginalResolution{Size: test.usageBytes, SHA256: "existing-synthetic-sha256"}
			}
			result := runSyntheticEngine(t, openSyntheticCheckpoint(t), cache, syntheticSelection(asset), Config{
				SourceLibraryID:     "synthetic-source",
				BatchSize:           1,
				CapacityBytes:       100,
				FreeSpaceFloorBytes: 100,
				AllowNetwork:        true,
			}, test.available)
			t.Logf("boundary=%s input={\"usage_bytes\":%d,\"archive_reported_bytes\":1,\"checked_reservation_bytes\":20,\"available_bytes\":%d,\"capacity_bytes\":100,\"free_space_floor_bytes\":100} output=%s resolve_calls=%d helper_calls=%d", test.name, test.usageBytes, test.available, jsonValue(t, result), cache.resolveCalls, cache.helperCalls)
			if result.StopReason != test.want || cache.resolveCalls != 0 || cache.helperCalls != 0 {
				t.Fatalf("result = %#v resolve calls = %d helper calls = %d", result, cache.resolveCalls, cache.helperCalls)
			}
		})
	}
}

func TestEnginePersistsAtomicCompletionAndReusesItAfterRestart(t *testing.T) {
	paths := syntheticStoragePaths(t)
	firstCheckpoint := openCheckpointAt(t, paths)
	firstAsset, secondAsset := syntheticAsset("first"), syntheticAsset("second")
	cache := &syntheticCache{entries: map[string]photos.OriginalResolution{}, missSizes: map[string]int64{firstAsset.CacheKey: 20, secondAsset.CacheKey: 20}}
	selection := syntheticSelection(firstAsset, secondAsset)
	config := Config{SourceLibraryID: "synthetic-source", BatchSize: 1, CapacityBytes: 100, FreeSpaceFloorBytes: 10, AllowNetwork: true}
	first := runSyntheticEngine(t, firstCheckpoint, cache, selection, config, 1_000)
	if err := firstCheckpoint.Close(); err != nil {
		t.Fatal(err)
	}
	secondCheckpoint := openCheckpointAt(t, paths)
	second := runSyntheticEngine(t, secondCheckpoint, cache, selection, config, 1_000)
	completed, err := secondCheckpoint.Completed(context.Background(), selection.SourceLibraryID, selection.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=checkpoint_restart input_first=%s output_first=%s input_second=%s output_second=%s durable=%s resolve_calls=%d helper_calls=%d", jsonValue(t, selection), jsonValue(t, first), jsonValue(t, selection), jsonValue(t, second), jsonValue(t, completed), cache.resolveCalls, cache.helperCalls)
	if first.Installed != 1 || second.RestartReused != 1 || second.Installed != 1 || len(completed) != 2 || cache.resolveCalls != 2 || cache.helperCalls != 0 {
		t.Fatalf("first = %#v second = %#v completed = %#v resolve calls = %d helper calls = %d", first, second, completed, cache.resolveCalls, cache.helperCalls)
	}
}

func TestEngineReusesCheckedHitAndCoalescesOneKey(t *testing.T) {
	first := syntheticAsset("same-key")
	duplicate := first
	duplicate.AssetID = "duplicate-asset-row"
	key := first.CacheKey
	cache := &syntheticCache{entries: map[string]photos.OriginalResolution{
		key: {Path: filepath.Join("synthetic-cache", key), Source: photos.OriginalSourceDevelopmentCache, Size: 20, SHA256: "checked-synthetic-sha256"},
	}}
	checkpoint := openSyntheticCheckpoint(t)
	selection := syntheticSelection(first, duplicate)
	result := runSyntheticEngine(t, checkpoint, cache, selection, Config{
		SourceLibraryID:     "synthetic-source",
		BatchSize:           2,
		CapacityBytes:       100,
		FreeSpaceFloorBytes: 10,
		AllowNetwork:        true,
	}, 1_000)
	t.Logf("boundary=checked_hit_coalescing input=%s output=%s resolve_calls=%d helper_calls=%d", jsonValue(t, selection), jsonValue(t, result), cache.resolveCalls, cache.helperCalls)
	if result.Reused != 1 || result.Coalesced != 1 || result.Completed != 1 || cache.resolveCalls != 0 || cache.helperCalls != 0 {
		t.Fatalf("result = %#v resolve calls = %d helper calls = %d", result, cache.resolveCalls, cache.helperCalls)
	}
}

func runSyntheticEngine(t *testing.T, checkpoint *Checkpoint, cache *syntheticCache, selection archive.DevelopmentCacheSelection, config Config, available int64) Result {
	t.Helper()
	var events []Event
	engine := Engine{
		Select:     func(context.Context, string) (archive.DevelopmentCacheSelection, error) { return selection, nil },
		Cache:      cache,
		Checkpoint: checkpoint,
		AvailableBytes: func(context.Context) (int64, error) {
			return available, nil
		},
		Log: func(event Event) { events = append(events, event) },
	}
	result, err := engine.Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=engine_events input=%s output=%s", jsonValue(t, config), jsonValue(t, events))
	return result
}

func openSyntheticCheckpoint(t *testing.T) *Checkpoint {
	t.Helper()
	return openCheckpointAt(t, syntheticStoragePaths(t))
}

func openCheckpointAt(t *testing.T, paths StoragePaths) *Checkpoint {
	t.Helper()
	checkpoint, err := OpenCheckpoint(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.now = func() time.Time { return time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { _ = checkpoint.Close() })
	return checkpoint
}

func syntheticSelection(assets ...archive.DevelopmentCacheAsset) archive.DevelopmentCacheSelection {
	return archive.DevelopmentCacheSelection{SourceLibraryID: "synthetic-source", SnapshotID: "synthetic-complete-snapshot", Assets: assets}
}

func syntheticAsset(id string) archive.DevelopmentCacheAsset {
	request := photos.OriginalRequest{
		SourceLibraryID:  "synthetic-source",
		ModificationDate: "2026-07-11T10:05:00Z",
		Query: photos.OriginalExportQuery{
			LocalIdentifier:  id,
			CreationDate:     "2026-07-11T10:00:00Z",
			Width:            1600,
			Height:           1200,
			OriginalFilename: id + ".heic",
			OriginalUTI:      "public.heic",
		},
	}
	return archive.DevelopmentCacheAsset{AssetID: id, CacheKey: syntheticCacheKey(request), Request: request}
}

func syntheticCacheKey(request photos.OriginalRequest) string {
	return filepath.Base(photos.OriginalCachePath("", request.SourceLibraryID, request.ModificationDate, request.Query))
}

func jsonValue(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
