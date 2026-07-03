package archive

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
