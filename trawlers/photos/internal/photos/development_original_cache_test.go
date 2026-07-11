package photos

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestDevelopmentCacheRejectsMissingRootWithoutCreatingIt(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "unmounted-external-cache")
	source := filepath.Join(parent, "Fixture Photos Library.photoslibrary")
	if err := os.Mkdir(source, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := newDevelopmentOriginalResolver(root, source, unexpectedVolumeInspector(t), unexpectedOriginalExporter(t))
	t.Logf("boundary missing_root input=%q output=%q", root, err)
	if err == nil || !strings.Contains(err.Error(), "already be mounted and created") {
		t.Fatalf("constructor error = %v, want missing mounted root", err)
	}
	if _, statErr := os.Lstat(root); !os.IsNotExist(statErr) {
		t.Fatalf("missing cache root was created: %v", statErr)
	}
}

func TestDevelopmentCacheRejectsInternalUnsafeSymlinkAndOverlappingRoots(t *testing.T) {
	t.Run("secondary internal volume", func(t *testing.T) {
		root, source, inspectVolume := syntheticDevelopmentRoots(t)
		inspectVolume = volumeInspectorFor(root, developmentVolume{Mounted: true, Local: true, External: false, Physical: true, Writable: true})
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary internal_root input=%q output=%q", root, err)
		if err == nil || !strings.Contains(err.Error(), "external physical volume") {
			t.Fatalf("constructor error = %v, want secondary internal volume rejection", err)
		}
	})

	t.Run("network mount", func(t *testing.T) {
		root, source, _ := syntheticDevelopmentRoots(t)
		inspectVolume := volumeInspectorFor(root, developmentVolume{Mounted: true, Local: false, External: true, Physical: false, Writable: true})
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary network_root input=%q output=%q", root, err)
		if err == nil || !strings.Contains(err.Error(), "local external physical volume") {
			t.Fatalf("constructor error = %v, want network volume rejection", err)
		}
	})

	t.Run("disk image", func(t *testing.T) {
		root, source, _ := syntheticDevelopmentRoots(t)
		inspectVolume := volumeInspectorFor(root, developmentVolume{Mounted: true, Local: true, External: true, Physical: false, Writable: true})
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary disk_image_root input=%q output=%q", root, err)
		if err == nil || !strings.Contains(err.Error(), "external physical volume") {
			t.Fatalf("constructor error = %v, want disk image rejection", err)
		}
	})

	t.Run("unsafe permissions", func(t *testing.T) {
		root, source, inspectVolume := syntheticDevelopmentRoots(t)
		if err := os.Chmod(root, 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary root_permissions input=%q mode=0755 output=%q", root, err)
		if err == nil || !strings.Contains(err.Error(), "want 0700") {
			t.Fatalf("constructor error = %v, want private-permission rejection", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		root, source, inspectVolume := syntheticDevelopmentRoots(t)
		link := filepath.Join(filepath.Dir(root), "cache-link")
		if err := os.Symlink(root, link); err != nil {
			t.Fatal(err)
		}
		_, err := newDevelopmentOriginalResolver(link, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary symlink_root input=%q output=%q", link, err)
		if err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
			t.Fatalf("constructor error = %v, want symlink rejection", err)
		}
	})

	t.Run("source overlap", func(t *testing.T) {
		source := t.TempDir()
		root := filepath.Join(source, "external-cache")
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
		inspectVolume := volumeInspectorFor(root, developmentVolume{Mounted: true, Local: true, External: true, Physical: true, Writable: true})
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary overlapping_root input=%q source=%q output=%q", root, source, err)
		if err == nil || !strings.Contains(err.Error(), "overlaps") {
			t.Fatalf("constructor error = %v, want overlap rejection", err)
		}
	})

	t.Run("locks symlink", func(t *testing.T) {
		root, source, inspectVolume := syntheticDevelopmentRoots(t)
		elsewhere := t.TempDir()
		if err := os.Symlink(elsewhere, filepath.Join(root, ".locks")); err != nil {
			t.Fatal(err)
		}
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
		t.Logf("boundary symlink_locks input=%q output=%q", filepath.Join(root, ".locks"), err)
		if err == nil || !strings.Contains(err.Error(), "not a symlink") {
			t.Fatalf("constructor error = %v, want locks symlink rejection", err)
		}
	})
}

func TestDevelopmentCacheCopiesPackageOriginalAndReusesCheckedBytesAfterRestart(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	packagePath := filepath.Join(source, "synthetic-package-original.heic")
	packageBytes := []byte("exact synthetic package original bytes")
	if err := os.WriteFile(packagePath, packageBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("development-package")
	request.AllowNetwork = false
	request.PackageCandidates = []LocalMediaCandidate{{Path: packagePath, Class: "original", Size: int64(len(packageBytes))}}

	var helperCalls atomic.Int32
	exporter := func(context.Context, OriginalExportQuery, string, bool) error {
		helperCalls.Add(1)
		return errors.New("synthetic helper must not run")
	}
	firstResolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary cache_argument input=%q output=%q", root, firstResolver.cache.root)
	t.Logf("boundary resolver_request input=%#v", request)
	first, err := firstResolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Lease.Close()
	installedBytes, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	proofBytes, err := os.ReadFile(originalCacheProofPath(first.Path))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary package_copy input=%q output=%q", packageBytes, installedBytes)
	t.Logf("boundary checked_install input=%q output=%q", first.Path, proofBytes)
	if first.Path == packagePath || !strings.HasPrefix(first.Path, firstResolver.cache.root+string(filepath.Separator)) {
		t.Fatalf("package original was not copied into the development cache: %#v", first)
	}
	if first.Source != OriginalSourcePackage || first.Exported || string(installedBytes) != string(packageBytes) {
		t.Fatalf("first resolution = %#v bytes = %q", first, installedBytes)
	}
	if helperCalls.Load() != 0 {
		t.Fatalf("helper calls = %d, want 0", helperCalls.Load())
	}

	if err := os.Remove(packagePath); err != nil {
		t.Fatal(err)
	}
	restarted, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Lease.Close()
	t.Logf("boundary restart_cache_hit input=%#v output=%#v helper_calls=%d", request, second, helperCalls.Load())
	if second.Source != OriginalSourceDevelopmentCache || second.Path != first.Path || second.Size != first.Size || second.SHA256 != first.SHA256 {
		t.Fatalf("first = %#v second = %#v", first, second)
	}
	if helperCalls.Load() != 0 {
		t.Fatalf("restart helper calls = %d, want 0", helperCalls.Load())
	}
}

func TestDevelopmentCacheMissRequiresNetworkOptInBeforeHelper(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	var helperCalls atomic.Int32
	resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, func(context.Context, OriginalExportQuery, string, bool) error {
		helperCalls.Add(1)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("network-disabled")
	request.AllowNetwork = false
	_, err = resolver.Resolve(context.Background(), request)
	if err == nil || !strings.Contains(err.Error(), "requires network opt-in") {
		t.Fatalf("Resolve error = %v, want network opt-in stop", err)
	}
	t.Logf("boundary network_policy input=%#v output=%q helper_calls=%d", request, err, helperCalls.Load())
	if helperCalls.Load() != 0 {
		t.Fatalf("helper calls = %d, want 0", helperCalls.Load())
	}
}

func TestDevelopmentCacheNetworkExportUsesExistingExporterAndRestartsFromCache(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	request := syntheticOriginalRequest("network-enabled")
	var helperCalls atomic.Int32
	exporter := func(_ context.Context, _ OriginalExportQuery, destination string, allowNetwork bool) error {
		helperCalls.Add(1)
		if !allowNetwork {
			return errors.New("synthetic network policy was lost")
		}
		return os.WriteFile(destination, []byte("synthetic authorised helper output"), 0o600)
	}
	resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first.Lease.Close()
	if first.Source != OriginalSourcePhotoKit || !first.Exported || helperCalls.Load() != 1 {
		t.Fatalf("first resolution = %#v helper calls = %d", first, helperCalls.Load())
	}
	firstBytes, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary helper_result input_allow_network=%t output=%q resolution=%#v", request.AllowNetwork, firstBytes, first)

	request.AllowNetwork = false
	restarted, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
	if err != nil {
		t.Fatal(err)
	}
	second, err := restarted.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Lease.Close()
	if second.Source != OriginalSourceDevelopmentCache || second.SHA256 != first.SHA256 || helperCalls.Load() != 1 {
		t.Fatalf("second resolution = %#v helper calls = %d", second, helperCalls.Load())
	}
	t.Logf("boundary helper_restart input_allow_network=%t output=%#v helper_calls=%d", request.AllowNetwork, second, helperCalls.Load())
}

func TestDevelopmentCacheInterruptedPackageCopyLeavesNoReusableEntry(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	packagePath := filepath.Join(source, "interrupted.heic")
	if err := os.WriteFile(packagePath, []byte("synthetic interrupted package original"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("interrupted-package")
	request.AllowNetwork = false
	request.PackageCandidates = []LocalMediaCandidate{{Path: packagePath, Class: "original"}}
	resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = resolver.Resolve(ctx, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Resolve error = %v, want context cancellation", err)
	}
	finalPath := OriginalCachePath(root, request.SourceLibraryID, request.ModificationDate, request.Query)
	for _, path := range []string{finalPath, originalCacheTemporaryPath(finalPath), originalCacheProofPath(finalPath)} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("interrupted cache artifact remains at %q: %v", path, statErr)
		}
	}
	t.Logf("boundary interrupted_copy input=%#v output=%q final_exists=false", request, err)

	restarted, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := restarted.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer resolved.Lease.Close()
	if resolved.Source != OriginalSourcePackage {
		t.Fatalf("restart resolution = %#v", resolved)
	}
}

func TestDevelopmentCacheRestartRemovesUncheckedFinalUnderEntryLock(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("crash-after-media-rename")
	finalPath := OriginalCachePath(canonicalRoot, request.SourceLibraryID, request.ModificationDate, request.Query)
	if err := os.WriteFile(finalPath, []byte("unchecked final after synthetic crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureDevelopmentCacheLocks(canonicalRoot); err != nil {
		t.Fatal(err)
	}
	heldLock, err := (&OriginalResolver{root: canonicalRoot}).lock(context.Background(), finalPath, syscall.LOCK_EX)
	if err != nil {
		t.Fatal(err)
	}
	cleanupDone := make(chan error, 1)
	exporter := unexpectedOriginalExporter(t)
	go func() {
		_, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
		cleanupDone <- err
	}()
	select {
	case err := <-cleanupDone:
		t.Fatalf("restart cleanup ignored the held entry lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("unchecked final changed while entry lock was held: %v", err)
	}
	heldLock.Close()
	if err := <-cleanupDone; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("unchecked final survived restart cleanup: %v", err)
	}
	t.Logf("boundary restart_unchecked_final input=%q output_exists=false", finalPath)
}

func TestDevelopmentCacheRestartRemovesStaleProofTemporaryUnderEntryLock(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	request := syntheticOriginalRequest("crash-during-proof-write")
	finalPath := OriginalCachePath(canonicalRoot, request.SourceLibraryID, request.ModificationDate, request.Query)
	proofTemporary := developmentProofTemporaryPath(finalPath)
	if err := os.WriteFile(finalPath, []byte("unchecked final before synthetic proof crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proofTemporary, []byte("partial synthetic proof"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t)); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{finalPath, proofTemporary} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("proof crash artifact survived restart cleanup at %q: %v", path, err)
		}
	}
	t.Logf("boundary restart_stale_proof input_final=%q input_temporary=%q output_exists=false", finalPath, proofTemporary)
}

func TestDevelopmentCacheDoesNotEvictCheckedOriginals(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	resolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, unexpectedOriginalExporter(t))
	if err != nil {
		t.Fatal(err)
	}
	paths := make([]string, 0, 2)
	for _, id := range []string{"retained-first", "retained-second"} {
		packagePath := filepath.Join(source, id+".heic")
		if err := os.WriteFile(packagePath, []byte("synthetic retained original "+id), 0o600); err != nil {
			t.Fatal(err)
		}
		request := syntheticOriginalRequest(id)
		request.AllowNetwork = false
		request.PackageCandidates = []LocalMediaCandidate{{Path: packagePath, Class: "original"}}
		resolved, err := resolver.Resolve(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, resolved.Path)
		resolved.Lease.Close()
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("checked development original was evicted: %v", err)
		}
	}
}

func TestDevelopmentCacheCoalescesSameKeyThroughExistingLock(t *testing.T) {
	root, source, inspectVolume := syntheticDevelopmentRoots(t)
	request := syntheticOriginalRequest("development-coalesced")
	started := make(chan struct{})
	release := make(chan struct{})
	var helperCalls atomic.Int32
	exporter := func(_ context.Context, _ OriginalExportQuery, destination string, _ bool) error {
		if helperCalls.Add(1) == 1 {
			close(started)
		}
		<-release
		return os.WriteFile(destination, []byte("one synthetic coalesced original"), 0o600)
	}
	firstResolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
	if err != nil {
		t.Fatal(err)
	}
	secondResolver, err := newDevelopmentOriginalResolver(root, source, inspectVolume, exporter)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		resolution OriginalResolution
		err        error
	}
	results := make(chan result, 2)
	for _, resolver := range []*DevelopmentOriginalResolver{firstResolver, secondResolver} {
		go func(resolver *DevelopmentOriginalResolver) {
			resolution, err := resolver.Resolve(context.Background(), request)
			results <- result{resolution: resolution, err: err}
		}(resolver)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("synthetic helper did not start")
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
	t.Logf("boundary same_key input=%#v output_sources=%#v helper_calls=%d", request, sources, helperCalls.Load())
	if helperCalls.Load() != 1 || sources[OriginalSourcePhotoKit] != 1 || sources[OriginalSourceDevelopmentCache] != 1 {
		t.Fatalf("helper calls = %d sources = %#v", helperCalls.Load(), sources)
	}
}

func syntheticDevelopmentRoots(t *testing.T) (root string, source string, inspectVolume developmentVolumeInspector) {
	t.Helper()
	parent := t.TempDir()
	root = filepath.Join(parent, "external-cache")
	source = filepath.Join(parent, "Fixture Photos Library.photoslibrary")
	for _, path := range []string{root, source} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return root, source, volumeInspectorFor(parent, developmentVolume{
		Mounted:  true,
		Local:    true,
		External: true,
		Physical: true,
		Writable: true,
	})
}

func volumeInspectorFor(mountPoint string, volume developmentVolume) developmentVolumeInspector {
	volume.MountPoint = mountPoint
	return func(string) (developmentVolume, error) {
		return volume, nil
	}
}

func unexpectedOriginalExporter(t *testing.T) OriginalExporter {
	t.Helper()
	return func(context.Context, OriginalExportQuery, string, bool) error {
		t.Fatal("unexpected original exporter call")
		return nil
	}
}

func unexpectedVolumeInspector(t *testing.T) developmentVolumeInspector {
	t.Helper()
	return func(string) (developmentVolume, error) {
		t.Fatal("unexpected volume inspector call")
		return developmentVolume{}, nil
	}
}
