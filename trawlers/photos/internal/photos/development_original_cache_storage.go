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
	return copyOpenedOriginalFile(ctx, input, destination, 0)
}

func copyReservedOriginalFile(ctx context.Context, source, destination string, reservedSize int64) error {
	fd, err := syscall.Open(source, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDevelopmentOriginalSizeUnknown, err)
	}
	input := os.NewFile(uintptr(fd), source)
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != reservedSize {
		return ErrDevelopmentOriginalSizeUnknown
	}
	return copyOpenedOriginalFile(ctx, input, destination, reservedSize)
}

func copyOpenedOriginalFile(ctx context.Context, input *os.File, destination string, expectedSize int64) error {
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	buffer := make([]byte, 128*1024)
	var copyErr error
	var total int64
	for copyErr == nil {
		if err := ctx.Err(); err != nil {
			copyErr = err
			break
		}
		read, readErr := input.Read(buffer)
		if read > 0 {
			if expectedSize > 0 && int64(read) > expectedSize-total {
				copyErr = ErrDevelopmentOriginalSizeUnknown
				break
			}
			written, writeErr := output.Write(buffer[:read])
			if writeErr != nil {
				copyErr = writeErr
				break
			}
			if written != read {
				copyErr = io.ErrShortWrite
				break
			}
			total += int64(written)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				copyErr = fmt.Errorf("%w: %v", errPackageOriginalUnavailable, readErr)
			}
			break
		}
	}
	if copyErr == nil && expectedSize > 0 && total != expectedSize {
		copyErr = ErrDevelopmentOriginalSizeUnknown
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
