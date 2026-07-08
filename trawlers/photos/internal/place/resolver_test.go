package place

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// A no-placemark resolution must survive the cache round trip: written by
// ResolveProvider, accepted by validateComplete, served by ResolveCached.
// The first version of this fix wrote entries the read path rejected, so
// every run silently re-geocoded the same dead coordinate.
func TestNoPlacemarkResultCacheRoundTrip(t *testing.T) {
	cacheDir := t.TempDir()
	resolver := NewResolver(ResolverOptions{CacheDir: cacheDir, RadiusMeters: 150})
	input := Input{
		AssetID:  "asset:no-placemark",
		TakenAt:  "2025-10-06T12:00:00Z",
		Location: Coordinate{Latitude: 0.00001, Longitude: -30.00001},
	}

	empty := emptyResult(input, 150)
	if err := validateComplete(empty); err != nil {
		t.Fatalf("validateComplete rejects the empty result: %v", err)
	}

	path, err := cachePath(cacheDir, input, 150)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(empty, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved := resolver.ResolveCached(context.Background(), input)
	if resolved.Result == nil || resolved.CacheStatus != "hit" {
		t.Fatalf("cached no-placemark result not served: %+v", resolved)
	}
	if resolved.Result.Address != nil || resolved.Result.POIReason != NoPlacemarkReason {
		t.Fatalf("round-tripped result = %+v", resolved.Result)
	}
}

// An unmarked incomplete cache entry must still be rejected.
func TestIncompleteCacheEntryStillRejected(t *testing.T) {
	if err := validateComplete(Result{POIStatus: POIStatusNone}); err == nil {
		t.Fatal("unmarked address-less result passed validation")
	}
}

func TestClassifyBridgeErrorKinds(t *testing.T) {
	cases := []struct {
		message string
		want    error
	}{
		{"Apple reverse geocode failed: The operation couldn’t be completed. (MKErrorDomain error 3.)", ErrProviderThrottled},
		{"Apple reverse geocode timed out", ErrProviderTimeout},
		{"Apple nearby POI search timed out", ErrProviderTimeout},
		{"Apple reverse geocode returned no placemarks", ErrProviderNoResult},
		{"Apple reverse geocode returned no map items", ErrProviderNoResult},
	}
	for _, tc := range cases {
		if got := classifyBridgeError(tc.message); !errors.Is(got, tc.want) {
			t.Fatalf("classifyBridgeError(%q) = %v, want %v", tc.message, got, tc.want)
		}
	}
	if got := classifyBridgeError("something else"); errors.Is(got, ErrProviderThrottled) || errors.Is(got, ErrProviderTimeout) || errors.Is(got, ErrProviderNoResult) {
		t.Fatalf("unknown message classified as sentinel: %v", got)
	}
}
