package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardinput"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	cardwire "github.com/opentrawl/opentrawl/trawlers/photos/proto/opentrawl/photos/card/v1"
	"github.com/opentrawl/opentrawl/trawlkit/model"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestModelGenerationPersistsExactRequestAndRestartDecisions(t *testing.T) {
	ctx := context.Background()
	db, assetID := openModelGenerationTestStore(t)
	client, err := model.New(model.Config{BaseURL: "https://models.example.com/api", Model: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.Render(model.Request{
		Prompt:      "describe the synthetic image",
		Images:      []model.Image{{Data: []byte("synthetic-image-bytes"), MIMEType: "image/jpeg"}},
		Temperature: 0.1,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 11, 8, 0, 0, 0, time.UTC)
	first, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Call.Retained != nil || first.Call.Reused {
		t.Fatalf("first decision = %#v", first)
	}

	second, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now.Add(time.Second))
	if !errors.Is(err, errModelGenerationUncertain) || second.GenerationID != first.GenerationID {
		t.Fatalf("uncertain restart = %#v, %v", second, err)
	}
	assertGenerationCounts(t, db, first.GenerationID, 1, 1)

	rawResponse := model.RawResult{
		Response:            []byte(`{"response":"synthetic retained card","done":true}`),
		Status:              "200 OK",
		StatusCode:          200,
		ProviderRequestID:   "request-synthetic-card",
		TransmissionStarted: true,
	}
	if err := retainModelGenerationResult(ctx, db, first.GenerationID, rawResponse, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	resumed, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Call.Retained == nil || !rawResultsEqual(*resumed.Call.Retained, rawResponse) || resumed.Call.Reused {
		t.Fatalf("retained-response restart = %#v", resumed)
	}

	parseErr := errors.New("synthetic parser fixture rejected the card")
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return recordModelGenerationParseFailure(ctx, tx, first.GenerationID, assetID, parseErr, now.Add(4*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	var retainedParseFailure []byte
	if err := db.DB().QueryRowContext(ctx, `
select parse_failure from model_generation_asset where generation_id = ? and asset_id = ?
`, first.GenerationID, assetID).Scan(&retainedParseFailure); err != nil {
		t.Fatal(err)
	}
	if string(retainedParseFailure) != parseErr.Error() {
		t.Fatalf("parse failure = %q", retainedParseFailure)
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		return completeModelGeneration(ctx, tx, first.GenerationID, assetID, now.Add(5*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	reused, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now.Add(6*time.Second))
	if err != nil || !reused.Call.Reused || reused.Call.Retained != nil {
		t.Fatalf("completed restart = %#v, %v", reused, err)
	}

	changed, err := client.Render(model.Request{Prompt: "describe changed synthetic input"})
	if err != nil {
		t.Fatal(err)
	}
	changedDecision, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, changed, now.Add(7*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if changedDecision.GenerationID == first.GenerationID {
		t.Fatal("changed provider bytes reused the stable generation identity")
	}

	var route, modelID string
	var persistedBody, persistedResponse []byte
	if err := db.DB().QueryRowContext(ctx, `
select g.request_route, g.model_id, g.request_body, a.response_body
from model_generation g
join model_generation_attempt a on a.generation_id = g.id
where g.id = ?
`, first.GenerationID).Scan(&route, &modelID, &persistedBody, &persistedResponse); err != nil {
		t.Fatal(err)
	}
	if route != request.Route() || modelID != request.Model() || !bytes.Equal(persistedBody, request.Body()) || !bytes.Equal(persistedResponse, rawResponse.Response) {
		t.Fatalf("persisted boundary differs: route=%q model=%q body=%s response=%s", route, modelID, persistedBody, persistedResponse)
	}
	t.Logf("RAW final provider request: route=%s model=%s body=%s", request.Route(), request.Model(), request.Body())
	t.Logf("RAW persisted provider request: route=%s model=%s body=%s", route, modelID, persistedBody)
	t.Logf("RAW retained provider response: status=%s request_id=%s body=%s", rawResponse.Status, rawResponse.ProviderRequestID, persistedResponse)
	t.Log("RAW restart decisions: fresh=send attempt_without_result=stop_uncertain retained_response=resume_parse completed=reuse")
}

func TestModelGenerationRetainsFailureAndConcurrentWorkersClaimOnce(t *testing.T) {
	ctx := context.Background()
	db, assetID := openModelGenerationTestStore(t)
	client, err := model.New(model.Config{BaseURL: "https://models.example.com", Model: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 11, 9, 0, 0, 0, time.UTC)
	request, err := client.Render(model.Request{Prompt: "synthetic concurrent claim"})
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(chan error, 2)
	decisions := make(chan modelGenerationDecision, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			decision, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now)
			decisions <- decision
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	close(decisions)
	sends, stopped := 0, 0
	var generationID string
	for decision := range decisions {
		generationID = decision.GenerationID
	}
	for err := range results {
		switch {
		case err == nil:
			sends++
		case errors.Is(err, errModelGenerationUncertain):
			stopped++
		default:
			t.Fatalf("claim error = %v", err)
		}
	}
	if sends != 1 || stopped != 1 {
		t.Fatalf("concurrent decisions: sends=%d stopped=%d", sends, stopped)
	}
	assertGenerationCounts(t, db, generationID, 1, 1)

	rawFailure := model.RawResult{Failure: []byte("synthetic timeout after request write"), TransmissionStarted: true}
	if err := retainModelGenerationResult(ctx, db, generationID, rawFailure, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	restarted, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now.Add(2*time.Second))
	if err != nil || restarted.Call.Retained == nil || !rawResultsEqual(*restarted.Call.Retained, rawFailure) {
		t.Fatalf("failure restart = %#v, %v", restarted, err)
	}
	t.Logf("RAW retained provider failure: transmission_started=%t failure=%s", rawFailure.TransmissionStarted, rawFailure.Failure)
}

func TestModelGenerationSchemaHasRequestAttemptAndObservationProvenance(t *testing.T) {
	db, _ := openModelGenerationTestStore(t)
	ctx := context.Background()
	for table, columns := range map[string][]string{
		"model_generation":         {"request_sha256", "request_route", "model_id", "request_body"},
		"model_generation_asset":   {"generation_id", "asset_id", "prompt_version", "parser_version", "completed_at", "parse_failure"},
		"model_generation_attempt": {"generation_id", "response_body", "failure_body", "http_status", "provider_request_id", "transmission_started", "retained_at"},
		"model_observation":        {"generation_id"},
		"place_observation":        {"generation_id"},
	} {
		for _, column := range columns {
			exists, err := tableColumnExists(ctx, db.DB(), table, column)
			if err != nil {
				t.Fatal(err)
			}
			if !exists {
				t.Fatalf("missing schema column %s.%s", table, column)
			}
		}
	}
}

func TestModelGenerationMixedPlaceProvenanceIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, assetID := openModelGenerationTestStore(t)
	client, err := model.New(model.Config{BaseURL: "https://models.example.com", Model: "fixture-model"})
	if err != nil {
		t.Fatal(err)
	}
	request, err := client.Render(model.Request{Prompt: "synthetic duplicate parse"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	decision, err := prepareModelGeneration(ctx, db, assetID, modelPromptVersion, modelParserVersion, request, now)
	if err != nil {
		t.Fatal(err)
	}
	input := classifyInput{
		AssetID: assetID,
		QueueID: "queue:synthetic",
		Place: &classifyPlaceContext{
			CacheStatus: "hit",
			Result: place.Result{
				Provider: "synthetic",
				Address: &place.Address{
					Name:            "Synthetic Provider Avenue 42",
					SubThoroughfare: "42",
					Thoroughfare:    "Synthetic Provider Avenue",
					Locality:        "Synthetic Town",
					Country:         "Syntheticland",
					Formatted:       "Synthetic Provider Avenue 42, Synthetic Town, Syntheticland",
					Source:          "synthetic",
				},
				POIStatus: place.POIStatusFound,
				POICandidates: []place.POICandidate{
					{
						Name:      "Synthetic Provider Bakery",
						Category:  "bakery",
						DistanceM: 4,
						Tier:      place.TierNearbyPOI,
						Source:    "synthetic",
					},
					{
						Name:      "Synthetic Card Venue",
						Category:  "restaurant",
						DistanceM: 12,
						Tier:      place.TierVenueCandidate,
						Source:    "synthetic",
					},
				},
			},
		},
	}
	classifier := modelClassifier{modelID: "fixture-model", promptVersion: modelPromptVersion}
	preparedCandidates := []preparedPlaceCandidate{
		{ID: "place_1_candidate_1", Provider: "synthetic-provider", ProviderIndex: 0, Name: "Synthetic Provider Bakery", DistanceMeters: 4, Source: "synthetic", PlacePosition: 1, CandidatePosition: 1},
		{ID: "place_1_candidate_2", Provider: "synthetic-provider", ProviderIndex: 1, Name: "Synthetic Card Venue", DistanceMeters: 12, Source: "synthetic", PlacePosition: 1, CandidatePosition: 2},
	}
	prepared := preparedCardRequest{Image: imageMeta{Bytes: 17, SHA256: "synthetic-image"}, CandidateByID: map[string]preparedPlaceCandidate{}, CandidatesInSeq: preparedCandidates}
	prepared.Input.Input = &cardwire.CardInput{Places: []*cardwire.PlaceProjection{{
		ProviderIdentity: "synthetic-provider",
		Operation:        "synthetic-reverse",
		Address: &cardwire.Address{
			Formatted: "Synthetic Provider Avenue 42, Synthetic Town, Syntheticland",
			Source:    "synthetic-provider",
		},
	}}}
	for _, candidate := range preparedCandidates {
		prepared.CandidateByID[candidate.ID] = candidate
	}
	parsed, err := classifier.parseResult(model.Response{ToolCalls: []model.ToolCall{{
		Name: photoCardToolName,
		Arguments: []byte(fixtureCardResponse(
			"Synthetic duplicate summary.",
			"Synthetic duplicate description with a visible venue sign.",
			"candidate_id: place_1_candidate_2\nverdict: corroborated\nreason: synthetic sign matches the provider candidate.",
			"Synthetic card venue sign",
			"none",
		)),
	}}}, prepared)
	if err != nil {
		t.Fatal(err)
	}
	write := func(at time.Time) (int, int, error) {
		var observations, places int
		err := db.WithTx(ctx, func(tx *sql.Tx) error {
			var err error
			observations, places, err = writeModelClassification(ctx, tx, input, classifier, parsed, prepared, at, decision.GenerationID)
			if err != nil {
				return err
			}
			return completeModelGeneration(ctx, tx, decision.GenerationID, assetID, at)
		})
		return observations, places, err
	}
	observationsWritten, placesWritten, err := write(now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if observationsWritten != 4 || placesWritten != 3 {
		t.Fatalf("first parse wrote observations=%d places=%d", observationsWritten, placesWritten)
	}
	observationsWritten, placesWritten, err = write(now.Add(2 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if observationsWritten != 0 || placesWritten != 0 {
		t.Fatalf("duplicate parse wrote observations=%d places=%d", observationsWritten, placesWritten)
	}
	knownAssetID := "asset:synthetic-known-place"
	knownQueueID := "queue:synthetic-known-place"
	insertModelGenerationTestAsset(t, db, knownAssetID, knownQueueID, "synthetic-known-place")
	knownRequest, err := client.Render(model.Request{Prompt: "synthetic known-place provenance"})
	if err != nil {
		t.Fatal(err)
	}
	knownDecision, err := prepareModelGeneration(ctx, db, knownAssetID, modelPromptVersion, modelParserVersion, knownRequest, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := retainModelGenerationResult(ctx, db, knownDecision.GenerationID, model.RawResult{
		Response:            []byte(`{"response":"synthetic known-place card","done":true}`),
		Status:              "200 OK",
		StatusCode:          200,
		TransmissionStarted: true,
	}, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	knownInput := classifyInput{
		AssetID: knownAssetID,
		QueueID: knownQueueID,
		KnownPlace: &KnownPlaceMatch{
			Kind:           KnownPlaceKindWork,
			Name:           "Synthetic Local Workshop",
			DistanceMeters: 3,
		},
	}
	var knownObservations, knownPlaces int
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var err error
		knownPrepared := preparedCardRequest{
			Input:         cardinput.Result{Input: &cardwire.CardInput{KnownPlace: &cardwire.KnownPlace{Name: "Synthetic Local Workshop", Relationship: KnownPlaceKindWork}}},
			CandidateByID: map[string]preparedPlaceCandidate{}, CandidatesInSeq: []preparedPlaceCandidate{},
		}
		knownParsed := parsed
		knownParsed.VenuePlausibility.CandidateID = "none"
		knownObservations, knownPlaces, err = writeModelClassification(ctx, tx, knownInput, classifier, knownParsed, knownPrepared, now.Add(5*time.Second), knownDecision.GenerationID)
		if err != nil {
			return err
		}
		return completeModelGeneration(ctx, tx, knownDecision.GenerationID, knownAssetID, now.Add(5*time.Second))
	}); err != nil {
		t.Fatal(err)
	}
	if knownObservations != 4 || knownPlaces != 1 {
		t.Fatalf("known-place write observations=%d places=%d", knownObservations, knownPlaces)
	}

	var observations, linked int
	if err := db.DB().QueryRowContext(ctx, `
select count(*), count(generation_id)
from model_observation
where asset_id = ? and superseded_at is null
`, assetID).Scan(&observations, &linked); err != nil {
		t.Fatal(err)
	}
	if observations != 4 || linked != 4 {
		t.Fatalf("duplicate parse observations=%d linked=%d", observations, linked)
	}
	type storedPlaceObservation struct {
		AssetID       string  `json:"asset_id"`
		ID            string  `json:"id"`
		Type          string  `json:"type"`
		Text          string  `json:"text"`
		Tier          string  `json:"tier"`
		GenerationID  *string `json:"generation_id"`
		SupersededAt  string  `json:"superseded_at"`
		SearchFTSRows int     `json:"search_fts_rows"`
	}
	rows, err := db.DB().QueryContext(ctx, `
select p.asset_id, p.id, p.observation_type, p.value_text, p.tier, p.generation_id,
       coalesce(p.superseded_at, ''),
       (select count(*) from observation_fts f where f.id = p.id)
from place_observation p
where p.asset_id in (?, ?)
order by p.asset_id, p.observation_type, p.value_text
`, assetID, knownAssetID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	stored := []storedPlaceObservation{}
	for rows.Next() {
		var row storedPlaceObservation
		var generationID sql.NullString
		if err := rows.Scan(&row.AssetID, &row.ID, &row.Type, &row.Text, &row.Tier, &generationID, &row.SupersededAt, &row.SearchFTSRows); err != nil {
			t.Fatal(err)
		}
		if generationID.Valid {
			row.GenerationID = &generationID.String
		}
		stored = append(stored, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 4 {
		t.Fatalf("stored place observations = %#v", stored)
	}
	want := map[string]struct {
		generationID  string
		searchFTSRows int
	}{
		assetID + "|poi_candidate|Synthetic Provider Bakery":                             {generationID: "", searchFTSRows: 0},
		assetID + "|poi_candidate|Synthetic Card Venue":                                  {generationID: "", searchFTSRows: 0},
		assetID + "|address|Synthetic Provider Avenue 42, Synthetic Town, Syntheticland": {generationID: "", searchFTSRows: 1},
		knownAssetID + "|known_place|work — Synthetic Local Workshop":                    {generationID: "", searchFTSRows: 1},
	}
	for _, row := range stored {
		key := row.AssetID + "|" + row.Type + "|" + row.Text
		wantRow, ok := want[key]
		if !ok {
			t.Fatalf("unexpected place row = %#v", row)
		}
		if row.SupersededAt != "" || row.SearchFTSRows != wantRow.searchFTSRows {
			t.Fatalf("place storage state = %#v", row)
		}
		if wantRow.generationID == "" {
			if row.GenerationID != nil {
				t.Fatalf("provider place provenance = %#v", row)
			}
		} else if row.GenerationID == nil || *row.GenerationID != wantRow.generationID {
			t.Fatalf("place provenance = %#v", row)
		}
		delete(want, key)
	}
	if len(want) != 0 {
		t.Fatalf("missing place rows = %#v", want)
	}
	parserResult, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	storedRows, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW complete synthetic parser result:\n%s", parserResult)
	t.Logf("RAW complete mixed stored place observations and FTS rows:\n%s", storedRows)
}

func openModelGenerationTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	ctx := context.Background()
	paths := testPaths(t)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.DB().ExecContext(ctx, `
insert into source_library(id, library_path, snapshot_path, snapshot_created_at, photos_version, metadata_json)
values ('source:synthetic', '/tmp/Synthetic.photoslibrary', 'sqlite:crawl_snapshot/synthetic', '2026-07-11T08:00:00Z', 'fixture', '{}')
`); err != nil {
		t.Fatal(err)
	}
	assetID := "asset:synthetic"
	if _, err := db.DB().ExecContext(ctx, `
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values (?, 'synthetic-photo', 'image', '0', '2026-07-11T07:00:00Z', '2026-07-11T07:00:00Z',
  '2026-07-11T07:00:00Z', 'UTC', 2, 2, 0, 0, 0, '', 0, '', '', '', 'source:synthetic', '{}')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into classification_queue(id, asset_id, source_library_id, state, reason, needs_download, updated_at)
values ('queue:synthetic', ?, 'source:synthetic', 'pending', 'synthetic fixture', 0, '2026-07-11T08:00:00Z')
`, assetID); err != nil {
		t.Fatal(err)
	}
	return db, assetID
}

func insertModelGenerationTestAsset(t *testing.T, db *store.Store, assetID, queueID, localIdentifier string) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.DB().ExecContext(ctx, `
insert into asset(id, local_identifier, media_type, media_subtypes, creation_date, modification_date, added_date, timezone_name,
  width, height, duration_seconds, favorite, hidden, burst_identifier, represents_burst,
  camera_make, camera_model, lens_model, source_library_id, metadata_json)
values (?, ?, 'image', '0', '2026-07-11T07:00:00Z', '2026-07-11T07:00:00Z',
  '2026-07-11T07:00:00Z', 'UTC', 2, 2, 0, 0, 0, '', 0, '', '', '', 'source:synthetic', '{}')
`, assetID, localIdentifier); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into classification_queue(id, asset_id, source_library_id, state, reason, needs_download, updated_at)
values (?, ?, 'source:synthetic', 'pending', 'synthetic fixture', 0, '2026-07-11T08:00:00Z')
`, queueID, assetID); err != nil {
		t.Fatal(err)
	}
}

func assertGenerationCounts(t *testing.T, db *store.Store, generationID string, wantGenerations, wantAttempts int) {
	t.Helper()
	ctx := context.Background()
	var generations, attempts int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from model_generation where id = ?`, generationID).Scan(&generations); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `select count(*) from model_generation_attempt where generation_id = ?`, generationID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if generations != wantGenerations || attempts != wantAttempts {
		t.Fatalf("generation rows=%d attempts=%d, want %d and %d", generations, attempts, wantGenerations, wantAttempts)
	}
}
