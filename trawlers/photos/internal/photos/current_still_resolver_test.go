package photos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentStillResolverUsesRoleSpecificFractionalVersionAndReopens(t *testing.T) {
	root := t.TempDir()
	calls := 0
	exporter := func(_ context.Context, request CurrentStillRequest, destination string) (CurrentStillFact, error) {
		calls++
		modification, ok := request.Freshness.ExpectedModification()
		if !ok {
			t.Fatal("expected modification freshness")
		}
		bytes := []byte(fmt.Sprintf("synthetic current still %d.%06d", modification.UnixSeconds, modification.Microseconds))
		if err := os.WriteFile(destination, bytes, 0o600); err != nil {
			return CurrentStillFact{}, err
		}
		fact := currentStillFactFromBytes("public.heic", 1, 4032, 3024, bytes)
		fact.Timings = CurrentStillPhaseTimings{HelperVerificationMicros: 11, LaunchServicesStartMicros: 12, PhotoKitCallbackMicros: 13, ValidationHashMicros: 14}
		fact.PhotoKitCalls = 1
		return fact, nil
	}
	resolver, err := NewCurrentStillResolver(root, exporter)
	if err != nil {
		t.Fatal(err)
	}
	firstModification := mustParseCurrentStillModification(t, "2026-07-11T12:00:00.100000305Z")
	firstRequest := CurrentStillRequest{SourceLibraryID: "synthetic-library", AssetUUID: "A1B2", Freshness: mustCurrentStillFreshness(t, firstModification)}
	t.Logf("boundary=synthetic_current_still_cache_request raw_input=%#v", firstRequest)
	first, err := resolver.Resolve(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if first.Source != CurrentStillSourcePhotoKit || !first.Exported {
		t.Fatalf("first = %#v", first)
	}
	if first.PhotoKitCalls != 1 || first.Timings.QueueWaitMicros <= 0 || first.Timings.HelperVerificationMicros != 11 || first.Timings.LaunchServicesStartMicros != 12 || first.Timings.PhotoKitCallbackMicros != 13 || first.Timings.ValidationHashMicros < 14 || first.Timings.CacheInstallationMicros <= 0 || first.Timings.TotalMicros <= 0 {
		t.Fatalf("first observation = %#v", first)
	}
	if first.Fact.Timings != (CurrentStillPhaseTimings{}) {
		t.Fatalf("media fact retained observation timings: %#v", first.Fact)
	}
	if first.Lease != nil {
		first.Lease.Close()
	}
	media, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := os.ReadFile(originalCacheProofPath(first.Path))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=synthetic_current_still_cache_install raw_media=%q raw_proof=%s", media, proof)
	if got, want := filepath.Base(first.Path), filepath.Base(CurrentStillCachePath(root, firstRequest.SourceLibraryID, firstRequest.AssetUUID, firstRequest.Freshness)); got != want {
		t.Fatalf("cache key = %q, want %q", got, want)
	}
	restarted, err := NewCurrentStillResolver(root, func(context.Context, CurrentStillRequest, string) (CurrentStillFact, error) {
		t.Fatal("restart cache reached PhotoKit")
		return CurrentStillFact{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	hit, err := restarted.Resolve(context.Background(), firstRequest)
	if err != nil {
		t.Fatal(err)
	}
	if hit.Source != CurrentStillSourceCache || hit.Fact != first.Fact {
		t.Fatalf("hit = %#v", hit)
	}
	if hit.PhotoKitCalls != 0 || hit.Timings.QueueWaitMicros <= 0 || hit.Timings.HelperVerificationMicros != 0 || hit.Timings.PhotoKitCallbackMicros != 0 || hit.Timings.ValidationHashMicros <= 0 || hit.Timings.CacheInstallationMicros != 0 || hit.Timings.TotalMicros <= 0 {
		t.Fatalf("cache observation = %#v", hit)
	}
	if hit.Lease != nil {
		hit.Lease.Close()
	}
	secondRequest := CurrentStillRequest{SourceLibraryID: firstRequest.SourceLibraryID, AssetUUID: firstRequest.AssetUUID, Freshness: mustCurrentStillFreshness(t, CurrentStillModification{UnixSeconds: firstModification.UnixSeconds, Microseconds: firstModification.Microseconds + 1})}
	if CurrentStillCachePath(root, firstRequest.SourceLibraryID, firstRequest.AssetUUID, firstRequest.Freshness) == CurrentStillCachePath(root, secondRequest.SourceLibraryID, secondRequest.AssetUUID, secondRequest.Freshness) {
		t.Fatal("microsecond modification instant reused a current-still key")
	}
	if _, err := resolver.Resolve(context.Background(), secondRequest); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("PhotoKit calls = %d, want 2", calls)
	}
}

func TestCurrentStillModificationCacheKeyKeepsVersionTwoLayout(t *testing.T) {
	freshness := mustCurrentStillFreshness(t, CurrentStillModification{UnixSeconds: 1783771200, Microseconds: 123000})
	got := filepath.Base(CurrentStillCachePath(t.TempDir(), "synthetic-library", "A1B2", freshness))
	const want = "05ebd59bb3a3fdbf6be1ae3f7dc715b302bca19b649f2c6e5a2e7641c4255784.current"
	if got != want {
		t.Fatalf("modification cache key = %q, want unchanged v2 key %q", got, want)
	}
}

func TestCurrentStillResolverPreservesFailedPhaseTimings(t *testing.T) {
	resolver, err := NewCurrentStillResolver(t.TempDir(), func(context.Context, CurrentStillRequest, string) (CurrentStillFact, error) {
		failure := measuredCurrentStillError(errors.New("synthetic export failure"), CurrentStillPhaseTimings{HelperVerificationMicros: 21, LaunchServicesStartMicros: 22, PhotoKitCallbackMicros: 23})
		failure.PhotoKitCalls = 1
		return CurrentStillFact{}, failure
	})
	if err != nil {
		t.Fatal(err)
	}
	request := testCurrentStillRequest(t)
	_, err = resolver.Resolve(context.Background(), request)
	var measured *CurrentStillMeasuredError
	if !errors.As(err, &measured) {
		t.Fatalf("error = %v", err)
	}
	if measured.PhotoKitCalls != 1 || measured.Timings.QueueWaitMicros <= 0 || measured.Timings.HelperVerificationMicros != 21 || measured.Timings.LaunchServicesStartMicros != 22 || measured.Timings.PhotoKitCallbackMicros != 23 || measured.Timings.TotalMicros <= 0 {
		t.Fatalf("failed observation = %#v", measured.Timings)
	}
}

func TestCurrentStillResolverKeepsPreCallFailureAtZeroCalls(t *testing.T) {
	resolver, err := NewCurrentStillResolver(t.TempDir(), func(context.Context, CurrentStillRequest, string) (CurrentStillFact, error) {
		return CurrentStillFact{}, measuredCurrentStillError(errors.New("synthetic verification failure"), CurrentStillPhaseTimings{HelperVerificationMicros: 5})
	})
	if err != nil {
		t.Fatal(err)
	}
	request := testCurrentStillRequest(t)
	_, err = resolver.Resolve(context.Background(), request)
	assertMeasuredCurrentStillFailure(t, err, 0, func(timings CurrentStillPhaseTimings) bool {
		return timings.HelperVerificationMicros == 5 && timings.TotalMicros > 0
	})
}

func TestCurrentStillResolverMeasuresValidationFailure(t *testing.T) {
	resolver, err := NewCurrentStillResolver(t.TempDir(), func(_ context.Context, _ CurrentStillRequest, destination string) (CurrentStillFact, error) {
		if err := os.WriteFile(destination, []byte("synthetic bytes"), 0o600); err != nil {
			return CurrentStillFact{}, err
		}
		return CurrentStillFact{MediaType: "public.heic", PixelWidth: 1, PixelHeight: 1, Size: 99, SHA256: "00", Timings: CurrentStillPhaseTimings{PhotoKitCallbackMicros: 7}, PhotoKitCalls: 1}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request := testCurrentStillRequest(t)
	_, err = resolver.Resolve(context.Background(), request)
	assertMeasuredCurrentStillFailure(t, err, 1, func(timings CurrentStillPhaseTimings) bool {
		return timings.PhotoKitCallbackMicros == 7 && timings.ValidationHashMicros > 0 && timings.TotalMicros > 0
	})
}

func TestCurrentStillResolverMeasuresCacheInstallFailure(t *testing.T) {
	root := t.TempDir()
	request := testCurrentStillRequest(t)
	finalPath := CurrentStillCachePath(root, request.SourceLibraryID, request.AssetUUID, request.Freshness)
	resolver, err := NewCurrentStillResolver(root, func(_ context.Context, _ CurrentStillRequest, destination string) (CurrentStillFact, error) {
		data := []byte("synthetic bytes")
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return CurrentStillFact{}, err
		}
		if err := os.Mkdir(finalPath, 0o700); err != nil {
			return CurrentStillFact{}, err
		}
		fact := currentStillFactFromBytes("public.heic", 1, 1, 1, data)
		fact.PhotoKitCalls = 1
		return fact, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(context.Background(), request)
	assertMeasuredCurrentStillFailure(t, err, 1, func(timings CurrentStillPhaseTimings) bool {
		return timings.ValidationHashMicros > 0 && timings.CacheInstallationMicros > 0 && timings.TotalMicros > 0
	})
}

func assertMeasuredCurrentStillFailure(t *testing.T, err error, calls int, valid func(CurrentStillPhaseTimings) bool) {
	t.Helper()
	var measured *CurrentStillMeasuredError
	if !errors.As(err, &measured) {
		t.Fatalf("error = %v", err)
	}
	if measured.PhotoKitCalls != calls || !valid(measured.Timings) {
		t.Fatalf("measured failure = %#v", measured)
	}
}

func TestCurrentStillResolverRejectsMismatchedProofAndRemovesEntry(t *testing.T) {
	root := t.TempDir()
	request := testCurrentStillRequest(t)
	path := CurrentStillCachePath(root, request.SourceLibraryID, request.AssetUUID, request.Freshness)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("stale bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(originalCacheProofPath(path), []byte(`{"version":1,"role":"current_still","media_type":"public.heic","orientation":1,"pixel_width":1,"pixel_height":1,"size":99,"sha256":"00"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := NewCurrentStillResolver(root, func(_ context.Context, _ CurrentStillRequest, destination string) (CurrentStillFact, error) {
		bytes := []byte("replacement synthetic bytes")
		if err := os.WriteFile(destination, bytes, 0o600); err != nil {
			return CurrentStillFact{}, err
		}
		return currentStillFactFromBytes("public.heic", 1, 1, 1, bytes), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := resolver.Resolve(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.Source != CurrentStillSourcePhotoKit {
		t.Fatalf("source = %q", resolution.Source)
	}
	if resolution.Lease != nil {
		resolution.Lease.Close()
	}
}

func TestParseCurrentStillModificationRoundsFloatDerivedNanoseconds(t *testing.T) {
	modification, err := ParseCurrentStillModification("2023-03-24T05:18:11.797890305Z")
	if err != nil {
		t.Fatal(err)
	}
	if modification.Microseconds != 797890 {
		t.Fatalf("modification = %#v", modification)
	}
	carried, err := ParseCurrentStillModification("2026-07-11T12:00:00.999999600Z")
	if err != nil {
		t.Fatal(err)
	}
	base := mustParseCurrentStillModification(t, "2026-07-11T12:00:00Z")
	if carried.UnixSeconds != base.UnixSeconds+1 || carried.Microseconds != 0 {
		t.Fatalf("carried modification = %#v", carried)
	}
}

func TestCurrentStillResolverUsesSourceFingerprintForCacheFreshness(t *testing.T) {
	root := t.TempDir()
	calls := 0
	resolver, err := NewCurrentStillResolver(root, func(_ context.Context, request CurrentStillRequest, destination string) (CurrentStillFact, error) {
		calls++
		fingerprint, ok := request.Freshness.SourceFingerprint()
		if !ok {
			t.Fatal("export request did not preserve source-fingerprint freshness")
		}
		data := []byte("synthetic current still " + fingerprint)
		if err := os.WriteFile(destination, data, 0o600); err != nil {
			return CurrentStillFact{}, err
		}
		return currentStillFactFromBytes("public.heic", 1, 4032, 3024, data), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	first := CurrentStillRequest{SourceLibraryID: "synthetic-library", AssetUUID: "asset", Freshness: mustCurrentStillSourceFingerprint(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}
	firstResolution, err := resolver.Resolve(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if firstResolution.Lease != nil {
		firstResolution.Lease.Close()
	}
	restarted, err := NewCurrentStillResolver(root, func(context.Context, CurrentStillRequest, string) (CurrentStillFact, error) {
		t.Fatal("same source fingerprint missed after restart")
		return CurrentStillFact{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if hit, err := restarted.Resolve(context.Background(), first); err != nil || hit.Source != CurrentStillSourceCache {
		t.Fatalf("restart = %#v, %v", hit, err)
	} else if hit.Lease != nil {
		hit.Lease.Close()
	}
	changed := first
	changed.Freshness = mustCurrentStillSourceFingerprint(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if CurrentStillCachePath(root, first.SourceLibraryID, first.AssetUUID, first.Freshness) == CurrentStillCachePath(root, changed.SourceLibraryID, changed.AssetUUID, changed.Freshness) {
		t.Fatal("changed source fingerprint reused a current-still cache key")
	}
	changedResolution, err := resolver.Resolve(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedResolution.Lease != nil {
		changedResolution.Lease.Close()
	}
	if calls != 2 {
		t.Fatalf("exports = %d, want 2", calls)
	}
}

func TestCurrentStillResolverRejectsMissingOrMalformedFreshness(t *testing.T) {
	resolver, err := NewCurrentStillResolver(t.TempDir(), func(context.Context, CurrentStillRequest, string) (CurrentStillFact, error) {
		t.Fatal("invalid freshness reached exporter")
		return CurrentStillFact{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, freshness := range []CurrentStillFreshness{{}, {sourceFingerprint: "ABC"}} {
		if _, err := resolver.Resolve(context.Background(), CurrentStillRequest{SourceLibraryID: "synthetic-library", AssetUUID: "asset", Freshness: freshness}); err == nil {
			t.Fatal("invalid freshness resolved")
		}
	}
}

func mustParseCurrentStillModification(t *testing.T, value string) CurrentStillModification {
	t.Helper()
	modification, err := ParseCurrentStillModification(value)
	if err != nil {
		t.Fatal(err)
	}
	return modification
}

func mustCurrentStillFreshness(t *testing.T, modification CurrentStillModification) CurrentStillFreshness {
	t.Helper()
	freshness, err := CurrentStillFreshnessForModification(modification)
	if err != nil {
		t.Fatal(err)
	}
	return freshness
}

func mustCurrentStillSourceFingerprint(t *testing.T, fingerprint string) CurrentStillFreshness {
	t.Helper()
	freshness, err := CurrentStillFreshnessForSourceFingerprint(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return freshness
}

func testCurrentStillRequest(t *testing.T) CurrentStillRequest {
	t.Helper()
	return CurrentStillRequest{SourceLibraryID: "synthetic-library", AssetUUID: "synthetic-asset", Freshness: mustCurrentStillFreshness(t, mustParseCurrentStillModification(t, "2026-07-11T12:00:00.123Z"))}
}
