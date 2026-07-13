package photos

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ProductOriginalCacheMaxBytes int64 = 4 * 1024 * 1024 * 1024
	originalCacheProofVersion          = 1
)

const (
	OriginalSourcePackage  = "photos_package_original"
	OriginalSourceCache    = "cached_photokit_original"
	OriginalSourcePhotoKit = "photokit_original_export"
)

type OriginalExporter func(context.Context, OriginalExportQuery, string, bool) error

type OriginalRequest struct {
	SourceLibraryID   string
	ModificationDate  string
	Query             OriginalExportQuery
	PackageCandidates []LocalMediaCandidate
	AllowNetwork      bool
}

type OriginalResolution struct {
	Path     string
	Source   string
	Size     int64
	SHA256   string
	Exported bool
	Lease    *OriginalLease
}

type OriginalResolver struct {
	root     string
	maxBytes int64
	export   OriginalExporter
}

type OriginalLease struct {
	resolver *OriginalResolver
	lock     *os.File
	once     sync.Once
}

type originalCacheProof struct {
	Version int    `json:"version"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
}

type originalCacheEntry struct {
	path    string
	size    int64
	modTime time.Time
}

type originalFileLock struct {
	file *os.File
}

func NewOriginalResolver(root string, exporter OriginalExporter) (*OriginalResolver, error) {
	return newOriginalResolver(root, ProductOriginalCacheMaxBytes, exporter)
}

func newOriginalResolver(root string, maxBytes int64, exporter OriginalExporter) (*OriginalResolver, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("originals cache path is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("originals cache budget must be positive")
	}
	if exporter == nil {
		return nil, errors.New("original exporter is required")
	}
	if err := ensurePrivateDirectory(root); err != nil {
		return nil, fmt.Errorf("create originals cache: %w", err)
	}
	if err := ensurePrivateDirectory(filepath.Join(root, ".locks")); err != nil {
		return nil, fmt.Errorf("create originals cache locks: %w", err)
	}
	resolver := &OriginalResolver{root: root, maxBytes: maxBytes, export: exporter}
	if err := resolver.prune(); err != nil {
		return nil, err
	}
	return resolver, nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func (r *OriginalResolver) Resolve(ctx context.Context, request OriginalRequest) (OriginalResolution, error) {
	if r == nil {
		return OriginalResolution{}, errors.New("original resolver is not configured")
	}
	if strings.TrimSpace(request.Query.LocalIdentifier) == "" {
		return OriginalResolution{}, errors.New("asset local identifier is required for original resolution")
	}
	if strings.TrimSpace(request.SourceLibraryID) == "" {
		return OriginalResolution{}, errors.New("source library ID is required for original resolution")
	}
	if candidate, ok := UniquePackageOriginal(request.PackageCandidates); ok {
		if info, digest, err := InspectOriginalFile(candidate.Path); err == nil {
			return originalResolution(candidate.Path, OriginalSourcePackage, info, digest, false, nil), nil
		}
	}
	if !IsOriginalExtension(filepath.Ext(request.Query.OriginalFilename)) && !IsOriginalUTI(request.Query.OriginalUTI) {
		return OriginalResolution{}, errors.New("camera original resource metadata is required for PhotoKit export")
	}

	path := OriginalCachePath(r.root, request.SourceLibraryID, request.ModificationDate, request.Query)
	entryLock, err := r.lock(ctx, path, syscall.LOCK_EX)
	if err != nil {
		return OriginalResolution{}, fmt.Errorf("lock original cache entry: %w", err)
	}
	lockReleased := false
	defer func() {
		if !lockReleased {
			entryLock.Close()
		}
	}()

	if info, digest, ok := inspectCachedOriginal(path); ok {
		_ = os.Chtimes(path, time.Now(), time.Now())
		if err := syscall.Flock(int(entryLock.file.Fd()), syscall.LOCK_SH); err != nil {
			return OriginalResolution{}, fmt.Errorf("lock cached original for use: %w", err)
		}
		lease := &OriginalLease{resolver: r, lock: entryLock.file}
		entryLock.file = nil
		lockReleased = true
		return originalResolution(path, OriginalSourceCache, info, digest, false, lease), nil
	}
	removeCachedOriginal(path)

	temporaryPath := originalCacheTemporaryPath(path)
	_ = os.Remove(temporaryPath)
	_ = os.Remove(temporaryPath + ".exporting")
	defer func() {
		_ = os.Remove(temporaryPath)
		_ = os.Remove(temporaryPath + ".exporting")
	}()
	if err := r.export(ctx, request.Query, temporaryPath, request.AllowNetwork); err != nil {
		removeCachedOriginal(path)
		return OriginalResolution{}, err
	}
	info, digest, err := InspectOriginalFile(temporaryPath)
	if err != nil {
		removeCachedOriginal(path)
		return OriginalResolution{}, fmt.Errorf("inspect exported original: %w", err)
	}
	if info.Size() > r.maxBytes {
		removeCachedOriginal(path)
		return OriginalResolution{}, fmt.Errorf("exported original exceeds cache budget: %d bytes", info.Size())
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		removeCachedOriginal(path)
		return OriginalResolution{}, fmt.Errorf("protect exported original: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		removeCachedOriginal(path)
		return OriginalResolution{}, fmt.Errorf("install exported original: %w", err)
	}
	if err := writeOriginalCacheProof(path, info.Size(), digest[:]); err != nil {
		removeCachedOriginal(path)
		return OriginalResolution{}, err
	}
	if err := syscall.Flock(int(entryLock.file.Fd()), syscall.LOCK_SH); err != nil {
		removeCachedOriginal(path)
		return OriginalResolution{}, fmt.Errorf("lock exported original for use: %w", err)
	}
	lease := &OriginalLease{resolver: r, lock: entryLock.file}
	entryLock.file = nil
	lockReleased = true
	if err := r.prune(); err != nil {
		lease.Close()
		removeCachedOriginal(path)
		return OriginalResolution{}, err
	}
	return originalResolution(path, OriginalSourcePhotoKit, info, digest, true, lease), nil
}

func originalResolution(path, source string, info os.FileInfo, digest [32]byte, exported bool, lease *OriginalLease) OriginalResolution {
	return OriginalResolution{
		Path:     path,
		Source:   source,
		Size:     info.Size(),
		SHA256:   hex.EncodeToString(digest[:]),
		Exported: exported,
		Lease:    lease,
	}
}

func (l *OriginalLease) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		if l.lock != nil {
			_ = syscall.Flock(int(l.lock.Fd()), syscall.LOCK_UN)
			_ = l.lock.Close()
			l.lock = nil
		}
		if l.resolver != nil {
			_ = l.resolver.prune()
		}
	})
}

func inspectCachedOriginal(path string) (os.FileInfo, [32]byte, bool) {
	info, digest, err := InspectOriginalFile(path)
	if err != nil {
		return nil, [32]byte{}, false
	}
	data, err := os.ReadFile(originalCacheProofPath(path))
	if err != nil {
		return nil, [32]byte{}, false
	}
	var proof originalCacheProof
	if err := json.Unmarshal(data, &proof); err != nil {
		return nil, [32]byte{}, false
	}
	if proof.Version != originalCacheProofVersion || proof.Size != info.Size() || !strings.EqualFold(proof.SHA256, hex.EncodeToString(digest[:])) {
		return nil, [32]byte{}, false
	}
	return info, digest, true
}

// ReadCachedOriginal reopens one hash-proved immutable original without
// creating, pruning or touching the cache.
func ReadCachedOriginal(root string, request OriginalRequest) (string, int64, string, bool) {
	path := OriginalCachePath(root, request.SourceLibraryID, request.ModificationDate, request.Query)
	info, digest, ok := inspectCachedOriginal(path)
	if !ok {
		return "", 0, "", false
	}
	return path, info.Size(), hex.EncodeToString(digest[:]), true
}

func writeOriginalCacheProof(path string, size int64, digest []byte) error {
	proof := originalCacheProof{Version: originalCacheProofVersion, Size: size, SHA256: hex.EncodeToString(digest)}
	data, err := json.Marshal(proof)
	if err != nil {
		return fmt.Errorf("encode original cache proof: %w", err)
	}
	return writeOriginalCacheProofData(path, append(data, '\n'))
}

// writeOriginalCacheProofData is shared by the two media roles. Both use the
// same private root, atomic proof installation and entry locks.
func writeOriginalCacheProofData(path string, data []byte) error {
	proofPath := originalCacheProofPath(path)
	temporary, err := os.CreateTemp(filepath.Dir(proofPath), ".original-proof-*")
	if err != nil {
		return fmt.Errorf("create original cache proof: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("protect original cache proof: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write original cache proof: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close original cache proof: %w", err)
	}
	if err := os.Rename(temporaryPath, proofPath); err != nil {
		return fmt.Errorf("install original cache proof: %w", err)
	}
	return nil
}

func originalCacheProofPath(path string) string {
	return path + ".proof.json"
}

func originalCacheTemporaryPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".incoming")
}

func removeCachedOriginal(path string) {
	_ = os.Remove(path)
	_ = os.Remove(path + ".exporting")
	_ = os.Remove(originalCacheTemporaryPath(path))
	_ = os.Remove(originalCacheTemporaryPath(path) + ".exporting")
	_ = os.Remove(originalCacheProofPath(path))
}

func (r *OriginalResolver) prune() error {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return fmt.Errorf("read originals cache: %w", err)
	}
	if err := r.removeUnlockedTemporaryOriginals(entries); err != nil {
		return err
	}
	files := make([]originalCacheEntry, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".proof.json") || strings.HasSuffix(entry.Name(), ".exporting") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect originals cache: %w", err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(r.root, entry.Name())
		files = append(files, originalCacheEntry{path: path, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.Before(files[j].modTime)
		}
		return files[i].path < files[j].path
	})
	return r.pruneSnapshot(files, total)
}

func (r *OriginalResolver) pruneSnapshot(files []originalCacheEntry, total int64) error {
	for _, file := range files {
		if total <= r.maxBytes {
			break
		}
		entryLock, ok, err := r.tryLock(file.path, syscall.LOCK_EX)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		info, statErr := os.Stat(file.path)
		if statErr == nil {
			removeCachedOriginal(file.path)
			total -= info.Size()
		} else if os.IsNotExist(statErr) {
			// Another resolver may have removed this entry after both took
			// their cache snapshots. It still contributed to this total.
			total -= file.size
		} else {
			entryLock.Close()
			return fmt.Errorf("inspect cached original for eviction: %w", statErr)
		}
		entryLock.Close()
	}
	return nil
}

func (r *OriginalResolver) removeUnlockedTemporaryOriginals(entries []os.DirEntry) error {
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		targetName, ok := originalCacheTargetForTemporary(entry.Name())
		if !ok {
			continue
		}
		targetPath := filepath.Join(r.root, targetName)
		entryLock, acquired, err := r.tryLock(targetPath, syscall.LOCK_EX)
		if err != nil {
			return fmt.Errorf("lock stale original temporary: %w", err)
		}
		if !acquired {
			continue
		}
		temporaryPath := filepath.Join(r.root, entry.Name())
		removeErr := os.Remove(temporaryPath)
		entryLock.Close()
		if removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("remove stale original temporary: %w", removeErr)
		}
	}
	return nil
}

func originalCacheTargetForTemporary(name string) (string, bool) {
	if !strings.HasPrefix(name, ".") {
		return "", false
	}
	name = strings.TrimPrefix(name, ".")
	for _, suffix := range []string{".incoming.exporting", ".incoming"} {
		if target, ok := strings.CutSuffix(name, suffix); ok && target != "" {
			return target, true
		}
	}
	return "", false
}

func (r *OriginalResolver) lock(ctx context.Context, path string, mode int) (*originalFileLock, error) {
	file, err := os.OpenFile(r.lockPath(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		if err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB); err == nil {
			return &originalFileLock{file: file}, nil
		} else if !lockWouldBlock(err) {
			_ = file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (r *OriginalResolver) tryLock(path string, mode int) (*originalFileLock, bool, error) {
	file, err := os.OpenFile(r.lockPath(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(file.Fd()), mode|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if lockWouldBlock(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &originalFileLock{file: file}, true, nil
}

func lockWouldBlock(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}

func (r *OriginalResolver) lockPath(path string) string {
	return filepath.Join(r.root, ".locks", filepath.Base(path)+".lock")
}

func (l *originalFileLock) Close() {
	if l == nil || l.file == nil {
		return
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil
}
