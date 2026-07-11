package photos

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

const OriginalSourceDevelopmentCache = "cached_development_original"

var errPackageOriginalUnavailable = errors.New("package original is unavailable")

type developmentVolume struct {
	MountPoint string
	Mounted    bool
	Local      bool
	External   bool
	Physical   bool
	Writable   bool
}

type developmentVolumeInspector func(string) (developmentVolume, error)

// DevelopmentOriginalResolver keeps checked originals in one pre-existing
// external directory. It shares the product resolver's cache key and locks but
// has a separate, non-evicting policy.
type DevelopmentOriginalResolver struct {
	cache      *OriginalResolver
	removeFile func(string) error
}

func NewDevelopmentOriginalResolver(root, sourceRoot string, exporter OriginalExporter) (*DevelopmentOriginalResolver, error) {
	return newDevelopmentOriginalResolver(root, sourceRoot, inspectDevelopmentVolume, exporter)
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
	if r == nil || r.cache == nil {
		return OriginalResolution{}, errors.New("development original resolver is not configured")
	}
	if strings.TrimSpace(request.Query.LocalIdentifier) == "" {
		return OriginalResolution{}, errors.New("asset local identifier is required for original resolution")
	}
	if strings.TrimSpace(request.SourceLibraryID) == "" {
		return OriginalResolution{}, errors.New("source library ID is required for original resolution")
	}
	if !IsOriginalExtension(filepath.Ext(request.Query.OriginalFilename)) && !IsOriginalUTI(request.Query.OriginalUTI) {
		return OriginalResolution{}, errors.New("camera original resource metadata is required for original resolution")
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
		copyErr := copyOriginalFile(ctx, candidate.Path, temporaryPath)
		if copyErr == nil {
			source = OriginalSourcePackage
			exported = false
		} else if ctxErr := ctx.Err(); ctxErr != nil {
			return OriginalResolution{}, ctxErr
		} else if !errors.Is(copyErr, errPackageOriginalUnavailable) {
			return OriginalResolution{}, fmt.Errorf("copy package original into development cache: %w", copyErr)
		}
	}
	if exported {
		_ = os.Remove(temporaryPath)
		if !request.AllowNetwork {
			return OriginalResolution{}, errors.New("external development cache miss requires network opt-in")
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

func validateDevelopmentCacheRoot(root, sourceRoot string, inspectVolume developmentVolumeInspector) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("external development cache path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve external development cache path: %w", err)
	}
	info, err := os.Lstat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("external development cache root must already be mounted and created")
		}
		return "", fmt.Errorf("inspect external development cache root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("external development cache root must not be a symlink")
	}
	if !info.IsDir() {
		return "", errors.New("external development cache root must be a directory")
	}
	if info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("external development cache root permissions are %04o, want 0700", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return "", errors.New("external development cache root is not owned by the current user")
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve external development cache root: %w", err)
	}
	canonicalSource, err := canonicalExistingPath(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Photos source root: %w", err)
	}
	if pathsOverlap(canonicalRoot, canonicalSource) {
		return "", errors.New("external development cache root overlaps the Photos source root")
	}
	volume, err := inspectVolume(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("inspect external development cache volume: %w", err)
	}
	if !volume.Mounted || !volume.Local || !volume.External || !volume.Physical || !volume.Writable {
		return "", errors.New("development cache root must be on a mounted, writable, local external physical volume")
	}
	canonicalMount, err := canonicalExistingPath(volume.MountPoint)
	if err != nil || !pathContains(canonicalMount, canonicalRoot) {
		return "", errors.New("development cache root does not belong to the inspected mounted volume")
	}
	return canonicalRoot, nil
}

func ensureDevelopmentCacheLocks(root string) error {
	locks := filepath.Join(root, ".locks")
	info, err := os.Lstat(locks)
	if os.IsNotExist(err) {
		if err := os.Mkdir(locks, 0o700); err != nil {
			return fmt.Errorf("create development cache locks: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect development cache locks: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("development cache locks path must be a private directory, not a symlink")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("development cache locks permissions are %04o, want 0700", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("development cache locks path is not owned by the current user")
	}
	return nil
}

func canonicalExistingPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absPath)
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func (r *DevelopmentOriginalResolver) cleanupAfterRestart() error {
	entries, err := os.ReadDir(r.cache.root)
	if err != nil {
		return fmt.Errorf("read development originals cache: %w", err)
	}
	targets := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if target, ok := originalCacheTargetForTemporary(entry.Name()); ok {
			targets[filepath.Join(r.cache.root, target)] = true
			continue
		}
		if target, ok := developmentProofTargetForTemporary(entry.Name()); ok {
			targets[filepath.Join(r.cache.root, target)] = true
			continue
		}
		if target, ok := strings.CutSuffix(entry.Name(), ".proof.json"); ok && target != "" {
			targets[filepath.Join(r.cache.root, target)] = true
			continue
		}
		if target, ok := strings.CutSuffix(entry.Name(), ".exporting"); ok && target != "" {
			targets[filepath.Join(r.cache.root, target)] = true
			continue
		}
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		targets[filepath.Join(r.cache.root, entry.Name())] = true
	}
	paths := make([]string, 0, len(targets))
	for path := range targets {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		entryLock, err := r.cache.lock(context.Background(), path, syscall.LOCK_EX)
		if err != nil {
			return fmt.Errorf("lock development cache during restart cleanup: %w", err)
		}
		var cleanupErr error
		if _, _, ok := r.inspectCachedOriginal(path); ok {
			cleanupErr = r.removeFiles(
				originalCacheTemporaryPath(path),
				originalCacheTemporaryPath(path)+".exporting",
				developmentProofTemporaryPath(path),
			)
		} else {
			cleanupErr = r.removeCachedOriginal(path)
		}
		entryLock.Close()
		if cleanupErr != nil {
			return cleanupErr
		}
	}
	return nil
}

func writeDevelopmentOriginalCacheProof(path string, size int64, digest []byte) error {
	proof := originalCacheProof{Version: originalCacheProofVersion, Size: size, SHA256: hex.EncodeToString(digest)}
	data, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("encode development original proof: %w", err)
	}
	temporaryPath := developmentProofTemporaryPath(path)
	_ = os.Remove(temporaryPath)
	temporary, err := os.OpenFile(temporaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create development original proof: %w", err)
	}
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write development original proof: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync development original proof: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close development original proof: %w", err)
	}
	if err := os.Rename(temporaryPath, originalCacheProofPath(path)); err != nil {
		return fmt.Errorf("install development original proof: %w", err)
	}
	return nil
}

func developmentProofTemporaryPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".proof.incoming")
}

func developmentProofTargetForTemporary(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".proof.incoming") {
		return "", false
	}
	target := strings.TrimSuffix(strings.TrimPrefix(name, "."), ".proof.incoming")
	return target, target != ""
}

func (r *DevelopmentOriginalResolver) removeCachedOriginal(path string) error {
	return r.removeFiles(
		path,
		path+".exporting",
		originalCacheTemporaryPath(path),
		originalCacheTemporaryPath(path)+".exporting",
		originalCacheProofPath(path),
		developmentProofTemporaryPath(path),
	)
}

func (r *DevelopmentOriginalResolver) removeFiles(paths ...string) error {
	for _, path := range paths {
		if err := r.removeAndVerifyAbsent(path); err != nil {
			return err
		}
	}
	return nil
}

func (r *DevelopmentOriginalResolver) removeAndVerifyAbsent(path string) error {
	removeFile := r.removeFile
	if removeFile == nil {
		removeFile = os.Remove
	}
	if err := removeFile(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove unsafe development cache artifact %q: %w", path, err)
	}
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("unsafe development cache artifact remains after cleanup: %q", path)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("verify development cache cleanup for %q: %w", path, err)
	}
	return nil
}

func (r *DevelopmentOriginalResolver) inspectCachedOriginal(path string) (os.FileInfo, [32]byte, bool) {
	mediaInfo, err := os.Lstat(path)
	if err != nil || mediaInfo.Mode()&os.ModeSymlink != 0 || !mediaInfo.Mode().IsRegular() || mediaInfo.Size() <= 0 {
		return nil, [32]byte{}, false
	}
	proofInfo, err := os.Lstat(originalCacheProofPath(path))
	if err != nil || proofInfo.Mode()&os.ModeSymlink != 0 || !proofInfo.Mode().IsRegular() {
		return nil, [32]byte{}, false
	}
	return inspectCachedOriginal(path)
}

func copyOriginalFile(ctx context.Context, source, destination string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("%w: %v", errPackageOriginalUnavailable, err)
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil {
		return fmt.Errorf("%w: %v", errPackageOriginalUnavailable, err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return fmt.Errorf("%w: not a non-empty regular file", errPackageOriginalUnavailable)
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	buffer := make([]byte, 128*1024)
	var copyErr error
	for copyErr == nil {
		if err := ctx.Err(); err != nil {
			copyErr = err
			break
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			written, writeErr := output.Write(buffer[:read])
			if writeErr != nil {
				copyErr = writeErr
				break
			}
			if written != read {
				copyErr = io.ErrShortWrite
				break
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				copyErr = fmt.Errorf("%w: %v", errPackageOriginalUnavailable, readErr)
			}
			break
		}
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
