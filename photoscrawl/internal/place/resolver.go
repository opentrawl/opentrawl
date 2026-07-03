package place

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ResolverOptions struct {
	CacheDir           string
	LegacyBackfillDir  string
	RadiusMeters       float64
	ProviderStartEvery time.Duration
}

type Resolver struct {
	cacheDir      string
	backfillDir   string
	radius        float64
	startEvery    time.Duration
	mu            sync.Mutex
	nextProvider  time.Time
	manifestOnce  sync.Once
	manifest      map[string]backfillManifestEntry
	manifestError error
}

type ResolveResult struct {
	Result           *Result
	CacheStatus      string
	BackfillHit      bool
	ProviderAttempt  bool
	ProviderError    string
	ResolvedFromPath string
}

type backfillManifestEntry struct {
	Index          int     `json:"index"`
	Latitude       float64 `json:"latitude"`
	Longitude      float64 `json:"longitude"`
	AccuracyMeters float64 `json:"accuracy_meters,omitempty"`
}

func NewResolver(opts ResolverOptions) *Resolver {
	radius := opts.RadiusMeters
	if radius <= 0 {
		radius = defaultRadiusMeters
	}
	startEvery := opts.ProviderStartEvery
	if startEvery <= 0 {
		startEvery = time.Second
	}
	return &Resolver{
		cacheDir:    strings.TrimSpace(opts.CacheDir),
		backfillDir: strings.TrimSpace(opts.LegacyBackfillDir),
		radius:      radius,
		startEvery:  startEvery,
	}
}

func (r *Resolver) Resolve(ctx context.Context, input Input) ResolveResult {
	if r == nil || strings.TrimSpace(r.cacheDir) == "" {
		return ResolveResult{CacheStatus: "disabled"}
	}
	if err := os.MkdirAll(r.cacheDir, 0o700); err != nil {
		return ResolveResult{CacheStatus: "error", ProviderError: err.Error()}
	}
	if result, path := r.loadCache(input); result != nil {
		result.Cached = true
		result.CacheStatus = "hit"
		return ResolveResult{Result: result, CacheStatus: "hit", ResolvedFromPath: path}
	}
	if result, path := r.loadBackfill(input); result != nil {
		result.Cached = true
		result.CacheStatus = "backfill_hit"
		return ResolveResult{Result: result, CacheStatus: "backfill_hit", BackfillHit: true, ResolvedFromPath: path}
	}
	if err := r.waitProvider(ctx); err != nil {
		return ResolveResult{CacheStatus: "miss", ProviderAttempt: true, ProviderError: err.Error()}
	}
	result, err := rawAppleResult(ctx, input, r.radius)
	if err != nil {
		return ResolveResult{CacheStatus: "miss", ProviderAttempt: true, ProviderError: err.Error()}
	}
	result.CacheStatus = "miss_filled"
	if path, err := cachePath(r.cacheDir, input, r.radius); err == nil {
		if data, err := json.MarshalIndent(result, "", "  "); err == nil {
			_ = os.WriteFile(path, append(data, '\n'), 0o600)
		}
	}
	return ResolveResult{Result: &result, CacheStatus: "miss_filled", ProviderAttempt: true}
}

func (r *Resolver) loadCache(input Input) (*Result, string) {
	paths := []string{}
	if path, err := cachePath(r.cacheDir, input, r.radius); err == nil {
		paths = append(paths, path)
	}
	if path, err := legacyCachePath(r.cacheDir, input, r.radius); err == nil {
		paths = append(paths, path)
	}
	for _, path := range compactStrings(paths) {
		result, ok := loadResolvedResult(path, input, r.radius)
		if ok {
			return &result, path
		}
	}
	return nil, ""
}

func (r *Resolver) loadBackfill(input Input) (*Result, string) {
	manifest := r.backfillManifest()
	if len(manifest) == 0 {
		return nil, ""
	}
	entry, ok := manifest[coordinateKey(input.Location.Latitude, input.Location.Longitude, input.AccuracyMeters)]
	if !ok {
		return nil, ""
	}
	path := filepath.Join(r.backfillDir, "outputs", fmt.Sprintf("%06d.json", entry.Index))
	result, ok := loadResolvedResult(path, input, r.radius)
	if !ok {
		return nil, ""
	}
	return &result, path
}

func (r *Resolver) backfillManifest() map[string]backfillManifestEntry {
	r.manifestOnce.Do(func() {
		if strings.TrimSpace(r.backfillDir) == "" {
			return
		}
		data, err := os.ReadFile(filepath.Join(r.backfillDir, "manifest.json"))
		if err != nil {
			r.manifestError = err
			return
		}
		var entries []backfillManifestEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			r.manifestError = err
			return
		}
		r.manifest = make(map[string]backfillManifestEntry, len(entries))
		for _, entry := range entries {
			r.manifest[coordinateKey(entry.Latitude, entry.Longitude, entry.AccuracyMeters)] = entry
		}
	})
	return r.manifest
}

func (r *Resolver) waitProvider(ctx context.Context) error {
	r.mu.Lock()
	now := time.Now()
	if r.nextProvider.IsZero() || now.After(r.nextProvider) {
		r.nextProvider = now
	}
	start := r.nextProvider
	r.nextProvider = start.Add(r.startEvery)
	r.mu.Unlock()
	wait := time.Until(start)
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func loadResolvedResult(path string, input Input, radius float64) (Result, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, false
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, false
	}
	if result.Input.Location.Latitude == 0 && result.Input.Location.Longitude == 0 {
		result.Input = input
	}
	if result.RadiusMeters == 0 {
		result.RadiusMeters = radius
	}
	if result.Provider == "" {
		result.Provider = "apple"
	}
	if result.Source == "" {
		result.Source = "apple_corelocation_mapkit"
	}
	if len(result.Area) == 0 {
		result.Area = areaFromAddress(result.Address)
	}
	if result.POITotal == 0 {
		result.POITotal = len(result.POICandidates)
	}
	if strings.TrimSpace(result.POIStatus) == "" {
		result.POIStatus = poiStatus(result)
	}
	result.POICandidates = calibrateCandidates(input, radius, result.POICandidates)
	return result, validateComplete(result) == nil
}
