package place

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

func TestNoPlacemarkStopsWithoutSuccessfulCache(t *testing.T) {
	cacheDir := t.TempDir()
	resolver := newResolver(ResolverOptions{CacheDir: cacheDir, RadiusMeters: 150}, func(context.Context, Input, float64) (Result, error) {
		return Result{}, ErrProviderNoResult
	})
	input := Input{
		AssetID:  "asset:no-placemark",
		TakenAt:  "2025-10-06T12:00:00Z",
		Location: Coordinate{Latitude: 0.00001, Longitude: -30.00001},
	}

	resolved := resolver.ResolveProvider(context.Background(), input)
	if resolved.Result != nil || !errors.Is(resolved.ProviderErr, ErrProviderNoResult) {
		t.Fatalf("no-result resolution = %+v", resolved)
	}
	path, err := cachePath(cacheDir, input, 150)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no-result cache file error = %v", err)
	}
}

func TestLegacyNoPlacemarkCacheIsRejected(t *testing.T) {
	cacheDir := t.TempDir()
	resolver := NewResolver(ResolverOptions{CacheDir: cacheDir, RadiusMeters: 150})
	input := Input{Location: Coordinate{Latitude: 0.00001, Longitude: -30.00001}}
	legacy := Result{
		Input:        input,
		Provider:     "apple",
		Source:       "apple_corelocation_mapkit",
		RadiusMeters: 150,
		POIStatus:    POIStatusNone,
		POIReason:    NoPlacemarkReason,
	}
	path, err := cachePath(cacheDir, input, 150)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(legacy, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved := resolver.ResolveCached(context.Background(), input)
	if resolved.Result != nil || resolved.CacheStatus != "miss" {
		t.Fatalf("legacy no-placemark cache = %+v", resolved)
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
