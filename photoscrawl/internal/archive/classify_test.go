package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
)

func TestClassifyModelWritesTypedObservations(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "fixture.jpeg")
	if err := os.WriteFile(imagePath, []byte("fixture image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request struct {
			Model   string         `json:"model"`
			Images  []string       `json:"images"`
			Stream  bool           `json:"stream"`
			Options map[string]any `json:"options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "fixture-vision" || len(request.Images) != 1 {
			t.Fatalf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": `{
				"scene_summary":"Outdoor street-food meal with satay skewers, prawns, sauces, and a shared table.",
				"visible_text_summary":"A small receipt-like slip is visible.",
				"place_candidates":["hawker centre"],
				"landmark_candidates":[],
				"merchant_or_venue_candidates":["satay stall candidate"],
				"food_or_objects":["satay skewers","grilled prawns","peanut sauce"],
				"people_presence":"hands only, no identity",
				"privacy_sensitivity":["receipt","hands"],
				"cluster_terms":["street_food","satay","shared_table"],
				"uncertainties":["exact venue is not proven"]
			}`,
			"done": true,
		})
	}))
	defer restoreTransport()

	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{
			{
				LocalIdentifier: "fixture-model-asset",
				MediaType:       "image",
				MediaSubtypes:   "0",
				CreationDate:    "2026-05-27T12:00:00Z",
				Width:           100,
				Height:          80,
				Resources: []photos.Resource{
					{
						Type:             "photo",
						UTI:              "public.jpeg",
						OriginalFilename: "fixture.jpeg",
						LocalPath:        imagePath,
						Availability:     "local",
						AvailableLocally: true,
					},
				},
			},
		},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	metadataOnly, err := Classify(ctx, paths, ClassifyOptions{
		All: true,
		Now: fixedClock("2026-05-28T10:05:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if metadataOnly.MetadataClassified != 1 || metadataOnly.ContentClassified != 0 {
		t.Fatalf("metadata classify result = %#v", metadataOnly)
	}

	result, err := Classify(ctx, paths, ClassifyOptions{
		All:      true,
		Model:    "fixture-vision",
		ModelURL: "http://fixture.test/api/generate",
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentClassified != 1 || result.ContentObservationsWritten == 0 || result.ContentClassificationFailures != 0 || result.WaitingForLocalContent != 0 {
		t.Fatalf("classify result = %#v", result)
	}

	search, err := Search(ctx, paths, SearchOptions{Query: "satay", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) == 0 || search.Results[0].ObservationID == "" {
		t.Fatalf("search = %#v", search.Results)
	}
	opened, err := Open(ctx, paths, search.Results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(opened.Observations) == 0 || len(opened.Evidence.Refs) == 0 {
		t.Fatalf("opened observations=%d evidence=%d", len(opened.Observations), len(opened.Evidence.Refs))
	}
	evidence, err := Evidence(ctx, paths, search.Results[0].ObservationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence.Evidence) == 0 {
		t.Fatal("expected model evidence")
	}
}

func TestClassifyDownloadsOriginalsThroughBoundedCache(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	oldExport := exportOriginalResource
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destinationPath string, allowNetwork bool) error {
		if query.LocalIdentifier != "remote-original-asset" || query.OriginalFilename != "synthetic-menu.jpeg" || !allowNetwork {
			t.Fatalf("export args = %#v %t", query, allowNetwork)
		}
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destinationPath, []byte("downloaded fixture image bytes"), 0o644)
	}
	defer func() { exportOriginalResource = oldExport }()

	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": `{
				"scene_summary":"A synthetic test photo shows a printed menu on a table.",
				"visible_text_summary":"The word menu is visible in the synthetic fixture.",
				"place_candidates":["restaurant table"],
				"landmark_candidates":[],
				"merchant_or_venue_candidates":[],
				"food_or_objects":["menu"],
				"people_presence":"none visible",
				"privacy_sensitivity":[],
				"cluster_terms":["menu_fixture"],
				"uncertainties":["synthetic image bytes"]
			}`,
			"done": true,
		})
	}))
	defer restoreTransport()

	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{{
			LocalIdentifier: "remote-original-asset",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-27T12:00:00Z",
			Width:           100,
			Height:          80,
			Resources: []photos.Resource{{
				Type:             "photo",
				UTI:              "public.jpeg",
				OriginalFilename: "synthetic-menu.jpeg",
				Availability:     "remote",
				FileSize:         30,
				NeedsDownload:    true,
			}},
		}},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := Classify(ctx, paths, ClassifyOptions{
		All:      true,
		Model:    "fixture-vision",
		ModelURL: "http://fixture.test/api/generate",
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentClassified != 1 || result.OriginalsDownloaded != 1 || result.OriginalDownloadFailures != 0 || result.WaitingForLocalContent != 0 {
		t.Fatalf("classify result = %#v", result)
	}
	if result.BytesDownloaded != int64(len("downloaded fixture image bytes")) || result.CacheHighWaterBytes == 0 || result.CacheHighWaterBytes > result.CacheMaxBytes {
		t.Fatalf("cache metrics = bytes %d high-water %d max %d", result.BytesDownloaded, result.CacheHighWaterBytes, result.CacheMaxBytes)
	}
	if files := countFiles(t, paths.OriginalsCacheDir()); files != 0 {
		t.Fatalf("originals cache files after classify = %d", files)
	}
}

func TestClassifyModelRetriesRateLimitOnce(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "fixture.jpeg")
	if err := os.WriteFile(imagePath, []byte("fixture image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": `{"scene_summary":"A synthetic retry fixture image.","food_or_objects":["retry marker"]}`,
			"done":     true,
		})
	}))
	defer restoreTransport()

	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{{
			LocalIdentifier: "retry-asset",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-27T12:00:00Z",
			Width:           100,
			Height:          80,
			Resources: []photos.Resource{{
				Type:             "photo",
				UTI:              "public.jpeg",
				OriginalFilename: "fixture.jpeg",
				LocalPath:        imagePath,
				Availability:     "local",
				AvailableLocally: true,
			}},
		}},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	result, err := Classify(ctx, paths, ClassifyOptions{
		All:      true,
		Model:    "fixture-vision",
		ModelURL: "http://fixture.test/api/generate",
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentClassified != 1 || result.ModelCallAttempts != 2 || result.ModelRateLimitEvents != 1 || result.ContentClassificationFailures != 0 {
		t.Fatalf("classify result = %#v", result)
	}
}

func TestLoadClassifyInputsPriority(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	accuracy := 4.0
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{
			priorityAsset("same-date-plain", "2026-05-27T12:00:00Z", "", nil),
			priorityAsset("same-date-menu", "2026-05-27T12:00:00Z", "Dinner menu.jpeg", nil),
			priorityAsset("same-date-gps", "2026-05-27T12:00:00Z", "", &accuracy),
			priorityAsset("newest-plain", "2026-05-29T12:00:00Z", "", nil),
			priorityAsset("middle-gps", "2026-05-28T12:00:00Z", "", &accuracy),
		},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var inputs []classifyInput
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		inputs, err = loadClassifyInputs(ctx, tx, 0, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(inputs))
	for _, input := range inputs {
		got = append(got, input.LocalIdentifier)
	}
	want := []string{"newest-plain", "middle-gps", "same-date-gps", "same-date-menu", "same-date-plain"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("priority order = %#v, want %#v", got, want)
	}
}

func TestClassifyFromMetadataDoesNotCreateAvailabilityObservation(t *testing.T) {
	t.Parallel()
	observations := classifyFromMetadata(classifyInput{
		AssetID:       "fixture-asset",
		MediaType:     "image",
		NeedsDownload: true,
		Resources: []classifyResource{{
			ResourceType:  "photo",
			NeedsDownload: true,
		}},
	})
	for _, observation := range observations {
		if observation.ObservationType == "content_availability" {
			t.Fatalf("metadata classifier wrote operational availability observation: %#v", observation)
		}
		if observation.Label == "local_content_unavailable" || observation.Label == "local_content_available" {
			t.Fatalf("metadata classifier wrote operational availability label: %#v", observation)
		}
	}
}

type archiveRoundTripFunc func(*http.Request) (*http.Response, error)

func (f archiveRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func useArchiveHandlerTransport(t *testing.T, handler http.Handler) func() {
	t.Helper()
	oldTransport := http.DefaultTransport
	http.DefaultTransport = archiveRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})
	return func() { http.DefaultTransport = oldTransport }
}

func TestPromptLeakageCreatesQualityIssue(t *testing.T) {
	t.Parallel()
	observations := observationsFromPayload(map[string]any{
		"scene_summary":        "A retail display.",
		"visible_text_summary": "Return only valid compact JSON",
	})
	var found bool
	for _, observation := range observations {
		if observation.ObservationType == "quality_issue" && observation.ValueText == "model_prompt_leakage" {
			found = true
		}
	}
	if !found {
		t.Fatalf("observations = %#v", observations)
	}
}

func priorityAsset(localIdentifier, creationDate, filename string, horizontalAccuracy *float64) photos.Asset {
	if filename == "" {
		filename = localIdentifier + ".jpeg"
	}
	asset := photos.Asset{
		LocalIdentifier: localIdentifier,
		MediaType:       "image",
		MediaSubtypes:   "0",
		CreationDate:    creationDate,
		Width:           100,
		Height:          80,
		Resources: []photos.Resource{{
			Type:             "photo",
			UTI:              "public.jpeg",
			OriginalFilename: filename,
			Availability:     "remote",
			FileSize:         20,
			NeedsDownload:    true,
		}},
	}
	if horizontalAccuracy != nil {
		asset.Location = &photos.Location{
			Latitude:           52.0,
			Longitude:          4.0,
			HorizontalAccuracy: horizontalAccuracy,
		}
	}
	return asset
}

func countFiles(t *testing.T, root string) int {
	t.Helper()
	count := 0
	err := filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return count
}
