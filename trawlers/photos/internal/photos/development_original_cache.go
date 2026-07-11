package photos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const OriginalSourceDevelopmentCache = "cached_development_original"

var errPackageOriginalUnavailable = errors.New("package original is unavailable")

var ErrDevelopmentCacheNetworkDisabled = errors.New("external development cache miss requires network opt-in")

var ErrDevelopmentOriginalSizeUnknown = errors.New("original_size_unknown")

// DevelopmentOriginalResolver keeps checked originals in one pre-existing
// external directory. It shares the product resolver's cache key and locks but
// has a separate, non-evicting policy.
type DevelopmentOriginalResolver struct {
	cache      *OriginalResolver
	removeFile func(string) error
}

type DevelopmentCacheUsage struct {
	Files int
	Bytes int64
}

func NewDevelopmentOriginalResolver(root, sourceRoot string, exporter OriginalExporter) (*DevelopmentOriginalResolver, error) {
	return newDevelopmentOriginalResolver(root, sourceRoot, inspectDevelopmentVolume, exporter)
}

// Lookup returns only a checked cache entry. It never copies package media or
// calls the configured exporter.
func (r *DevelopmentOriginalResolver) Lookup(ctx context.Context, request OriginalRequest) (OriginalResolution, bool, error) {
	if err := r.validateRequest(request); err != nil {
		return OriginalResolution{}, false, err
	}
	path := OriginalCachePath(r.cache.root, request.SourceLibraryID, request.ModificationDate, request.Query)
	entryLock, err := r.cache.lock(ctx, path, syscall.LOCK_SH)
	if err != nil {
		return OriginalResolution{}, false, fmt.Errorf("lock development cache entry for lookup: %w", err)
	}
	info, digest, ok := r.inspectCachedOriginal(path)
	if !ok {
		entryLock.Close()
		return OriginalResolution{}, false, nil
	}
	lease := &OriginalLease{lock: entryLock.file}
	entryLock.file = nil
	return originalResolution(path, OriginalSourceDevelopmentCache, info, digest, false, lease), true, nil
}

// Usage counts only entries whose media and proof reopen successfully.
func (r *DevelopmentOriginalResolver) Usage(ctx context.Context) (DevelopmentCacheUsage, error) {
	if r == nil || r.cache == nil {
		return DevelopmentCacheUsage{}, errors.New("development original resolver is not configured")
	}
	entries, err := os.ReadDir(r.cache.root)
	if err != nil {
		return DevelopmentCacheUsage{}, fmt.Errorf("read development originals cache usage: %w", err)
	}
	var usage DevelopmentCacheUsage
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".proof.json") || strings.HasSuffix(name, ".exporting") {
			continue
		}
		path := filepath.Join(r.cache.root, name)
		entryLock, err := r.cache.lock(ctx, path, syscall.LOCK_SH)
		if err != nil {
			return DevelopmentCacheUsage{}, fmt.Errorf("lock development cache entry for usage: %w", err)
		}
		info, _, ok := r.inspectCachedOriginal(path)
		entryLock.Close()
		if !ok {
			continue
		}
		usage.Files++
		usage.Bytes += info.Size()
	}
	return usage, nil
}

// MissSize returns a stat-checked package original size. A network-backed miss
// has no trustworthy size before export.
func (r *DevelopmentOriginalResolver) MissSize(request OriginalRequest) (int64, bool, error) {
	if err := r.validateRequest(request); err != nil {
		return 0, false, err
	}
	candidate, ok := UniquePackageOriginal(request.PackageCandidates)
	if !ok {
		return 0, false, nil
	}
	info, err := os.Lstat(candidate.Path)
	if os.IsNotExist(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("inspect package original size: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() <= 0 {
		return 0, false, nil
	}
	return info.Size(), true, nil
}

func newDevelopmentOriginalResolver(root, sourceRoot string, inspectVolume developmentVolumeInspector, exporter OriginalExporter) (*DevelopmentOriginalResolver, error) {
	if inspectVolume == nil {
		return nil, errors.New("development cache volume inspector is required")
	}
	if exporter == nil {
		return nil, errors.New("original exporter is required")
	}
	root, err := validateDevelopmentCacheRoot(root, sourceRoot, inspectVolume)
	if err != nil {
		return nil, err
	}
	if err := ensureDevelopmentCacheLocks(root); err != nil {
		return nil, err
	}
	resolver := &DevelopmentOriginalResolver{
		cache:      &OriginalResolver{root: root, export: exporter},
		removeFile: os.Remove,
	}
	if err := resolver.cleanupAfterRestart(); err != nil {
		return nil, err
	}
	return resolver, nil
}

func (r *DevelopmentOriginalResolver) Resolve(ctx context.Context, request OriginalRequest) (OriginalResolution, error) {
	return r.resolve(ctx, request, 0, false)
}

// ResolvePackage installs only the package candidate whose size was reserved.
// It never falls through to the PhotoKit exporter.
func (r *DevelopmentOriginalResolver) ResolvePackage(ctx context.Context, request OriginalRequest, reservedSize int64) (OriginalResolution, error) {
	if reservedSize <= 0 {
		return OriginalResolution{}, ErrDevelopmentOriginalSizeUnknown
	}
	return r.resolve(ctx, request, reservedSize, true)
}

func (r *DevelopmentOriginalResolver) resolve(ctx context.Context, request OriginalRequest, reservedSize int64, packageOnly bool) (OriginalResolution, error) {
	if err := r.validateRequest(request); err != nil {
		return OriginalResolution{}, err
	}

	path := OriginalCachePath(r.cache.root, request.SourceLibraryID, request.ModificationDate, request.Query)
	entryLock, err := r.cache.lock(ctx, path, syscall.LOCK_EX)
	if err != nil {
		return OriginalResolution{}, fmt.Errorf("lock development cache entry: %w", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			entryLock.Close()
		}
	}()

	if info, digest, ok := r.inspectCachedOriginal(path); ok {
		if err := r.removeAndVerifyAbsent(developmentProofTemporaryPath(path)); err != nil {
			return OriginalResolution{}, err
		}
		if err := syscall.Flock(int(entryLock.file.Fd()), syscall.LOCK_SH); err != nil {
			return OriginalResolution{}, fmt.Errorf("lock cached development original for use: %w", err)
		}
		lease := &OriginalLease{lock: entryLock.file}
		entryLock.file = nil
		lockReleased = true
		return originalResolution(path, OriginalSourceDevelopmentCache, info, digest, false, lease), nil
	}
	if err := r.removeCachedOriginal(path); err != nil {
		return OriginalResolution{}, err
	}

	temporaryPath := originalCacheTemporaryPath(path)
	keepInstalled := false
	defer func() {
		if !keepInstalled {
			_ = r.removeCachedOriginal(path)
		}
	}()
	source := OriginalSourcePhotoKit
	exported := true
	if candidate, ok := UniquePackageOriginal(request.PackageCandidates); ok {
		var copyErr error
		if packageOnly {
			copyErr = copyReservedOriginalFile(ctx, candidate.Path, temporaryPath, reservedSize)
		} else {
			copyErr = copyOriginalFile(ctx, candidate.Path, temporaryPath)
		}
		if copyErr == nil {
			source = OriginalSourcePackage
			exported = false
		} else if ctxErr := ctx.Err(); ctxErr != nil {
			return OriginalResolution{}, ctxErr
		} else if packageOnly && (errors.Is(copyErr, errPackageOriginalUnavailable) || errors.Is(copyErr, ErrDevelopmentOriginalSizeUnknown)) {
			return OriginalResolution{}, ErrDevelopmentOriginalSizeUnknown
		} else if !errors.Is(copyErr, errPackageOriginalUnavailable) {
			return OriginalResolution{}, fmt.Errorf("copy package original into development cache: %w", copyErr)
		}
	}
	if exported {
		_ = os.Remove(temporaryPath)
		if packageOnly {
			return OriginalResolution{}, ErrDevelopmentOriginalSizeUnknown
		}
		if !request.AllowNetwork {
			return OriginalResolution{}, ErrDevelopmentCacheNetworkDisabled
		}
		if err := r.cache.export(ctx, request.Query, temporaryPath, true); err != nil {
			return OriginalResolution{}, err
		}
	}
	info, digest, err := InspectOriginalFile(temporaryPath)
	if err != nil {
		return OriginalResolution{}, fmt.Errorf("inspect development original: %w", err)
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return OriginalResolution{}, fmt.Errorf("protect development original: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return OriginalResolution{}, fmt.Errorf("install development original: %w", err)
	}
	if err := writeDevelopmentOriginalCacheProof(path, info.Size(), digest[:]); err != nil {
		return OriginalResolution{}, err
	}
	installedInfo, installedDigest, ok := r.inspectCachedOriginal(path)
	if !ok {
		return OriginalResolution{}, errors.New("reopen checked development original after install")
	}
	if err := syscall.Flock(int(entryLock.file.Fd()), syscall.LOCK_SH); err != nil {
		return OriginalResolution{}, fmt.Errorf("lock development original for use: %w", err)
	}
	lease := &OriginalLease{lock: entryLock.file}
	entryLock.file = nil
	lockReleased = true
	keepInstalled = true
	return originalResolution(path, source, installedInfo, installedDigest, exported, lease), nil
}

func (r *DevelopmentOriginalResolver) validateRequest(request OriginalRequest) error {
	if r == nil || r.cache == nil {
		return errors.New("development original resolver is not configured")
	}
	if strings.TrimSpace(request.Query.LocalIdentifier) == "" {
		return errors.New("asset local identifier is required for original resolution")
	}
	if strings.TrimSpace(request.SourceLibraryID) == "" {
		return errors.New("source library ID is required for original resolution")
	}
	if !IsOriginalExtension(filepath.Ext(request.Query.OriginalFilename)) && !IsOriginalUTI(request.Query.OriginalUTI) {
		return errors.New("camera original resource metadata is required for original resolution")
	}
	return nil
}
