package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
	"github.com/openclaw/photoscrawl/internal/place"
)

func TestClassifyPlacePhaseParksThrottledLocatedAssets(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	accuracy := 8.0
	imagePath := filepath.Join(t.TempDir(), "fixture.jpeg")
	if err := writeFixtureFile(imagePath); err != nil {
		t.Fatal(err)
	}
	seedClassifyPlaceAssets(t, paths, []photos.Asset{
		{
			LocalIdentifier: "located-throttled",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-28T12:00:00Z",
			Width:           100,
			Height:          80,
			Location: &photos.Location{
				Latitude:           52.3676,
				Longitude:          4.9041,
				HorizontalAccuracy: &accuracy,
			},
			Resources: []photos.Resource{{
				Type:             "photo",
				UTI:              "public.jpeg",
				OriginalFilename: "located.jpeg",
				LocalPath:        imagePath,
				Availability:     "local",
				AvailableLocally: true,
			}},
		},
		{
			LocalIdentifier: "no-gps",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-27T12:00:00Z",
			Width:           100,
			Height:          80,
			Resources: []photos.Resource{{
				Type:             "photo",
				UTI:              "public.jpeg",
				OriginalFilename: "no-gps.jpeg",
				LocalPath:        imagePath,
				Availability:     "local",
				AvailableLocally: true,
			}},
		},
	})
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	inputs := loadTestClassifyInputs(t, ctx, db, "")
	var providerCalls int
	var sleeps []time.Duration
	resolver := classifyPlaceResolver{
		key: testClassifyPlaceKey,
		resolveCached: func(context.Context, place.Input) place.ResolveResult {
			return place.ResolveResult{CacheStatus: "miss"}
		},
		resolveProvider: func(context.Context, place.Input) place.ResolveResult {
			providerCalls++
			return place.ResolveResult{
				CacheStatus:     "miss",
				ProviderAttempt: true,
				ProviderError:   "Apple reverse geocode failed: The operation could not be completed. (MKErrorDomain error 3.)",
				ProviderErr:     place.ErrProviderThrottled,
			}
		},
		sleep: func(_ context.Context, duration time.Duration) error {
			sleeps = append(sleeps, duration)
			return nil
		},
	}
	logs := &recordingClassifyLogSink{}
	var result ClassifyResult
	ready, err := resolveClassifyPlaces(ctx, db, inputs, nil, nil, resolver, fixedClock("2026-05-28T13:00:00Z"), &result, classifyLogger{sink: logs})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := localIdentifiers(ready), []string{"no-gps"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ready inputs = %#v, want %#v", got, want)
	}
	if providerCalls != 3 || result.PlaceProviderAttempts != 3 || result.PlaceProviderFailures != 3 {
		t.Fatalf("provider calls=%d result=%#v", providerCalls, result)
	}
	// No sleep after the final attempt: the run gives up on live geocoding
	// instead of waiting out a backoff it will never use.
	if want := []time.Duration{2 * time.Second, 10 * time.Second}; !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff sleeps = %#v, want %#v", sleeps, want)
	}
	assertQueueState(t, ctx, paths, "located-throttled", classifyQueueStatePlacePending)
	assertQueueState(t, ctx, paths, "no-gps", classifyQueueStatePending)
	assertRecordedLogEvent(t, logs, "place_parked")
	assertRecordedPlaceGeocode(t, logs, "throttled")
}

func TestClassifyPlacePhaseUnparksCachedPendingAssets(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	accuracy := 8.0
	seedClassifyPlaceAssets(t, paths, []photos.Asset{{
		LocalIdentifier: "pending-cached",
		MediaType:       "image",
		MediaSubtypes:   "0",
		CreationDate:    "2026-05-28T12:00:00Z",
		Width:           100,
		Height:          80,
		Location: &photos.Location{
			Latitude:           52.3676,
			Longitude:          4.9041,
			HorizontalAccuracy: &accuracy,
		},
	}})
	setQueueStateForLocalIdentifier(t, ctx, paths, "pending-cached", classifyQueueStatePlacePending, classifyPlacePendingReason)
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	resolver := classifyPlaceResolver{
		key: testClassifyPlaceKey,
		resolveCached: func(context.Context, place.Input) place.ResolveResult {
			return place.ResolveResult{Result: fixturePlaceResult(), CacheStatus: "hit"}
		},
		resolveProvider: func(context.Context, place.Input) place.ResolveResult {
			t.Fatal("provider must not run for cache hits")
			return place.ResolveResult{}
		},
	}
	pending, err := loadPlacePendingInputs(ctx, db, resolver, classifyPlacePendingKeyLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending inputs = %d, want 1", len(pending))
	}
	logs := &recordingClassifyLogSink{}
	var result ClassifyResult
	ready, err := resolveClassifyPlaces(ctx, db, nil, pending, nil, resolver, fixedClock("2026-05-28T13:00:00Z"), &result, classifyLogger{sink: logs})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("ready inputs = %#v, want none", ready)
	}
	if result.PlaceCacheHits != 1 {
		t.Fatalf("place cache hits = %d, want 1", result.PlaceCacheHits)
	}
	assertQueueState(t, ctx, paths, "pending-cached", classifyQueueStateMetadataClassified)
	assertRecordedLogEvent(t, logs, "place_unparked")
}

func TestLoadClassifyInputsExcludesPlacePending(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	accuracy := 8.0
	seedClassifyPlaceAssets(t, paths, []photos.Asset{{
		LocalIdentifier: "pending-place",
		MediaType:       "image",
		MediaSubtypes:   "0",
		CreationDate:    "2026-05-28T12:00:00Z",
		Width:           100,
		Height:          80,
		Location: &photos.Location{
			Latitude:           52.3676,
			Longitude:          4.9041,
			HorizontalAccuracy: &accuracy,
		},
	}})
	setQueueStateForLocalIdentifier(t, ctx, paths, "pending-place", classifyQueueStatePlacePending, classifyPlacePendingReason)
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	if inputs := loadTestClassifyInputs(t, ctx, db, "fixture-vision"); len(inputs) != 0 {
		t.Fatalf("place_pending was selected for model refresh: %#v", localIdentifiers(inputs))
	}
}

func TestLoadPlacePendingInputsCapsDistinctKeys(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	accuracy := 8.0
	assets := make([]photos.Asset, 0, classifyPlacePendingKeyLimit+1)
	for i := 0; i < classifyPlacePendingKeyLimit+1; i++ {
		assets = append(assets, photos.Asset{
			LocalIdentifier: fmt.Sprintf("pending-%03d", i),
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    fmt.Sprintf("2026-05-28T12:%02d:00Z", i%60),
			Width:           100,
			Height:          80,
			Location: &photos.Location{
				Latitude:           40 + float64(i)/1000,
				Longitude:          -70 - float64(i)/1000,
				HorizontalAccuracy: &accuracy,
			},
		})
	}
	seedClassifyPlaceAssets(t, paths, assets)
	setAllQueueStatesForTest(t, ctx, paths, classifyQueueStatePlacePending, classifyPlacePendingReason)
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	resolver := classifyPlaceResolver{key: testClassifyPlaceKey}
	pending, err := loadPlacePendingInputs(ctx, db, resolver, classifyPlacePendingKeyLimit)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	for _, input := range pending {
		keys[testClassifyPlaceKey(classifyPlaceInput(input))] = true
	}
	if len(keys) != classifyPlacePendingKeyLimit || len(pending) != classifyPlacePendingKeyLimit {
		t.Fatalf("pending inputs=%d keys=%d, want %d", len(pending), len(keys), classifyPlacePendingKeyLimit)
	}
}

// The place cache can list the same business twice; writing must dedupe
// instead of failing on the derived observation id.
func TestWritePlaceClassificationDedupesIdenticalCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)

	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		inputs, err := loadClassifyInputs(ctx, tx, 0, "")
		if err != nil {
			return err
		}
		input := inputs[0]
		duplicate := place.POICandidate{
			Name:      "Twice Listed Cafe",
			Category:  "cafe",
			DistanceM: 12,
			Tier:      place.TierVenueCandidate,
			Source:    "fixture",
		}
		input.Place = &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				Provider:      "apple",
				Source:        "fixture",
				RadiusMeters:  150,
				POIStatus:     place.POIStatusFound,
				POICandidates: []place.POICandidate{duplicate, duplicate},
			},
		}
		_, err = writePlaceClassification(ctx, tx, input, venuePlausibility{})
		return err
	}); err != nil {
		t.Fatalf("duplicate candidates must dedupe, not fail: %v", err)
	}
}

func seedClassifyPlaceAssets(t *testing.T, paths Paths, assets []photos.Asset) {
	t.Helper()
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	if _, err := Sync(context.Background(), paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider: fakeProvider{snapshot: photos.LibrarySnapshot{
			Provider:            "fake",
			PhotosVersion:       "fixture",
			AuthorizationStatus: "authorized",
			Assets:              assets,
		}},
		Now: fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T, ctx context.Context, paths Paths) *store.Store {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func loadTestClassifyInputs(t *testing.T, ctx context.Context, db *store.Store, refreshModelID string) []classifyInput {
	t.Helper()
	var inputs []classifyInput
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inputs, err = loadClassifyInputs(ctx, tx, 0, refreshModelID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return inputs
}

func setQueueStateForLocalIdentifier(t *testing.T, ctx context.Context, paths Paths, localIdentifier, state, reason string) {
	t.Helper()
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update classification_queue
set state = ?, reason = ?, updated_at = ?
where asset_id = (
  select id
  from asset
  where local_identifier = ?
)
`, state, reason, fixedClock("2026-05-28T12:30:00Z")().Format(time.RFC3339Nano), localIdentifier)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func setAllQueueStatesForTest(t *testing.T, ctx context.Context, paths Paths, state, reason string) {
	t.Helper()
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
update classification_queue
set state = ?, reason = ?, updated_at = ?
`, state, reason, fixedClock("2026-05-28T12:30:00Z")().Format(time.RFC3339Nano))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func testClassifyPlaceKey(input place.Input) string {
	return place.CacheKey(input, 150)
}

func fixturePlaceResult() *place.Result {
	return &place.Result{
		Provider:     "apple",
		Source:       "fixture",
		RadiusMeters: 150,
		Address: &place.Address{
			Locality:  "Example City",
			Country:   "Example Country",
			Formatted: "Example City, Example Country",
			Source:    "fixture",
		},
		POIStatus: place.POIStatusNone,
	}
}

func localIdentifiers(inputs []classifyInput) []string {
	out := make([]string, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, input.LocalIdentifier)
	}
	return out
}

func assertRecordedPlaceGeocode(t *testing.T, sink *recordingClassifyLogSink, outcome string) {
	t.Helper()
	for _, got := range sink.events {
		if got.event == "place_geocode" && strings.Contains(got.message, "key=") && strings.Contains(got.message, "outcome="+outcome) && strings.Contains(got.message, "duration_ms=") {
			return
		}
	}
	t.Fatalf("missing place_geocode outcome %q in %#v", outcome, sink.events)
}

func writeFixtureFile(path string) error {
	return os.WriteFile(path, []byte("fixture image bytes"), 0o644)
}

func TestClassifyPlacePhaseTimeoutStopsLiveGeocoding(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	accuracy := 8.0
	imagePath := filepath.Join(t.TempDir(), "fixture.jpeg")
	if err := writeFixtureFile(imagePath); err != nil {
		t.Fatal(err)
	}
	seedClassifyPlaceAssets(t, paths, []photos.Asset{
		{
			LocalIdentifier: "located-timeout",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-28T12:00:00Z",
			Width:           100,
			Height:          80,
			Location: &photos.Location{
				Latitude:           41.6054,
				Longitude:          1.8302,
				HorizontalAccuracy: &accuracy,
			},
			Resources: []photos.Resource{{
				Type:             "photo",
				UTI:              "public.jpeg",
				OriginalFilename: "located.jpeg",
				LocalPath:        imagePath,
				Availability:     "local",
				AvailableLocally: true,
			}},
		},
	})
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	inputs := loadTestClassifyInputs(t, ctx, db, "")
	var providerCalls int
	var sleeps []time.Duration
	resolver := classifyPlaceResolver{
		key: testClassifyPlaceKey,
		resolveCached: func(context.Context, place.Input) place.ResolveResult {
			return place.ResolveResult{CacheStatus: "miss"}
		},
		resolveProvider: func(context.Context, place.Input) place.ResolveResult {
			providerCalls++
			return place.ResolveResult{
				CacheStatus:     "miss",
				ProviderAttempt: true,
				ProviderError:   "Apple reverse geocode timed out",
				ProviderErr:     place.ErrProviderTimeout,
			}
		},
		sleep: func(_ context.Context, duration time.Duration) error {
			sleeps = append(sleeps, duration)
			return nil
		},
	}
	logs := &recordingClassifyLogSink{}
	var result ClassifyResult
	ready, err := resolveClassifyPlaces(ctx, db, inputs, nil, nil, resolver, fixedClock("2026-05-28T13:00:00Z"), &result, classifyLogger{sink: logs})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 {
		t.Fatalf("ready inputs = %#v", localIdentifiers(ready))
	}
	// One call, no retries, no backoff sleeps: a tarpitted geocoder stops
	// live geocoding for the whole run instead of burning 20s per key.
	if providerCalls != 1 || len(sleeps) != 0 {
		t.Fatalf("provider calls=%d sleeps=%#v", providerCalls, sleeps)
	}
	assertQueueState(t, ctx, paths, "located-timeout", classifyQueueStatePlacePending)
	assertRecordedPlaceGeocode(t, logs, "timeout")
}

// A place that resolved to nothing must not put an empty place_context
// block in the model sidecar; a real address must.
func TestSidecarOmitsEmptyPlaceContext(t *testing.T) {
	t.Parallel()
	input := classifyInput{
		AssetID:      "asset:sidecar-empty-place",
		CreationDate: "2025-10-06T12:00:00Z",
		MediaType:    "image",
		HasLocation:  true,
		Latitude:     0.00001,
		Longitude:    -30.00001,
		Place:        &classifyPlaceContext{Result: place.Result{POIStatus: place.POIStatusNone, POIReason: place.NoPlacemarkReason}},
	}
	data, err := photoCardMetadataJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	location, ok := payload["location"].(map[string]any)
	if !ok {
		t.Fatalf("location block missing: %s", data)
	}
	if _, exists := location["place_context"]; exists {
		t.Fatalf("empty place_context reached the sidecar: %s", data)
	}
	if _, exists := location["gps"]; !exists {
		t.Fatalf("gps missing from sidecar: %s", data)
	}

	input.Place = &classifyPlaceContext{Result: place.Result{
		Address:   &place.Address{Locality: "Innsbruck", Country: "Austria", Formatted: "Innsbruck, Austria"},
		POIStatus: place.POIStatusNone,
	}}
	data, err = photoCardMetadataJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	location = payload["location"].(map[string]any)
	context, ok := location["place_context"].(map[string]any)
	if !ok {
		t.Fatalf("place_context missing for real address: %s", data)
	}
	if context["address_line"] == "" {
		t.Fatalf("address_line empty: %s", data)
	}
	if _, exists := context["venue_candidates"]; exists {
		t.Fatalf("empty venue_candidates key reached the sidecar: %s", data)
	}
}
