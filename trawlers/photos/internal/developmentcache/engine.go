package developmentcache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

const (
	StopBatchComplete       = "batch_complete"
	StopNoPendingAssets     = "no_pending_assets"
	StopCapacityCeiling     = "capacity_ceiling"
	StopFreeSpaceFloor      = "free_space_floor"
	StopNetworkDisabled     = "network_disabled"
	StopOriginalSizeUnknown = "original_size_unknown"
)

type Config struct {
	SourceLibraryID     string
	BatchSize           int
	CapacityBytes       int64
	FreeSpaceFloorBytes int64
	AllowNetwork        bool
}

type Cache interface {
	Lookup(context.Context, photos.OriginalRequest) (photos.OriginalResolution, bool, error)
	MissSize(photos.OriginalRequest) (int64, bool, error)
	ResolvePackage(context.Context, photos.OriginalRequest, int64) (photos.OriginalResolution, error)
	Usage(context.Context) (photos.DevelopmentCacheUsage, error)
}

type Event struct {
	Phase          string `json:"phase"`
	AssetID        string `json:"asset_id,omitempty"`
	Outcome        string `json:"outcome"`
	DurationMillis int64  `json:"duration_millis"`
}

type Result struct {
	SourceLibraryID string `json:"source_library_id"`
	SnapshotID      string `json:"snapshot_id"`
	Selected        int    `json:"selected"`
	Completed       int    `json:"completed"`
	Reused          int    `json:"reused"`
	RestartReused   int    `json:"restart_reused"`
	Installed       int    `json:"installed"`
	Coalesced       int    `json:"coalesced"`
	StopReason      string `json:"stop_reason"`
}

type Engine struct {
	Select         func(context.Context, string) (archive.DevelopmentCacheSelection, error)
	Cache          Cache
	Checkpoint     *Checkpoint
	AvailableBytes func(context.Context) (int64, error)
	Log            func(Event)
	Clock          func() time.Time
}

func (e Engine) Run(ctx context.Context, config Config) (Result, error) {
	if err := validateConfig(config); err != nil {
		return Result{}, err
	}
	if e.Select == nil || e.Cache == nil || e.Checkpoint == nil || e.AvailableBytes == nil {
		return Result{}, errors.New("development cache engine is not configured")
	}
	started := e.now()
	selection, err := e.Select(ctx, config.SourceLibraryID)
	if err != nil {
		e.log(Event{Phase: "selection", Outcome: "error", DurationMillis: e.elapsed(started)})
		return Result{}, err
	}
	e.log(Event{Phase: "selection", Outcome: "ok", DurationMillis: e.elapsed(started)})
	if selection.SourceLibraryID != config.SourceLibraryID || selection.SnapshotID == "" {
		return Result{}, errors.New("development cache selection does not match the configured source")
	}
	result := Result{
		SourceLibraryID: config.SourceLibraryID,
		SnapshotID:      selection.SnapshotID,
		Selected:        len(selection.Assets),
	}
	checkpointStarted := e.now()
	completed, err := e.Checkpoint.Completed(ctx, config.SourceLibraryID, selection.SnapshotID)
	if err != nil {
		e.log(Event{Phase: "checkpoint_read", Outcome: "error", DurationMillis: e.elapsed(checkpointStarted)})
		return Result{}, err
	}
	e.log(Event{Phase: "checkpoint_read", Outcome: "ok", DurationMillis: e.elapsed(checkpointStarted)})
	seen := map[string]bool{}
	usage := photos.DevelopmentCacheUsage{}
	usageLoaded := false

	for _, asset := range selection.Assets {
		if strings.TrimSpace(asset.AssetID) == "" || strings.TrimSpace(asset.CacheKey) == "" || asset.Request.SourceLibraryID != config.SourceLibraryID {
			return result, errors.New("development cache selection contains a stale or mismatched asset")
		}
		if seen[asset.CacheKey] {
			result.Coalesced++
			e.log(Event{Phase: "cache_lookup", AssetID: asset.AssetID, Outcome: "coalesced", DurationMillis: 0})
			continue
		}
		seen[asset.CacheKey] = true

		lookupStarted := e.now()
		resolved, found, err := e.Cache.Lookup(ctx, asset.Request)
		if err != nil {
			e.log(Event{Phase: "cache_lookup", AssetID: asset.AssetID, Outcome: "error", DurationMillis: e.elapsed(lookupStarted)})
			return result, err
		}
		if found {
			e.log(Event{Phase: "cache_lookup", AssetID: asset.AssetID, Outcome: "checked_hit", DurationMillis: e.elapsed(lookupStarted)})
			wasCompleted := completionMatches(completed[asset.CacheKey], asset.AssetID, resolved)
			if !wasCompleted {
				if err := e.recordCompletion(ctx, selection, asset, resolved); err != nil {
					resolved.Lease.Close()
					return result, err
				}
				result.Completed++
			}
			resolved.Lease.Close()
			result.Reused++
			if wasCompleted {
				result.RestartReused++
			}
			if result.Completed >= config.BatchSize {
				result.StopReason = StopBatchComplete
				return result, nil
			}
			continue
		}
		e.log(Event{Phase: "cache_lookup", AssetID: asset.AssetID, Outcome: "miss", DurationMillis: e.elapsed(lookupStarted)})

		reservationBytes, sizeKnown, err := e.Cache.MissSize(asset.Request)
		if err != nil {
			e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: "error", DurationMillis: 0})
			return result, err
		}
		if !sizeKnown {
			if !config.AllowNetwork {
				e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: StopNetworkDisabled, DurationMillis: 0})
				result.StopReason = StopNetworkDisabled
				return result, nil
			}
			e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: StopOriginalSizeUnknown, DurationMillis: 0})
			result.StopReason = StopOriginalSizeUnknown
			return result, nil
		}
		if !usageLoaded {
			usageStarted := e.now()
			usage, err = e.Cache.Usage(ctx)
			if err != nil {
				e.log(Event{Phase: "cache_usage", AssetID: asset.AssetID, Outcome: "error", DurationMillis: e.elapsed(usageStarted)})
				return result, err
			}
			e.log(Event{Phase: "cache_usage", AssetID: asset.AssetID, Outcome: "ok", DurationMillis: e.elapsed(usageStarted)})
			usageLoaded = true
		}
		if crossesLimit(usage.Bytes, reservationBytes, config.CapacityBytes) {
			e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: StopCapacityCeiling, DurationMillis: 0})
			result.StopReason = StopCapacityCeiling
			return result, nil
		}
		available, err := e.AvailableBytes(ctx)
		if err != nil {
			e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: "error", DurationMillis: 0})
			return result, fmt.Errorf("read development cache free space: %w", err)
		}
		if available < config.FreeSpaceFloorBytes || crossesLimit(config.FreeSpaceFloorBytes, reservationBytes, available) {
			e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: StopFreeSpaceFloor, DurationMillis: 0})
			result.StopReason = StopFreeSpaceFloor
			return result, nil
		}
		e.log(Event{Phase: "capacity", AssetID: asset.AssetID, Outcome: "ok", DurationMillis: 0})

		request := asset.Request
		request.AllowNetwork = config.AllowNetwork
		resolveStarted := e.now()
		resolved, err = e.Cache.ResolvePackage(ctx, request, reservationBytes)
		if errors.Is(err, photos.ErrDevelopmentOriginalSizeUnknown) {
			e.log(Event{Phase: "resolution", AssetID: asset.AssetID, Outcome: StopOriginalSizeUnknown, DurationMillis: e.elapsed(resolveStarted)})
			result.StopReason = StopOriginalSizeUnknown
			return result, nil
		}
		if errors.Is(err, photos.ErrDevelopmentCacheNetworkDisabled) {
			e.log(Event{Phase: "resolution", AssetID: asset.AssetID, Outcome: StopNetworkDisabled, DurationMillis: e.elapsed(resolveStarted)})
			result.StopReason = StopNetworkDisabled
			return result, nil
		}
		if err != nil {
			e.log(Event{Phase: "resolution", AssetID: asset.AssetID, Outcome: "error", DurationMillis: e.elapsed(resolveStarted)})
			return result, err
		}
		e.log(Event{Phase: "resolution", AssetID: asset.AssetID, Outcome: "checked", DurationMillis: e.elapsed(resolveStarted)})
		if err := e.recordCompletion(ctx, selection, asset, resolved); err != nil {
			resolved.Lease.Close()
			return result, err
		}
		resolved.Lease.Close()
		usage.Bytes += resolved.Size
		usage.Files++
		result.Completed++
		result.Installed++
		if result.Completed >= config.BatchSize {
			result.StopReason = StopBatchComplete
			return result, nil
		}
	}
	result.StopReason = StopNoPendingAssets
	return result, nil
}

func validateConfig(config Config) error {
	if config.SourceLibraryID == "" {
		return errors.New("source library ID is required")
	}
	if config.BatchSize <= 0 {
		return errors.New("development cache batch size must be positive")
	}
	if config.CapacityBytes <= 0 {
		return errors.New("development cache capacity must be positive")
	}
	if config.FreeSpaceFloorBytes < 0 {
		return errors.New("development cache free-space floor must be non-negative")
	}
	return nil
}

func (e Engine) recordCompletion(ctx context.Context, selection archive.DevelopmentCacheSelection, asset archive.DevelopmentCacheAsset, resolved photos.OriginalResolution) error {
	started := e.now()
	if resolved.Size <= 0 || resolved.SHA256 == "" {
		e.log(Event{Phase: "checkpoint", AssetID: asset.AssetID, Outcome: "unchecked", DurationMillis: e.elapsed(started)})
		return errors.New("development cache resolver returned an unchecked original")
	}
	err := e.Checkpoint.Record(ctx, selection.SourceLibraryID, selection.SnapshotID, asset.CacheKey, Completion{
		AssetID:   asset.AssetID,
		SizeBytes: resolved.Size,
		SHA256:    resolved.SHA256,
	})
	outcome := "ok"
	if err != nil {
		outcome = "error"
	}
	e.log(Event{Phase: "checkpoint", AssetID: asset.AssetID, Outcome: outcome, DurationMillis: e.elapsed(started)})
	return err
}

func (e Engine) log(event Event) {
	if e.Log != nil {
		e.Log(event)
	}
}

func (e Engine) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

func (e Engine) elapsed(started time.Time) int64 {
	return e.now().Sub(started).Milliseconds()
}

func completionMatches(completion Completion, assetID string, resolved photos.OriginalResolution) bool {
	return completion.AssetID == assetID && completion.SizeBytes == resolved.Size && completion.SHA256 == resolved.SHA256
}

func crossesLimit(current, addition, limit int64) bool {
	return current > limit || addition > limit-current
}
