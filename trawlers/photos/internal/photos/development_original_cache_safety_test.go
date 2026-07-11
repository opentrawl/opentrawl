package photos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDevelopmentCacheRejectsSymlinkedMediaUnderEntryLock(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	var helperCalls atomic.Int32
	resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, func(context.Context, OriginalExportQuery, string, bool) error {
		helperCalls.Add(1)
		return errors.New("synthetic helper must not run")
	})
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("symlinked-media")
	request.AllowNetwork = false
	finalPath := OriginalCachePath(resolver.cache.root, request.SourceLibraryID, request.ModificationDate, request.Query)
	outsidePath := filepath.Join(t.TempDir(), "outside-original.jpeg")
	outsideBytes := []byte("synthetic bytes outside the canonical cache root")
	if err := os.WriteFile(outsidePath, outsideBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	outsideInfo, outsideDigest, err := InspectOriginalFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, finalPath); err != nil {
		t.Fatal(err)
	}
	if err := writeOriginalCacheProof(finalPath, outsideInfo.Size(), outsideDigest[:]); err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "requires network opt-in") {
		t.Fatalf("Resolve error = %v, want rejected symlink cache miss", err)
	}
	resolveErr := err
	if helperCalls.Load() != 0 {
		t.Fatalf("helper calls = %d, want 0", helperCalls.Load())
	}
	gotOutside, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotOutside) != string(outsideBytes) {
		t.Fatalf("outside media changed: %q", gotOutside)
	}
	for _, path := range []string{finalPath, originalCacheProofPath(finalPath)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unsafe cache artifact remains at %q: %v", path, err)
		}
	}
	t.Logf("boundary symlinked_media input=%q outside=%q output=%q helper_calls=%d", finalPath, outsidePath, resolveErr, helperCalls.Load())
}

func TestDevelopmentCacheRejectsSymlinkedProofUnderEntryLock(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	var helperCalls atomic.Int32
	resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, func(context.Context, OriginalExportQuery, string, bool) error {
		helperCalls.Add(1)
		return errors.New("synthetic helper must not run")
	})
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("symlinked-proof")
	request.AllowNetwork = false
	finalPath := OriginalCachePath(resolver.cache.root, request.SourceLibraryID, request.ModificationDate, request.Query)
	mediaBytes := []byte("synthetic media with proof outside the cache root")
	if err := os.WriteFile(finalPath, mediaBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	mediaInfo, mediaDigest, err := InspectOriginalFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	outsideBase := filepath.Join(t.TempDir(), "outside-proof-owner")
	if err := writeOriginalCacheProof(outsideBase, mediaInfo.Size(), mediaDigest[:]); err != nil {
		t.Fatal(err)
	}
	outsideProof := originalCacheProofPath(outsideBase)
	if err := os.Symlink(outsideProof, originalCacheProofPath(finalPath)); err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "requires network opt-in") {
		t.Fatalf("Resolve error = %v, want rejected proof symlink cache miss", err)
	}
	if helperCalls.Load() != 0 {
		t.Fatalf("helper calls = %d, want 0", helperCalls.Load())
	}
	resolveErr := err
	if _, err := os.Stat(outsideProof); err != nil {
		t.Fatalf("outside proof changed: %v", err)
	}
	for _, path := range []string{finalPath, originalCacheProofPath(finalPath)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("unsafe cache artifact remains at %q: %v", path, err)
		}
	}
	t.Logf("boundary symlinked_proof input=%q outside=%q output=%q helper_calls=%d", originalCacheProofPath(finalPath), outsideProof, resolveErr, helperCalls.Load())
}

func TestDevelopmentCacheRestartCleanupSurfacesRemovalFailures(t *testing.T) {
	t.Run("unchecked final", func(t *testing.T) {
		root, source, inspectVolume := syntheticDevelopmentRoots(t)
		resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		if err != nil {
			t.Fatal(err)
		}
		request := syntheticOriginalRequest("failed-final-removal")
		finalPath := OriginalCachePath(resolver.cache.root, request.SourceLibraryID, request.ModificationDate, request.Query)
		if err := os.WriteFile(finalPath, []byte("unchecked synthetic final"), 0o600); err != nil {
			t.Fatal(err)
		}
		resolver.removeFile = silentNoopRemover(finalPath)
		err = resolver.cleanupAfterRestart()
		if err == nil || !strings.Contains(err.Error(), "remains after cleanup") {
			t.Fatalf("cleanup error = %v, want failed post-removal check", err)
		}
		if _, statErr := os.Lstat(finalPath); statErr != nil {
			t.Fatalf("failed-removal final did not remain for diagnosis: %v", statErr)
		}
		t.Logf("boundary failed_final_cleanup input=%q output=%q remains=true", finalPath, err)
	})

	t.Run("stale proof temporary", func(t *testing.T) {
		root, source, inspectVolume := syntheticDevelopmentRoots(t)
		resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		if err != nil {
			t.Fatal(err)
		}
		request := syntheticOriginalRequest("failed-proof-removal")
		finalPath := OriginalCachePath(resolver.cache.root, request.SourceLibraryID, request.ModificationDate, request.Query)
		if err := os.WriteFile(finalPath, []byte("valid synthetic final"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, digest, err := InspectOriginalFile(finalPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeOriginalCacheProof(finalPath, info.Size(), digest[:]); err != nil {
			t.Fatal(err)
		}
		proofTemporary := developmentProofTemporaryPath(finalPath)
		if err := os.WriteFile(proofTemporary, []byte("stale synthetic proof temporary"), 0o600); err != nil {
			t.Fatal(err)
		}
		resolver.removeFile = failingRemover(proofTemporary)
		err = resolver.cleanupAfterRestart()
		if err == nil || !strings.Contains(err.Error(), "synthetic removal failure") {
			t.Fatalf("cleanup error = %v, want proof removal failure", err)
		}
		if _, statErr := os.Lstat(proofTemporary); statErr != nil {
			t.Fatalf("failed-removal proof temporary did not remain for diagnosis: %v", statErr)
		}
		t.Logf("boundary failed_proof_cleanup input=%q output=%q remains=true", proofTemporary, err)
	})
}

func failingRemover(blockedPath string) func(string) error {
	return func(path string) error {
		if path == blockedPath {
			return errors.New("synthetic removal failure")
		}
		return os.Remove(path)
	}
}

func silentNoopRemover(blockedPath string) func(string) error {
	return func(path string) error {
		if path == blockedPath {
			return nil
		}
		return os.Remove(path)
	}
}
