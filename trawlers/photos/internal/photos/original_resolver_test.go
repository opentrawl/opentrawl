package photos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOriginalResolverUsesExactPackageOriginal(t *testing.T) {
	root := t.TempDir()
	originalPath := filepath.Join(t.TempDir(), "original.heic")
	if err := os.WriteFile(originalPath, []byte("synthetic package original"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewOriginalResolver(root, func(context.Context, OriginalExportQuery, string, bool) error {
		t.Fatal("package original reached PhotoKit")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("package")
	request.PackageCandidates = []LocalMediaCandidate{{Path: originalPath, Class: "original", Size: 26}}
	resolved, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Path != originalPath || resolved.Source != OriginalSourcePackage || resolved.Size != 26 || resolved.SHA256 == "" || resolved.Exported || resolved.Lease != nil {
		t.Fatalf("package resolution = %#v", resolved)
	}
}

func TestOriginalResolverRequiresSourceLibraryIdentity(t *testing.T) {
	resolver, err := NewOriginalResolver(t.TempDir(), func(context.Context, OriginalExportQuery, string, bool) error {
		t.Fatal("export must not run without a source library identity")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolver.Resolve(context.Background(), OriginalRequest{
		Query: OriginalExportQuery{LocalIdentifier: "asset-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "source library ID") {
		t.Fatalf("Resolve error = %v, want missing source library identity", err)
	}
}

func TestOriginalResolverRejectsMissingCameraResourceMetadataBeforeExport(t *testing.T) {
	exports := 0
	resolver, err := NewOriginalResolver(t.TempDir(), func(context.Context, OriginalExportQuery, string, bool) error {
		exports++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolver.Resolve(context.Background(), OriginalRequest{
		SourceLibraryID: "source-library-1",
		Query:           OriginalExportQuery{LocalIdentifier: "asset-1"},
	})
	if err == nil || !strings.Contains(err.Error(), "camera original resource metadata") {
		t.Fatalf("Resolve error = %v, want missing camera original resource metadata", err)
	}
	if exports != 0 {
		t.Fatalf("exports = %d, want 0", exports)
	}
}

func TestOriginalResolverFallsThroughAmbiguousPackageMatch(t *testing.T) {
	first := filepath.Join(t.TempDir(), "first.heic")
	second := filepath.Join(t.TempDir(), "second.heic")
	for _, path := range []string{first, second} {
		if err := os.WriteFile(path, []byte("synthetic package candidate"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var exports int
	resolver, err := NewOriginalResolver(t.TempDir(), func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		exports++
		return os.WriteFile(destination, []byte("synthetic PhotoKit original"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("ambiguous")
	request.PackageCandidates = []LocalMediaCandidate{
		{Path: first, Class: "original", Size: 27},
		{Path: second, Class: "original", Size: 27},
	}
	resolved, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Lease.Close()
	if exports != 1 || resolved.Source != OriginalSourcePhotoKit {
		t.Fatalf("exports = %d resolution = %#v", exports, resolved)
	}
}

func TestOriginalResolverReusesHashProvedCacheAcrossRestart(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("restart")
	exports := 0
	firstResolver, err := NewOriginalResolver(root, func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		exports++
		return os.WriteFile(destination, []byte("exact synthetic original"), 0o666)
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := firstResolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Lease.Close()

	secondResolver, err := NewOriginalResolver(root, func(context.Context, OriginalExportQuery, string, bool) error {
		t.Fatal("valid restart cache reached PhotoKit")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := secondResolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Lease.Close()
	if exports != 1 || second.Source != OriginalSourceCache || second.Path != first.Path || second.Size != first.Size || second.SHA256 != first.SHA256 {
		t.Fatalf("exports = %d first = %#v second = %#v", exports, first, second)
	}
	if mode := fileMode(t, second.Path); mode != 0o600 {
		t.Fatalf("cache media mode = %o, want 600", mode)
	}
	if mode := fileMode(t, originalCacheProofPath(second.Path)); mode != 0o600 {
		t.Fatalf("cache proof mode = %o, want 600", mode)
	}
	if mode := fileMode(t, root); mode != 0o700 {
		t.Fatalf("cache directory mode = %o, want 700", mode)
	}
}

func TestOriginalResolverRejectsCacheWhoseBytesDoNotMatchProof(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("changed")
	exports := 0
	exporter := func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		exports++
		return os.WriteFile(destination, []byte("replacement synthetic original"), 0o600)
	}
	resolver, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Lease.Close()
	if err := os.WriteFile(first.Path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Lease.Close()
	if exports != 2 || second.Source != OriginalSourcePhotoKit || second.SHA256 != first.SHA256 {
		t.Fatalf("exports = %d first = %#v second = %#v", exports, first, second)
	}
}

func TestOriginalResolverRejectsCacheWithoutProof(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("missing-proof")
	exports := 0
	exporter := func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		exports++
		return os.WriteFile(destination, []byte("synthetic original with proof"), 0o600)
	}
	resolver, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Lease.Close()
	if err := os.Remove(originalCacheProofPath(first.Path)); err != nil {
		t.Fatal(err)
	}

	restarted, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Lease.Close()
	if exports != 2 || second.Source != OriginalSourcePhotoKit {
		t.Fatalf("exports = %d resolution = %#v", exports, second)
	}
}

func TestOriginalResolverInstallsCompleteExportAtomically(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("atomic")
	finalPath := OriginalCachePath(root, request.SourceLibraryID, request.ModificationDate, request.Query)
	resolver, err := NewOriginalResolver(root, func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		if destination == finalPath || destination != originalCacheTemporaryPath(finalPath) {
			t.Fatalf("export destination = %q, final = %q", destination, finalPath)
		}
		if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
			t.Fatalf("final cache entry visible during export: %v", err)
		}
		return os.WriteFile(destination, []byte("complete atomic original"), 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Lease.Close()
	if resolved.Path != finalPath {
		t.Fatalf("resolved path = %q, want %q", resolved.Path, finalPath)
	}
	if _, err := os.Stat(originalCacheTemporaryPath(finalPath)); !os.IsNotExist(err) {
		t.Fatalf("temporary export remains: %v", err)
	}
	if _, err := os.Stat(originalCacheProofPath(finalPath)); err != nil {
		t.Fatalf("cache proof missing: %v", err)
	}
}

func TestOriginalResolverRemovesPartialExportAfterFailure(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("partial")
	finalPath := OriginalCachePath(root, request.SourceLibraryID, request.ModificationDate, request.Query)
	resolver, err := NewOriginalResolver(root, func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		if err := os.WriteFile(destination, []byte("partial"), 0o600); err != nil {
			return err
		}
		return errors.New("synthetic export failure")
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), request); err == nil {
		t.Fatal("partial export succeeded")
	}
	for _, path := range []string{finalPath, originalCacheTemporaryPath(finalPath), originalCacheProofPath(finalPath)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("partial cache artifact remains at %q: %v", filepath.Base(path), err)
		}
	}
}

func TestOriginalResolverRejectsEmptyExport(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("empty")
	finalPath := OriginalCachePath(root, request.SourceLibraryID, request.ModificationDate, request.Query)
	resolver, err := NewOriginalResolver(root, func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		return os.WriteFile(destination, nil, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.Resolve(context.Background(), request); err == nil {
		t.Fatal("empty export succeeded")
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("empty final cache artifact remains: %v", err)
	}
}

func TestOriginalResolverCoalescesSameKeyAcrossInstances(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("coalesced")
	started := make(chan struct{})
	release := make(chan struct{})
	var exportCalls atomic.Int32
	exporter := func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		if exportCalls.Add(1) == 1 {
			close(started)
		}
		<-release
		return os.WriteFile(destination, []byte("one coalesced original"), 0o600)
	}
	firstResolver, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	secondResolver, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		resolution OriginalResolution
		err        error
	}
	results := make(chan result, 2)
	for _, resolver := range []*OriginalResolver{firstResolver, secondResolver} {
		go func(resolver *OriginalResolver) {
			resolved, err := resolver.Resolve(context.Background(), request)
			results <- result{resolution: resolved, err: err}
		}(resolver)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("PhotoKit export did not start")
	}
	close(release)
	sources := map[string]int{}
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		sources[got.resolution.Source]++
		got.resolution.Lease.Close()
	}
	if exportCalls.Load() != 1 || sources[OriginalSourcePhotoKit] != 1 || sources[OriginalSourceCache] != 1 {
		t.Fatalf("export calls = %d sources = %#v", exportCalls.Load(), sources)
	}
}

func TestOriginalResolverWaitForSameKeyHonoursContext(t *testing.T) {
	root := t.TempDir()
	request := syntheticOriginalRequest("cancelled")
	started := make(chan struct{})
	release := make(chan struct{})
	exporter := func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		close(started)
		<-release
		return os.WriteFile(destination, []byte("eventual synthetic original"), 0o600)
	}
	firstResolver, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	secondResolver, err := NewOriginalResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		resolved, err := firstResolver.Resolve(context.Background(), request)
		if resolved.Lease != nil {
			resolved.Lease.Close()
		}
		firstDone <- err
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = secondResolver.Resolve(ctx, request)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second resolve error = %v, want context deadline", err)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestOriginalResolverDoesNotEvictLeasedFile(t *testing.T) {
	root := t.TempDir()
	exporter := func(_ context.Context, query OriginalExportQuery, destination string, _ bool) error {
		return os.WriteFile(destination, []byte("original bytes "+query.LocalIdentifier), 0o600)
	}
	resolver, err := newOriginalResolver(root, 40, exporter)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(context.Background(), syntheticOriginalRequest("first"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(first.Path, time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	second, err := resolver.Resolve(context.Background(), syntheticOriginalRequest("second"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("leased first original was evicted: %v", err)
	}
	first.Lease.Close()
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("released least-recently-used original remains: %v", err)
	}
	if _, err := os.Stat(second.Path); err != nil {
		t.Fatalf("active second original was evicted: %v", err)
	}
	second.Lease.Close()
}

func TestOriginalResolverRemovesUnlockedStaleTemporaryFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".stale.heic.incoming", ".stale.heic.incoming.exporting"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("stale partial original"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewOriginalResolver(root, func(context.Context, OriginalExportQuery, string, bool) error {
		return errors.New("unexpected export")
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".stale.heic.incoming", ".stale.heic.incoming.exporting"} {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("stale temporary %q remains: %v", name, err)
		}
	}
}

func TestOriginalResolverPruneDoesNotOverEvictFromStaleCrossInstanceSnapshot(t *testing.T) {
	root := t.TempDir()
	resolver, err := newOriginalResolver(root, 10, func(context.Context, OriginalExportQuery, string, bool) error {
		return errors.New("unexpected export")
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPath := filepath.Join(root, "first.heic")
	secondPath := filepath.Join(root, "second.heic")
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("1234567890"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := []originalCacheEntry{
		{path: firstPath, size: 10},
		{path: secondPath, size: 10},
	}
	if err := resolver.pruneSnapshot(snapshot, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("first resolver did not evict oldest file: %v", err)
	}
	if err := resolver.pruneSnapshot(snapshot, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("stale second snapshot over-evicted remaining file: %v", err)
	}
}

func syntheticOriginalRequest(id string) OriginalRequest {
	return OriginalRequest{
		SourceLibraryID:  "synthetic-library",
		ModificationDate: "2026-07-10T12:00:00Z",
		AllowNetwork:     true,
		Query: OriginalExportQuery{
			LocalIdentifier:  id,
			OriginalFilename: id + ".jpeg",
			OriginalUTI:      "public.jpeg",
		},
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
