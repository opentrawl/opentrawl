package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardformat"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/imagemetadata"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const fixtureModelURL = "http://127.0.0.1:11434/v1"

func TestClassifyModelWritesTypedObservations(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "fixture.jpeg")
	writeSyntheticImage(t, imagePath)
	var fixtureRequestBody []byte
	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type string `json:"type"`
				} `json:"content"`
			} `json:"messages"`
			Tools      []any `json:"tools"`
			ToolChoice any   `json:"tool_choice"`
		}
		var err error
		fixtureRequestBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request: %v", err)
		}
		if err := json.Unmarshal(fixtureRequestBody, &request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.Model != "fixture-vision" || len(request.Messages) != 1 || len(request.Messages[0].Content) != 2 || len(request.Tools) != 1 || request.ToolChoice == nil {
			t.Fatalf("request = %#v", request)
		}
		_ = json.NewEncoder(w).Encode(fixtureToolResponse(fixtureCardResponse(
			"Outdoor street-food meal with satay skewers, prawns, sauces, and a shared table.",
			"A synthetic outdoor food table holds satay skewers, grilled prawns, peanut sauce, and shared dishes. A small receipt-like slip is visible near the edge of the table. Only hands are visible, with no identifiable people.",
			"Nearby hawker centre candidate.",
			"A small receipt-like slip is visible.",
			"exact venue is not proven",
		)))
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
						Type:             "local_original",
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
		Now: fixedClock("2026-05-28T10:05:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if metadataOnly.MetadataClassified != 1 || metadataOnly.ContentClassified != 0 {
		t.Fatalf("metadata classify result = %#v", metadataOnly)
	}
	prepareCheckedCardInputForModelTest(t, ctx, paths, libraryPath, "fixture-model-asset")

	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentClassified != 1 || result.ContentObservationsWritten == 0 || result.ContentClassificationFailures != 0 || result.WaitingForLocalContent != 0 {
		proof, _ := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
		var parseFailure []byte
		if proof != nil {
			_ = proof.DB().QueryRowContext(ctx, `select coalesce(parse_failure, X'') from model_generation_asset limit 1`).Scan(&parseFailure)
			_ = proof.Close()
		}
		t.Fatalf("classify result = %#v parse_failure=%q", result, parseFailure)
	}
	assertContentOutcomesSumToProcessed(t, result)

	proofDB, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proofDB.Close() }()
	var generationID string
	var persistedRequest, retainedResponse []byte
	if err := proofDB.DB().QueryRowContext(ctx, `
select g.id, g.request_body, a.response_body
from model_generation g
join model_generation_asset ga on ga.generation_id = g.id
join model_generation_attempt a on a.generation_id = g.id
where ga.asset_id = ?
`, stableID("asset", stableID("source_library", libraryPath), "fixture-model-asset")).Scan(&generationID, &persistedRequest, &retainedResponse); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persistedRequest, fixtureRequestBody) {
		t.Fatalf("persisted request differs from fixture request\npersisted: %s\nfixture: %s", persistedRequest, fixtureRequestBody)
	}
	var observations, linkedObservations int
	var summary string
	if err := proofDB.DB().QueryRowContext(ctx, `
select count(*), count(generation_id), max(case when observation_type = 'card_summary' then value_text else '' end)
from model_observation
where asset_id = ? and superseded_at is null
`, stableID("asset", stableID("source_library", libraryPath), "fixture-model-asset")).Scan(&observations, &linkedObservations, &summary); err != nil {
		t.Fatal(err)
	}
	if observations == 0 || linkedObservations != observations || summary == "" {
		t.Fatalf("observation provenance: rows=%d linked=%d summary=%q generation=%s", observations, linkedObservations, summary, generationID)
	}
	selectedImage, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW selected Photos image input: %s", base64.StdEncoding.EncodeToString(selectedImage))
	t.Logf("RAW final and persisted provider request: %s", persistedRequest)
	t.Logf("RAW fixture request body: %s", fixtureRequestBody)
	t.Logf("RAW retained provider response before parser: %s", retainedResponse)
	t.Logf("RAW parser result to stored observation: generation_id=%s summary=%s", generationID, summary)

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

func TestClassifyRecardsBySupersedingHistory(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	provider := fakeProvider{snapshot: fakeSnapshot(false, false)}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	db, err := openArchive(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	firstClassifier := modelClassifier{modelID: "fixture-vision", promptVersion: modelPromptVersion}
	firstInput := loadTestClassifyInputs(t, ctx, db, "")[0]
	firstInput.Place = recardPlaceContext("Old Synthetic Pier")
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		generationID, err := insertRecardGeneration(ctx, tx, firstInput.AssetID, "old")
		if err != nil {
			return err
		}
		_, _, err = writeModelClassification(ctx, tx, firstInput, firstClassifier, recardModelResult(
			"Old oldcardterm synthetic card.",
			"Old oldcardterm description for the retained history row.",
			venuePlausibility{CandidateID: "place_1_candidate_1", Verdict: venueVerdictPlausible, Reason: "old synthetic place"},
		), recardPreparedRequest("Old Synthetic Pier"), fixedClock("2026-05-28T10:15:00Z")(), generationID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	provider.snapshot = fakeSnapshot(false, false)
	provider.snapshot.Assets[0].Favorite = true
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:30:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	secondInput := loadTestClassifyInputs(t, ctx, db, "")[0]
	secondInput.Place = recardPlaceContext("New Synthetic Pier")
	secondClassifier := modelClassifier{modelID: "fixture-vision-v2", promptVersion: modelPromptVersion}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		generationID, err := insertRecardGeneration(ctx, tx, secondInput.AssetID, "new")
		if err != nil {
			return err
		}
		_, _, err = writeModelClassification(ctx, tx, secondInput, secondClassifier, recardModelResult(
			"New newcardterm synthetic card.",
			"New newcardterm description for the active row.",
			venuePlausibility{CandidateID: "place_1_candidate_1", Verdict: venueVerdictPlausible, Reason: "new synthetic place"},
		), recardPreparedRequest("New Synthetic Pier"), fixedClock("2026-05-28T10:45:00Z")(), generationID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	sourceID := stableID("source_library", libraryPath)
	assetID := stableID("asset", sourceID, "fixture-asset-1")
	var oldModelStaleSince, oldModelSupersededAt, newModelStaleSince, newModelSupersededAt string
	if err := db.DB().QueryRowContext(ctx, `
select coalesce(stale_since, ''), coalesce(superseded_at, '')
from model_observation
where asset_id = ? and observation_type = ? and value_text like 'Old oldcardterm%'
`, assetID, modelObservationCardSummary).Scan(&oldModelStaleSince, &oldModelSupersededAt); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `
select coalesce(stale_since, ''), coalesce(superseded_at, '')
from model_observation
where asset_id = ? and observation_type = ? and value_text like 'New newcardterm%'
`, assetID, modelObservationCardSummary).Scan(&newModelStaleSince, &newModelSupersededAt); err != nil {
		t.Fatal(err)
	}
	if oldModelStaleSince != "2026-05-28T10:30:00Z" || oldModelSupersededAt != "2026-05-28T10:45:00Z" {
		t.Fatalf("old model row stale=%q superseded=%q", oldModelStaleSince, oldModelSupersededAt)
	}
	if newModelStaleSince != "" || newModelSupersededAt != "" {
		t.Fatalf("new model row stale=%q superseded=%q", newModelStaleSince, newModelSupersededAt)
	}

	var oldPlaceStaleSince, oldPlaceSupersededAt string
	if err := db.DB().QueryRowContext(ctx, `
select coalesce(stale_since, ''), coalesce(superseded_at, '')
from place_observation
where asset_id = ? and observation_type = 'venue' and value_text = 'Old Synthetic Pier'
`, assetID).Scan(&oldPlaceStaleSince, &oldPlaceSupersededAt); err != nil {
		t.Fatal(err)
	}
	if oldPlaceStaleSince != "2026-05-28T10:30:00Z" || oldPlaceSupersededAt != "2026-05-28T10:45:00Z" {
		t.Fatalf("old place row stale=%q superseded=%q", oldPlaceStaleSince, oldPlaceSupersededAt)
	}

	opened, err := Open(ctx, paths, assetID)
	if err != nil {
		t.Fatal(err)
	}
	if opened.Stale != nil {
		t.Fatalf("open returned stale banner for active card: %#v", opened.Stale)
	}
	if opened.Model.Summary != "New newcardterm synthetic card." {
		t.Fatalf("open summary = %q", opened.Model.Summary)
	}
	if opened.Mechanical.Venue == nil || opened.Mechanical.Venue.Name != "New Synthetic Pier" {
		t.Fatalf("open venue = %#v", opened.Mechanical.Venue)
	}
	oldSearch, err := Search(ctx, paths, SearchOptions{Query: "oldcardterm", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if oldSearch.TotalMatches != 0 {
		t.Fatalf("superseded old card search = %#v", oldSearch.Results)
	}
	newSearch, err := Search(ctx, paths, SearchOptions{Query: "newcardterm", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(newSearch.Results) != 1 || newSearch.Results[0].Stale {
		t.Fatalf("new card search = %#v", newSearch.Results)
	}
}

func recardModelResult(summary, description string, plausibility venuePlausibility) modelResult {
	return modelResult{
		VenuePlausibility: plausibility,
		Observations: []contentObservation{
			{
				ObservationType: modelObservationCardSummary,
				ValueText:       summary,
				Value:           map[string]any{"text": summary},
			},
			{
				ObservationType: modelObservationCardDescription,
				ValueText:       description,
				Value:           map[string]any{"text": description},
			},
		},
	}
}

func insertRecardGeneration(ctx context.Context, tx *sql.Tx, assetID, label string) (string, error) {
	generationID := stableID("model_generation", "recard", label)
	if _, err := tx.ExecContext(ctx, `
insert into model_generation(id, request_sha256, request_route, model_id, request_body, created_at)
values (?, ?, 'https://models.example.com/api/generate', 'fixture-vision', '{}', '2026-05-28T10:00:00Z')
`, generationID, stableID("request", "recard", label)); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `
insert into model_generation_asset(generation_id, asset_id, prompt_version, parser_version)
values (?, ?, ?, ?)
`, generationID, assetID, modelPromptVersion, modelParserVersion); err != nil {
		return "", err
	}
	return generationID, nil
}

func recardPlaceContext(name string) *classifyPlaceContext {
	return &classifyPlaceContext{
		CacheStatus: "hit",
		Result: place.Result{
			Provider:  "apple",
			Source:    "fixture",
			POIStatus: place.POIStatusFound,
			POICandidates: []place.POICandidate{{
				Name:      name,
				Category:  "pier",
				DistanceM: 12,
				Tier:      place.TierVenueCandidate,
				Source:    "fixture",
			}},
		},
	}
}

func recardPreparedRequest(name string) preparedCardRequest {
	candidate := preparedPlaceCandidate{ID: "place_1_candidate_1", Provider: "synthetic", ProviderIndex: 0, ProviderID: "provider:" + name, Name: name, DistanceMeters: 4, Source: "synthetic", PlacePosition: 1, CandidatePosition: 1}
	return preparedCardRequest{
		Input: cardinput.Result{Input: &cardwire.CardInput{Places: []*cardwire.PlaceProjection{{
			ProviderIdentity: "synthetic", Candidates: []*cardwire.PlaceCandidate{{CandidateId: candidate.ID, ProviderId: candidate.ProviderID, Name: candidate.Name}},
		}}}},
		CandidateByID: map[string]preparedPlaceCandidate{candidate.ID: candidate}, CandidatesInSeq: []preparedPlaceCandidate{candidate},
	}
}

func TestClassifyDownloadsOriginalThroughPersistentBoundedCache(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	oldExport := exportOriginalResource
	exportedImageBytes := syntheticImageBytes(t)
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destinationPath string, allowNetwork bool) error {
		if query.LocalIdentifier != "remote-original-asset" || query.OriginalFilename != "synthetic-menu.jpeg" || !allowNetwork {
			t.Fatalf("export args = %#v %t", query, allowNetwork)
		}
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return err
		}
		return os.WriteFile(destinationPath, exportedImageBytes, 0o600)
	}
	defer func() { exportOriginalResource = oldExport }()

	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(fixtureToolResponse(fixtureCardResponse(
			"A synthetic test photo shows a printed menu on a table.",
			"The image shows a printed menu lying on a table in a synthetic fixture. The word menu is visible and is the main readable text. No people are visible.",
			"Restaurant table candidate.",
			"The word menu is visible.",
			"synthetic image bytes",
		)))
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
				// iCloud-only assets report no byte size; downloads must not
				// depend on knowing the size up front.
				FileSize:      0,
				NeedsDownload: true,
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
	logs := &recordingClassifyLogSink{}
	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		LogSink:  logs,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 1 || result.CardInputNotReady != 1 || result.PhotoKitExports != 0 || result.OriginalResolutionFailures != 0 || result.WaitingForLocalContent != 0 {
		t.Fatalf("classify result = %#v", result)
	}
	assertContentOutcomesSumToProcessed(t, result)
	if files := countOriginalCacheMedia(t, paths.OriginalsCacheDir()); files != 0 {
		t.Fatalf("originals cache files after classify = %d", files)
	}
}

func TestClassifyReleasesOriginalLeaseWhenMetadataExtractionFails(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	originalBytes := syntheticImageBytes(t)
	oldExport := exportOriginalResource
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destinationPath string, allowNetwork bool) error {
		if query.LocalIdentifier != "metadata-extractor-failure" || !allowNetwork {
			t.Fatalf("export args = %#v %t", query, allowNetwork)
		}
		return os.WriteFile(destinationPath, originalBytes, 0o600)
	}
	t.Cleanup(func() { exportOriginalResource = oldExport })

	oldExtract := extractImageMetadata
	extractImageMetadata = func(context.Context, string) ([]byte, error) {
		return nil, errors.New("synthetic ImageIO failure")
	}
	t.Cleanup(func() { extractImageMetadata = oldExtract })

	const modificationDate = "2026-05-27T12:01:00Z"
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{{
			LocalIdentifier:  "metadata-extractor-failure",
			MediaType:        "image",
			MediaSubtypes:    "0",
			CreationDate:     "2026-05-27T12:00:00Z",
			ModificationDate: modificationDate,
			Width:            100,
			Height:           80,
			Resources: []photos.Resource{{
				Type:             "photo",
				UTI:              "public.jpeg",
				OriginalFilename: "metadata-extractor-failure.jpeg",
				Availability:     "remote",
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
	logs := &recordingClassifyLogSink{}
	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		LogSink:  logs,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CardInputNotReady != 1 || result.ContentFailedModel != 0 || result.PhotoKitExports != 0 || result.Processed != 1 {
		t.Fatalf("classify result = %#v", result)
	}
}

func TestClassifyContentOutcomesSumToProcessed(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	parseImagePath := filepath.Join(t.TempDir(), "parse.jpeg")
	writeSyntheticImage(t, parseImagePath)
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
					Type:             "local_original",
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
	prepareCheckedCardInputForModelTest(t, ctx, paths, libraryPath, "parse-fails")
	logs := &recordingClassifyLogSink{}
	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
		LogSink:  logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 5 ||
		result.ContentFailedParse != 1 ||
		result.CardInputNotReady != 3 ||
		result.ContentFailedDownload != 0 ||
		result.ContentNotInPhotoKit != 0 ||
		result.ContentNoContentAvailable != 0 ||
		result.ContentSkippedUnsupportedMedia != 1 {
		t.Fatalf("classify result = %#v", result)
	}
	assertContentOutcomesSumToProcessed(t, result)
	if result.ContentClassificationFailures != 1 || result.OriginalResolutionFailures != 0 || result.WaitingForLocalContent != 0 {
		t.Fatalf("aggregate counters = %#v", result)
	}
	assertRecordedLogEvent(t, logs, "failed_parse")

	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
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
	if nonTerminal != 4 {
		t.Fatalf("non-terminal classified rows = %d", nonTerminal)
	}
	states := map[string]string{}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
select a.local_identifier, q.state
from classification_queue q
join asset a on a.id = q.asset_id
`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var localIdentifier, state string
			if err := rows.Scan(&localIdentifier, &state); err != nil {
				return err
			}
			states[localIdentifier] = state
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	wantStates := map[string]string{
		"parse-fails":     "metadata_classified",
		"not-in-photokit": "pending",
		"download-fails":  "pending",
		"no-content":      "pending",
		"video-skipped":   "content_skipped",
	}
	if !reflect.DeepEqual(states, wantStates) {
		t.Fatalf("queue states = %#v, want %#v", states, wantStates)
	}

	second, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:30:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Processed != 4 || second.ContentFailedParse != 1 || second.CardInputNotReady != 3 || second.ContentFailedDownload != 0 || second.OriginalResolutionFailures != 0 || second.ModelCallAttempts != 0 {
		t.Fatalf("failed download did not resume on the next run: %#v", second)
	}
	assertContentOutcomesSumToProcessed(t, second)
}

func TestClassifyRetriesFailedDownloadOnNextRun(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	var exportCalls atomic.Int32
	oldExport := exportOriginalResource
	exportOriginalResource = func(_ context.Context, query photos.OriginalExportQuery, destinationPath string, allowNetwork bool) error {
		exportCalls.Add(1)
		if query.LocalIdentifier != "permanent-download-failure" || !allowNetwork {
			t.Fatalf("export args = %#v %t destination=%q", query, allowNetwork, destinationPath)
		}
		return errors.New("synthetic permanent download failure")
	}
	defer func() { exportOriginalResource = oldExport }()

	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{
			remoteFixtureAsset("permanent-download-failure", "2026-05-29T12:00:00Z"),
		},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	first, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Processed != 1 || first.CardInputNotReady != 1 || first.ContentFailedDownload != 0 || first.OriginalResolutionFailures != 0 {
		t.Fatalf("first classify result = %#v", first)
	}
	assertContentOutcomesSumToProcessed(t, first)
	if calls := exportCalls.Load(); calls != 0 {
		t.Fatalf("first export attempts = %d, want 0", calls)
	}

	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:20:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	second, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:25:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Processed != 1 || second.CardInputNotReady != 1 || second.ContentFailedDownload != 0 || second.OriginalResolutionFailures != 0 {
		t.Fatalf("failed download did not resume: %#v", second)
	}
	if calls := exportCalls.Load(); calls != 0 {
		t.Fatalf("export attempts after resume = %d, want 0", calls)
	}
	assertQueueState(t, ctx, paths, "permanent-download-failure", "pending")
}

func TestClassifyLogsFailedDownloadToTrawlkitRun(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	oldExport := exportOriginalResource
	exportOriginalResource = func(context.Context, photos.OriginalExportQuery, string, bool) error {
		return photos.NewPhotoKitExportError("PHPhotosErrorDomain", 3303, "")
	}
	defer func() { exportOriginalResource = oldExport }()

	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{
			remoteFixtureAsset("trawlkit-log-download-fails", "2026-05-29T12:00:00Z"),
		},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}

	stateRoot := t.TempDir()
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: stateRoot,
		CrawlerID: "photos",
		RunID:     "synthetic-log-run",
		Command:   "classify",
		Version:   "test",
		Platform:  "test",
		Now:       fixedClock("2026-05-28T10:15:00Z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
		LogSink:  run,
	})
	if finishErr := run.Finish(err); err == nil && finishErr != nil {
		err = finishErr
	}
	if err != nil {
		t.Fatal(err)
	}
	if result.CardInputNotReady != 1 || result.ContentFailedDownload != 0 || result.OriginalResolutionFailures != 0 {
		t.Fatalf("classify result = %#v", result)
	}

	reader, err := cklog.NewReader(stateRoot, "photos")
	if err != nil {
		t.Fatal(err)
	}
	lines, err := reader.RecentLines("synthetic-log-run", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) == 0 {
		t.Fatal("run log is empty")
	}
}

func TestClassifyModelRateLimitSendsOnceAndRestartDoesNotSend(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	imagePath := filepath.Join(t.TempDir(), "fixture.jpeg")
	writeSyntheticImage(t, imagePath)
	var calls atomic.Int32
	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "slow down", http.StatusTooManyRequests)
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
				Type:             "local_original",
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
	prepareCheckedCardInputForModelTest(t, ctx, paths, libraryPath, "retry-asset")
	logs := &recordingClassifyLogSink{}
	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
		LogSink:  logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContentClassified != 0 || result.ModelCallAttempts != 1 || result.ModelRateLimitEvents != 1 || result.RateLimitRequeued != 1 || result.ContentClassificationFailures != 0 {
		t.Fatalf("classify result = %#v", result)
	}
	if calls.Load() != 1 {
		t.Fatalf("provider calls = %d, want 1", calls.Load())
	}
	restarted, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:20:00Z"),
		LogSink:  logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || restarted.ModelCallAttempts != 0 || restarted.RateLimitRequeued != 1 {
		t.Fatalf("restart calls = %d, result = %#v", calls.Load(), restarted)
	}
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var attempts, status int
	var raw []byte
	if err := db.DB().QueryRowContext(ctx, `
select count(*), max(http_status), max(response_body)
from model_generation_attempt
`).Scan(&attempts, &status, &raw); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || status != http.StatusTooManyRequests || string(raw) != "slow down\n" {
		t.Fatalf("retained rate limit: attempts=%d status=%d raw=%q", attempts, status, raw)
	}
}

func assertContentOutcomesSumToProcessed(t *testing.T, result ClassifyResult) {
	t.Helper()
	sum := result.ContentClassified +
		result.ContentFailedParse +
		result.ContentFailedModel +
		result.ContentStoppedUncertain +
		result.ContentFailedDownload +
		result.ContentNotInPhotoKit +
		result.ContentNoContentAvailable +
		result.ContentSkippedUnsupportedMedia +
		result.CardInputNotReady
	if sum != result.Processed {
		t.Fatalf("content outcomes sum = %d, processed = %d, result = %#v", sum, result.Processed, result)
	}
	if result.ContentOutcomeTotal != result.Processed {
		t.Fatalf("content_outcome_total = %d, processed = %d, result = %#v", result.ContentOutcomeTotal, result.Processed, result)
	}
}

func TestClassifyPendingAndPlacePendingStayByteForByteReadOnly(t *testing.T) {
	ctx := context.Background()
	db, _ := openModelGenerationTestStore(t)
	if _, err := db.DB().ExecContext(ctx, `delete from classification_queue where id = 'queue:synthetic'; delete from asset where id = 'asset:synthetic'`); err != nil {
		t.Fatal(err)
	}
	insertModelGenerationTestAsset(t, db, "asset:not-ready-pending", "queue:not-ready-pending", "not-ready-pending")
	insertModelGenerationTestAsset(t, db, "asset:not-ready-place", "queue:not-ready-place", "not-ready-place")
	if _, err := db.DB().ExecContext(ctx, `update classification_queue set state = 'place_pending', reason = 'synthetic checked place wait', needs_download = 1, updated_at = '2026-07-13T09:10:11.123456789Z' where id = 'queue:not-ready-place'`); err != nil {
		t.Fatal(err)
	}
	before := classifyQueueRawRows(t, db, "queue:not-ready-pending", "queue:not-ready-place")
	result, err := ClassifyWithStore(ctx, db, Paths{DataDir: t.TempDir(), CacheDir: t.TempDir()}, ClassifyOptions{Limit: 2, Model: "fixture-vision", ModelURL: fixtureModelURL, Now: fixedClock("2026-07-13T10:00:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Processed != 2 || result.CardInputNotReady != 2 || result.ContentOutcomeTotal != 2 || result.ModelRunID != "" {
		t.Fatalf("classify result = %#v", result)
	}
	after := classifyQueueRawRows(t, db, "queue:not-ready-pending", "queue:not-ready-place")
	if !bytes.Equal(before, after) {
		t.Fatalf("queue bytes changed\nbefore=%s\nafter=%s", before, after)
	}
	jsonOutput, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(jsonOutput, []byte(`"card_input_not_ready":2`)) {
		t.Fatalf("raw JSON = %s", jsonOutput)
	}
	if human := result.String(); !strings.Contains(human, "Card input not ready: 2") {
		t.Fatalf("human output = %q", human)
	}
	for _, table := range []string{"model_run", "model_generation", "model_generation_attempt", "model_observation", "place_observation", "paid_call_stage", "paid_call_claim", "card_execution"} {
		var count int
		if err := db.DB().QueryRowContext(ctx, "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("forbidden %s rows = %d", table, count)
		}
	}
}

func classifyQueueRawRows(t *testing.T, db *store.Store, ids ...string) []byte {
	t.Helper()
	rows, err := db.DB().Query(`select quote(id), quote(asset_id), quote(source_library_id), quote(state), quote(reason), quote(needs_download), quote(updated_at) from classification_queue where id in (?, ?) order by id`, ids[0], ids[1])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out bytes.Buffer
	for rows.Next() {
		var values [7]string
		if err := rows.Scan(&values[0], &values[1], &values[2], &values[3], &values[4], &values[5], &values[6]); err != nil {
			t.Fatal(err)
		}
		out.WriteString(strings.Join(values[:], "|"))
		out.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type recordedClassifyLogEvent struct {
	event   string
	message string
}

type recordingClassifyLogSink struct {
	mu     sync.Mutex
	events []recordedClassifyLogEvent
}

func (sink *recordingClassifyLogSink) Info(event, message string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, recordedClassifyLogEvent{event: event, message: message})
	return nil
}

func (sink *recordingClassifyLogSink) Warn(event, message string) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, recordedClassifyLogEvent{event: event, message: message})
	return nil
}

func assertRecordedLogEvent(t *testing.T, sink *recordingClassifyLogSink, event string) {
	t.Helper()
	for _, got := range sink.events {
		if got.event == event && strings.Contains(got.message, "asset_ref=photos:asset/") && strings.Contains(got.message, "reason=") {
			return
		}
	}
	t.Fatalf("missing log event %q in %#v", event, sink.events)
}

func assertQueueState(t *testing.T, ctx context.Context, paths Paths, localIdentifier, want string) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var got string
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `
select q.state
from classification_queue q
join asset a on a.id = q.asset_id
where a.local_identifier = ?
`, localIdentifier).Scan(&got)
	}); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("queue state for %s = %q, want %q", localIdentifier, got, want)
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
	defer func() { _ = db.Close() }()
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
	// Order is date desc, then GPS presence, then queue id. The old
	// receipt/menu filename boost was deleted: it cost group_concats over
	// every queued row per batch and only ever broke same-second ties.
	// The final tie-break (queue id) hashes the library path, which is a
	// temp dir here — so the last two same-second GPS-less fixtures have
	// no defined relative order and are asserted as a set.
	if want := []string{"newest-plain", "middle-gps", "same-date-gps"}; !reflect.DeepEqual(got[:3], want) {
		t.Fatalf("priority order = %#v, want prefix %#v", got, want)
	}
	tail := map[string]bool{got[3]: true, got[4]: true}
	if !tail["same-date-plain"] || !tail["same-date-menu"] {
		t.Fatalf("priority order tail = %#v", got[3:])
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

func TestWritePlaceClassificationAppliesVenuePlausibilityAndAddressLine(t *testing.T) {
	for _, tc := range []struct {
		name      string
		id        string
		verdict   string
		wantVenue string
		wantTier  string
	}{
		{
			name:    "inconsistent",
			id:      "venue_candidate_1",
			verdict: venueVerdictInconsistent,
		},
		{
			name:      "plausible",
			id:        "venue_candidate_1",
			verdict:   venueVerdictPlausible,
			wantVenue: "Synthetic Consultancy",
			wantTier:  place.TierVenueCandidate,
		},
		{
			name:      "corroborated",
			id:        "venue_candidate_1",
			verdict:   venueVerdictCorroborated,
			wantVenue: "Synthetic Consultancy",
			wantTier:  place.TierConfirmedVenue,
		},
		{
			name:    "unknown_id",
			id:      "venue_candidate_2",
			verdict: venueVerdictCorroborated,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := openSyntheticPlaceResult(t, venuePlausibility{
				CandidateID: tc.id,
				Verdict:     tc.verdict,
				Reason:      "synthetic fixture reason",
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
		})
	}
}

func TestOpenVenueCandidatesCapsRoundsAndFlattens(t *testing.T) {
	rows := []map[string]any{
		openCandidateRow("Nearby 1", "nearby_poi", 1.2, "MKPOICategoryStore", ""),
		openCandidateRow("Nearby 2", "nearby_poi", 2.6, "MKPOICategoryStore", ""),
		openCandidateRow("Nearby 3", "nearby_poi", 3.4, "MKPOICategoryStore", ""),
		openCandidateRow("Nearby 4", "nearby_poi", 4.4, "MKPOICategoryStore", ""),
		openCandidateRow("Nearby 5", "nearby_poi", 5.4, "MKPOICategoryStore", ""),
		openCandidateRow("Nearby 6", "nearby_poi", 6.4, "MKPOICategoryStore", ""),
		openCandidateRow("Synthetic Gym", "venue_candidate", 80.7, "MKPOICategoryFitnessCenter", venueVerdictPlausible),
	}
	candidates := openVenueCandidates(rows)
	if len(candidates) != 5 {
		t.Fatalf("candidates = %#v, want 5", candidates)
	}
	if candidates[0].Name != "Nearby 1" || candidates[0].DistanceMeters != 1 || candidates[0].Category != "shop" {
		t.Fatalf("first candidate = %#v", candidates[0])
	}
	last := candidates[len(candidates)-1]
	if last.Name != "Synthetic Gym" || last.Tier != place.TierVenueCandidate || last.DistanceMeters != 81 || last.Category != "fitness centre" {
		t.Fatalf("included venue candidate = %#v", last)
	}
	data, err := json.Marshal(candidates)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"source"`, "venue_plausibility", `"candidate"`, "MKPOICategory"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("candidate JSON leaked %q: %s", forbidden, data)
		}
	}
}

func TestTopPOICandidatesStayInsideHumanOpenPresentation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var input classifyInput
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		inputs, err := loadClassifyInputs(ctx, tx, 0, "")
		if err != nil {
			return err
		}
		if len(inputs) != 1 {
			return fmt.Errorf("classify inputs = %d, want 1", len(inputs))
		}
		input = inputs[0]
		input.Place = &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				Provider:     "apple",
				Source:       "fixture",
				RadiusMeters: 150,
				POIStatus:    place.POIStatusFound,
				POICandidates: []place.POICandidate{
					{Name: "Nearby Bakery", Category: "MKPOICategoryStore", DistanceM: 4.2, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Nearby Bakery", Category: "MKPOICategoryStore", DistanceM: 4.6, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Corner Market", Category: "MKPOICategoryStore", DistanceM: 9.1, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Canal Pharmacy", Category: "pharmacy", DistanceM: 16.4, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Station Salon", Category: "personal care", DistanceM: 21.2, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Fysiochi", Category: "MKPOICategoryFitnessCenter", DistanceM: 33.3, Tier: place.TierNearbyPOI, Source: "fixture"},
					{Name: "Book Casino", Category: "entertainment", DistanceM: 42.2, Tier: place.TierVenueCandidate, Source: "fixture"},
					{Name: "Distant Cafe", Category: "cafe", DistanceM: 80.2, Tier: place.TierNearbyPOI, Source: "fixture"},
				},
			},
		}
		_, err = writePlaceClassification(ctx, tx, input, venuePlausibility{
			CandidateID: "venue_candidate_5",
			Verdict:     venueVerdictPlausible,
			Reason:      "synthetic fixture reason",
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	storedRows, err := rows(ctx, db.DB(), `
select observation_type, value_text, value_json, tier, distance_meters
from place_observation
where asset_id = ? and observation_type = 'poi_candidate'
order by case when distance_meters is null then 1 else 0 end, distance_meters, value_text
`, input.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	stored := storedCandidateSnapshots(t, storedRows)

	opened, err := Open(ctx, paths, input.AssetID)
	if err != nil {
		t.Fatal(err)
	}
	shown := openCandidateSnapshots(opened.Mechanical.VenueCandidates)

	want := []candidateSnapshot{
		{CandidateID: "venue_candidate_1", Name: "Nearby Bakery", Category: "shop", Tier: place.TierNearbyPOI, DistanceMeters: 4},
		{CandidateID: "venue_candidate_2", Name: "Corner Market", Category: "shop", Tier: place.TierNearbyPOI, DistanceMeters: 9},
		{CandidateID: "venue_candidate_3", Name: "Canal Pharmacy", Category: "pharmacy", Tier: place.TierNearbyPOI, DistanceMeters: 16},
		{CandidateID: "venue_candidate_4", Name: "Station Salon", Category: "personal care", Tier: place.TierNearbyPOI, DistanceMeters: 21},
		{CandidateID: "venue_candidate_5", Name: "Book Casino", Category: "entertainment", Tier: place.TierVenueCandidate, DistanceMeters: 42},
	}
	if !reflect.DeepEqual(stored, want) {
		t.Fatalf("stored candidates = %#v, want %#v", stored, want)
	}
	if !reflect.DeepEqual(shown, want) {
		t.Fatalf("open candidates = %#v, want %#v", shown, want)
	}
}

type candidateSnapshot struct {
	CandidateID    string
	Name           string
	Category       string
	Tier           string
	DistanceMeters float64
}

func storedCandidateSnapshots(t *testing.T, rows []map[string]any) []candidateSnapshot {
	t.Helper()
	out := make([]candidateSnapshot, 0, len(rows))
	for i, row := range rows {
		var value map[string]any
		if err := json.Unmarshal([]byte(rowString(row, "value_json")), &value); err != nil {
			t.Fatal(err)
		}
		distance := rowFloat(row, "distance_meters")
		if valueDistance := mapFloat(value, "distance_m"); valueDistance > 0 {
			distance = valueDistance
		}
		out = append(out, candidateSnapshot{
			CandidateID:    venueCandidateID(i),
			Name:           rowString(row, "value_text"),
			Category:       mapText(value, "category"),
			Tier:           rowString(row, "tier"),
			DistanceMeters: cardformat.Meters(distance),
		})
	}
	return out
}

func openCandidateSnapshots(candidates []OpenVenueCandidate) []candidateSnapshot {
	out := make([]candidateSnapshot, 0, len(candidates))
	for i, candidate := range candidates {
		out = append(out, candidateSnapshot{
			CandidateID:    venueCandidateID(i),
			Name:           candidate.Name,
			Category:       candidate.Category,
			Tier:           candidate.Tier,
			DistanceMeters: candidate.DistanceMeters,
		})
	}
	return out
}

func openCandidateRow(name, tier string, distance float64, category, verdict string) map[string]any {
	value := map[string]any{
		"category":   category,
		"distance_m": distance,
		"source":     "apple",
	}
	if verdict != "" {
		value["venue_plausibility"] = map[string]any{
			"candidate_id": "venue_candidate_1",
			"verdict":      verdict,
			"reason":       "synthetic fixture reason",
		}
	}
	data, _ := json.Marshal(value)
	return map[string]any{
		"observation_type": "poi_candidate",
		"value_text":       name,
		"value_json":       string(data),
		"provider":         "apple",
		"tier":             tier,
		"distance_meters":  distance,
	}
}

func fixtureCardResponse(summary, description, locationText, visibleText, uncertainty string) string {
	location := map[string]string{"kind": locationNone, "candidate_id": "", "inferred_name": "", "confidence": "none", "reason": "no useful location"}
	for _, line := range strings.Split(locationText, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "candidate_id":
			location["candidate_id"] = strings.TrimSpace(value)
			if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "none" {
				location["kind"] = locationCandidate
				location["confidence"] = "medium"
			}
		case "reason":
			location["reason"] = strings.TrimSpace(value)
		}
	}
	if strings.TrimSpace(locationText) != "" && location["reason"] == "no useful location" {
		location["reason"] = strings.TrimSpace(locationText)
	}
	if strings.EqualFold(strings.TrimSpace(visibleText), "none") {
		visibleText = ""
	}
	uncertainties := []string{}
	if text := strings.TrimSpace(uncertainty); text != "" && !strings.EqualFold(text, "none") {
		uncertainties = append(uncertainties, text)
	}
	arguments, _ := json.Marshal(map[string]any{
		"summary": summary, "description": description, "location": location,
		"visible_text": visibleText, "uncertainties": uncertainties,
	})
	return string(arguments)
}

func fixtureToolResponse(arguments string) map[string]any {
	return map[string]any{"choices": []map[string]any{{"message": map[string]any{
		"tool_calls": []map[string]any{{"type": "function", "function": map[string]any{
			"name": photoCardToolName, "arguments": arguments,
		}}},
	}}}}
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
	defer func() { _ = db.Close() }()
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
		_, err = writePlaceClassification(ctx, tx, input, plausibility)
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

func countOriginalCacheMedia(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".proof.json") {
			continue
		}
		count++
	}
	return count
}

// Tripwire: capture times render in the asset's own timezone, never the
// machine's. Apple stores fixed offsets as "GMT-0700"-style names; treating
// them as absent silently shifted hundreds of cards onto the reviewing
// machine's clock (one crossed a calendar day).
func TestCaptureTimeUsesAssetTimezone(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value, timezone, want string
	}{
		{"2025-07-08T16:48:00Z", "GMT-0700", "2025-07-08T09:48:00-07:00"},
		{"2025-07-08T16:48:00Z", "GMT+0530", "2025-07-08T22:18:00+05:30"},
		{"2025-07-10T00:11:58Z", "", "2025-07-10T00:11:58Z"},
		{"2025-07-08T16:48:00Z", "not-a-zone", "2025-07-08T16:48:00Z"},
		{"2025-07-08T16:48:00Z", "GMT", "2025-07-08T16:48:00Z"},
	}
	for _, tc := range cases {
		if got := localCaptureTime(tc.value, tc.timezone); got != tc.want {
			t.Fatalf("localCaptureTime(%q, %q) = %q, want %q", tc.value, tc.timezone, got, tc.want)
		}
	}
	if loc, err := time.LoadLocation("America/Denver"); err == nil {
		want := time.Date(2025, 7, 8, 16, 48, 0, 0, time.UTC).In(loc).Format(time.RFC3339)
		if got := localCaptureTime("2025-07-08T16:48:00Z", "America/Denver"); got != want {
			t.Fatalf("named zone = %q, want %q", got, want)
		}
	}
	if displayTimezoneName("GMT-0700") != "GMT-0700" || displayTimezoneName("garbage") != "" {
		t.Fatal("displayTimezoneName should accept GMT offsets and reject garbage")
	}
}

// Exhausted quota must never convert queue items into failures: every 429'd
// asset goes back to metadata_classified, and a run of consecutive refusals
// stops the batch instead of churning through it.
func TestClassifyQuotaExhaustionRequeuesAndAborts(t *testing.T) {
	withSyntheticCurrentStill(t)
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exhausted", http.StatusTooManyRequests)
	}))
	defer restoreTransport()

	assets := make([]photos.Asset, 0, 12)
	for i := 0; i < 12; i++ {
		imagePath := filepath.Join(t.TempDir(), fmt.Sprintf("fixture-%d.jpeg", i))
		writeSyntheticImage(t, imagePath)
		assets = append(assets, photos.Asset{
			LocalIdentifier: fmt.Sprintf("quota-asset-%d", i),
			MediaType:       "image",
			MediaSubtypes:   "0",
			CreationDate:    "2026-05-27T12:00:00Z",
			Width:           100,
			Height:          80,
			Resources: []photos.Resource{{
				Type:             "local_original",
				UTI:              "public.jpeg",
				OriginalFilename: fmt.Sprintf("fixture-%d.jpeg", i),
				LocalPath:        imagePath,
				Availability:     "local",
				AvailableLocally: true,
			}},
		})
	}
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets:              assets,
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		prepareCheckedCardInputForModelTest(t, ctx, paths, libraryPath, fmt.Sprintf("quota-asset-%d", i))
	}
	result, err := Classify(ctx, paths, ClassifyOptions{
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-05-28T10:15:00Z"),
		LogSink:  &recordingClassifyLogSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RateLimitAborted {
		t.Fatalf("expected rate limit abort, result = %+v", result)
	}
	// trawlkit model.Run aborts after 8 consecutive quota refusals.
	if result.RateLimitRequeued < 8 {
		t.Fatalf("rate limit requeued = %d, want >= 8", result.RateLimitRequeued)
	}
	if result.ContentFailedModel != 0 || result.ContentClassificationFailures != 0 {
		t.Fatalf("quota refusals recorded as failures: %+v", result)
	}

	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var failed, queued int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from classification_queue where state = 'content_failed'`).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	// Items the abort never reached keep their pre-run state; 429'd items
	// return to metadata_classified. Nothing may land in a failed state.
	if err := db.DB().QueryRowContext(ctx, `select count(*) from classification_queue where state in ('pending', 'metadata_classified')`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if failed != 0 || queued != 12 {
		t.Fatalf("queue after quota exhaustion: failed=%d retryable=%d, want failed=0 retryable=12", failed, queued)
	}
}

func writeSyntheticImage(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, syntheticImageBytes(t), 0o600); err != nil {
		t.Fatal(err)
	}
}

func prepareCheckedCardInputForModelTest(t *testing.T, ctx context.Context, paths Paths, libraryPath, localIdentifier string) {
	t.Helper()
	sourceID := stableID("source_library", libraryPath)
	assetID := stableID("asset", sourceID, localIdentifier)
	db := openTestStore(t, ctx, paths)
	defer func() { _ = db.Close() }()
	input, err := loadCardInputAuditInput(ctx, db.DB(), sourceID, assetID)
	if err != nil {
		t.Fatal(err)
	}
	request, err := input.currentStillRequest()
	if err != nil {
		t.Fatal(err)
	}
	currentBytes := syntheticImageBytes(t)
	resolver, err := photos.NewCurrentStillResolver(paths.OriginalsCacheDir(), func(_ context.Context, _ photos.CurrentStillRequest, destination string) (photos.CurrentStillFact, error) {
		if err := os.WriteFile(destination, currentBytes, 0o600); err != nil {
			return photos.CurrentStillFact{}, err
		}
		sum := sha256.Sum256(currentBytes)
		return photos.CurrentStillFact{MediaType: "public.jpeg", Orientation: 1, PixelWidth: input.Width, PixelHeight: input.Height, Size: int64(len(currentBytes)), SHA256: hex.EncodeToString(sum[:])}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolver.Resolve(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Lease != nil {
		resolved.Lease.Close()
	}
	original, originalPath, _, ok, err := cardInputAuditCheckedOriginal(input, paths.OriginalsCacheDir())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("asset %s has no checked original", localIdentifier)
	}
	metadataStore, err := imagemetadata.NewStore(filepath.Join(paths.CacheDir, "image-metadata"), extractImageMetadata)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := metadataStore.Load(ctx, originalPath, original.SHA256); err != nil {
		t.Fatal(err)
	}
}

func syntheticImageBytes(t *testing.T) []byte {
	t.Helper()
	return syntheticImageBytesWithAccent(t, color.NRGBA{R: 240, G: 192, B: 48, A: 255})
}

func syntheticAlternateImageBytes(t *testing.T) []byte {
	t.Helper()
	return syntheticImageBytesWithAccent(t, color.NRGBA{R: 72, G: 184, B: 120, A: 255})
}

func syntheticImageBytesWithAccent(t *testing.T, accent color.NRGBA) []byte {
	t.Helper()
	fixture := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	fixture.Set(0, 0, color.NRGBA{R: 24, G: 48, B: 96, A: 255})
	fixture.Set(1, 1, accent)
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, fixture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
