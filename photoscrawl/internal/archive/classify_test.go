package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
	"github.com/openclaw/photoscrawl/internal/place"
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
			"response": fixtureCardResponse(
				"Outdoor street-food meal with satay skewers, prawns, sauces, and a shared table.",
				"A synthetic outdoor food table holds satay skewers, grilled prawns, peanut sauce, and shared dishes. A small receipt-like slip is visible near the edge of the table. Only hands are visible, with no identifiable people.",
				"Nearby hawker centre candidate.",
				"A small receipt-like slip is visible.",
				"exact venue is not proven",
			),
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
	assertContentOutcomesSumToProcessed(t, result)

	search, err := Search(ctx, paths, SearchOptions{Query: "satay", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("search = %#v", search.Results)
	}
	opened, err := Open(ctx, paths, search.Results[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Model.Summary == "" || opened.Model.Description == "" {
		t.Fatalf("opened card=%#v", opened)
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
			"response": fixtureCardResponse(
				"A synthetic test photo shows a printed menu on a table.",
				"The image shows a printed menu lying on a table in a synthetic fixture. The word menu is visible and is the main readable text. No people are visible.",
				"Restaurant table candidate.",
				"The word menu is visible.",
				"synthetic image bytes",
			),
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
	assertContentOutcomesSumToProcessed(t, result)
	if result.BytesDownloaded != int64(len("downloaded fixture image bytes")) || result.CacheHighWaterBytes == 0 || result.CacheHighWaterBytes > result.CacheMaxBytes {
		t.Fatalf("cache metrics = bytes %d high-water %d max %d", result.BytesDownloaded, result.CacheHighWaterBytes, result.CacheMaxBytes)
	}
	if files := countFiles(t, paths.OriginalsCacheDir()); files != 0 {
		t.Fatalf("originals cache files after classify = %d", files)
	}
}

func TestClassifyContentOutcomesSumToProcessed(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	parseImagePath := filepath.Join(t.TempDir(), "parse.jpeg")
	if err := os.WriteFile(parseImagePath, []byte("parse fixture image bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	videoPath := filepath.Join(t.TempDir(), "clip.mov")
	if err := os.WriteFile(videoPath, []byte("video fixture bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	oldExport := exportOriginalResource
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destinationPath string, allowNetwork bool) error {
		switch query.LocalIdentifier {
		case "not-in-photokit":
			return photos.ErrPhotoKitAssetNotFound
		case "download-fails":
			return errors.New("synthetic download unavailable")
		default:
			t.Fatalf("unexpected export query = %#v allowNetwork=%t destination=%q", query, allowNetwork, destinationPath)
			return nil
		}
	}
	defer func() { exportOriginalResource = oldExport }()

	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"response": `{"scene_summary":}`,
			"done":     true,
		})
	}))
	defer restoreTransport()

	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{
			{
				LocalIdentifier: "parse-fails",
				MediaType:       "image",
				MediaSubtypes:   "0",
				CreationDate:    "2026-05-31T12:00:00Z",
				Width:           100,
				Height:          80,
				Resources: []photos.Resource{{
					Type:             "photo",
					UTI:              "public.jpeg",
					OriginalFilename: "parse.jpeg",
					LocalPath:        parseImagePath,
					Availability:     "local",
					AvailableLocally: true,
				}},
			},
			remoteFixtureAsset("not-in-photokit", "2026-05-30T12:00:00Z"),
			remoteFixtureAsset("download-fails", "2026-05-29T12:00:00Z"),
			{
				LocalIdentifier: "no-content",
				MediaType:       "image",
				MediaSubtypes:   "0",
				CreationDate:    "2026-05-28T12:00:00Z",
				Width:           100,
				Height:          80,
			},
			{
				LocalIdentifier: "video-skipped",
				MediaType:       "video",
				MediaSubtypes:   "0",
				CreationDate:    "2026-05-27T12:00:00Z",
				Width:           100,
				Height:          80,
				Resources: []photos.Resource{{
					Type:             "video",
					UTI:              "public.mpeg-4",
					OriginalFilename: "clip.mov",
					LocalPath:        videoPath,
					Availability:     "local",
					AvailableLocally: true,
				}},
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
	result, err := Classify(ctx, paths, ClassifyOptions{
		All:      true,
		Model:    "fixture-vision",
		ModelURL: "http://fixture.test/api/generate",
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 5 ||
		result.ContentFailedParse != 1 ||
		result.ContentFailedDownload != 1 ||
		result.ContentNotInPhotoKit != 1 ||
		result.ContentNoContentAvailable != 1 ||
		result.ContentSkippedUnsupportedMedia != 1 {
		t.Fatalf("classify result = %#v", result)
	}
	assertContentOutcomesSumToProcessed(t, result)
	if result.ContentClassificationFailures != 1 || result.OriginalDownloadFailures != 2 || result.WaitingForLocalContent != 1 {
		t.Fatalf("aggregate counters = %#v", result)
	}

	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var nonTerminal int
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
select count(*)
from classification_queue
where state in ('pending', 'metadata_classified')
`).Scan(&nonTerminal)
	}); err != nil {
		t.Fatal(err)
	}
	if nonTerminal != 0 {
		t.Fatalf("non-terminal classified rows = %d", nonTerminal)
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
			"response": fixtureCardResponse(
				"A synthetic retry fixture image.",
				"The synthetic image is used to prove retry handling. It contains a retry marker and no identifiable people.",
				"",
				"",
				"synthetic fixture",
			),
			"done": true,
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
	assertContentOutcomesSumToProcessed(t, result)
}

func assertContentOutcomesSumToProcessed(t *testing.T, result ClassifyResult) {
	t.Helper()
	sum := result.ContentClassified +
		result.ContentFailedParse +
		result.ContentFailedModel +
		result.ContentFailedDownload +
		result.ContentNotInPhotoKit +
		result.ContentNoContentAvailable +
		result.ContentSkippedUnsupportedMedia
	if sum != result.Processed {
		t.Fatalf("content outcomes sum = %d, processed = %d, result = %#v", sum, result.Processed, result)
	}
	if result.ContentOutcomeTotal != result.Processed {
		t.Fatalf("content_outcome_total = %d, processed = %d, result = %#v", result.ContentOutcomeTotal, result.Processed, result)
	}
}

func remoteFixtureAsset(localIdentifier, creationDate string) photos.Asset {
	return photos.Asset{
		LocalIdentifier: localIdentifier,
		MediaType:       "image",
		MediaSubtypes:   "0",
		CreationDate:    creationDate,
		Width:           100,
		Height:          80,
		Resources: []photos.Resource{{
			Type:             "photo",
			UTI:              "public.jpeg",
			OriginalFilename: localIdentifier + ".jpeg",
			Availability:     "remote",
			FileSize:         30,
			NeedsDownload:    true,
		}},
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
		inputs, err = loadClassifyInputs(ctx, tx, 0, "")
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

func TestParsePhotoCardRequiresSections(t *testing.T) {
	t.Parallel()
	if _, err := parsePhotoCard("Return only valid compact JSON."); err == nil {
		t.Fatal("expected malformed card to fail")
	}
}

func TestParsePhotoCardRequiresVenuePlausibilityVerdict(t *testing.T) {
	t.Parallel()
	card, err := parsePhotoCard(fixtureCardResponse(
		"A synthetic kitchen cooking scene.",
		"The image shows food preparation on a kitchen counter.",
		"candidate: Synthetic Cafe\nverdict: corroborated\nreason: a visible sign matches the provider candidate.",
		"None",
		"exact city",
	))
	if err != nil {
		t.Fatal(err)
	}
	if card.VenuePlausibility.Verdict != venueVerdictCorroborated || card.VenuePlausibility.CandidateName != "Synthetic Cafe" {
		t.Fatalf("venue plausibility = %#v", card.VenuePlausibility)
	}
}

func TestParsePhotoCardAcceptsVenuePlausibilityAssessment(t *testing.T) {
	t.Parallel()
	card, err := parsePhotoCard(fixtureCardResponse(
		"A synthetic private meal scene.",
		"The image shows a meal in a private indoor setting.",
		"candidate: Synthetic Consultancy\nplausibility: inconsistent\nreason: the visible scene contradicts the venue type.",
		"None",
		"exact venue",
	))
	if err != nil {
		t.Fatal(err)
	}
	if card.VenuePlausibility.Verdict != venueVerdictInconsistent {
		t.Fatalf("venue plausibility = %#v", card.VenuePlausibility)
	}
}

func TestWritePlaceClassificationAppliesVenuePlausibilityAndAddressLine(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verdict   string
		wantVenue string
		wantTier  string
	}{
		{
			name:    "inconsistent",
			verdict: venueVerdictInconsistent,
		},
		{
			name:      "plausible",
			verdict:   venueVerdictPlausible,
			wantVenue: "Synthetic Consultancy",
			wantTier:  place.TierVenueCandidate,
		},
		{
			name:      "corroborated",
			verdict:   venueVerdictCorroborated,
			wantVenue: "Synthetic Consultancy",
			wantTier:  place.TierConfirmedVenue,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := openSyntheticPlaceResult(t, venuePlausibility{
				CandidateName: "Synthetic Consultancy",
				Verdict:       tc.verdict,
				Reason:        "synthetic fixture reason",
			})
			if got, want := opened.Mechanical.Address, "Example Street 23, Example District, Example City, Example Country"; got != want {
				t.Fatalf("address = %q, want %q", got, want)
			}
			if tc.wantVenue == "" {
				if opened.Mechanical.Venue != nil {
					t.Fatalf("inconsistent venue rendered: %#v", opened.Mechanical.Venue)
				}
			} else {
				if opened.Mechanical.Venue == nil || opened.Mechanical.Venue.Name != tc.wantVenue || opened.Mechanical.Venue.Tier != tc.wantTier {
					t.Fatalf("venue = %#v, want %q tier %q", opened.Mechanical.Venue, tc.wantVenue, tc.wantTier)
				}
			}
			if len(opened.Mechanical.VenueCandidates) != 1 {
				t.Fatalf("venue candidates = %#v", opened.Mechanical.VenueCandidates)
			}
			plausibility := opened.Mechanical.VenueCandidates[0].VenuePlausibility
			if plausibility == nil || plausibility.Verdict != tc.verdict {
				t.Fatalf("venue candidate plausibility = %#v, want %q", plausibility, tc.verdict)
			}
		})
	}
}

func fixtureCardResponse(summary, description, venuePlausibility, ocr, uncertainty string) string {
	if strings.TrimSpace(venuePlausibility) == "" {
		venuePlausibility = "verdict: plausible\nreason: no visible contradiction."
	}
	lower := strings.ToLower(strings.TrimSpace(venuePlausibility))
	if !strings.Contains(lower, "verdict:") &&
		!strings.HasPrefix(lower, venueVerdictCorroborated) &&
		!strings.HasPrefix(lower, venueVerdictPlausible) &&
		!strings.HasPrefix(lower, venueVerdictInconsistent) {
		venuePlausibility = "verdict: plausible\nreason: " + venuePlausibility
	}
	if strings.TrimSpace(ocr) == "" {
		ocr = "None"
	}
	if strings.TrimSpace(uncertainty) == "" {
		uncertainty = "None"
	}
	return strings.Join([]string{
		"## One-line summary",
		summary,
		"",
		"## Detailed description",
		description,
		"",
		"## Venue plausibility",
		venuePlausibility,
		"",
		"## OCR and machine-readable text",
		ocr,
		"",
		"## Uncertainty",
		"- " + uncertainty,
	}, "\n")
}

func seedSyntheticPlaceAsset(t *testing.T, paths Paths) {
	t.Helper()
	ctx := context.Background()
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	accuracy := 8.0
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{{
			LocalIdentifier: "fixture-place-asset",
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-27T12:00:00Z",
			Width:           100,
			Height:          80,
			Location: &photos.Location{
				Latitude:           52.0,
				Longitude:          4.0,
				HorizontalAccuracy: &accuracy,
			},
		}},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
}

func openSyntheticPlaceResult(t *testing.T, plausibility venuePlausibility) OpenResult {
	t.Helper()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	assetID := ""
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		inputs, err := loadClassifyInputs(ctx, tx, 0, "")
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("classify inputs = %d, want 1", len(inputs))
		}
		input := inputs[0]
		assetID = input.AssetID
		input.Place = &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				Provider:     "apple",
				Source:       "fixture",
				RadiusMeters: 150,
				GeneratedAt:  fixedClock("2026-05-28T10:05:00Z")(),
				Address: &place.Address{
					Name:            "Example Street 23",
					SubThoroughfare: "23",
					Thoroughfare:    "Example Street",
					SubLocality:     "Example District",
					Locality:        "Example City",
					Country:         "Example Country",
					Formatted:       "Example Street 23, 23 Example Street, Example District, Example City, Example Country",
					Source:          "fixture",
				},
				POIStatus: place.POIStatusFound,
				POICandidates: []place.POICandidate{{
					Name:      "Synthetic Consultancy",
					Category:  "business",
					DistanceM: 14,
					Tier:      place.TierVenueCandidate,
					Source:    "fixture",
				}},
			},
		}
		_, err = writePlaceClassification(ctx, tx, input, plausibility, fixedClock("2026-05-28T10:15:00Z")())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, paths, assetID)
	if err != nil {
		t.Fatal(err)
	}
	return opened
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
