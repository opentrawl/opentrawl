package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/place"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

func tableColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "pragma table_info("+store.QuoteIdent(table)+")")
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var columnIndex, notNull, primaryKey int
		var columnName, columnType string
		var defaultValue any
		if err := rows.Scan(&columnIndex, &columnName, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if columnName == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

type firstCardMetadataObservation struct {
	ID              string `json:"id"`
	ObservationType string `json:"observation_type"`
	Label           string `json:"label"`
	Source          string `json:"source"`
	ClassifierID    string `json:"classifier_id"`
	EvidenceID      string `json:"evidence_id"`
}

type firstCardMetadataBoundary struct {
	Observations []firstCardMetadataObservation `json:"observations"`
	QueueState   string                         `json:"queue_state"`
	QueueReason  string                         `json:"queue_reason"`
	QueueUpdated string                         `json:"queue_updated_at"`
}

type firstCardGateBoundary struct {
	SourceState     string  `json:"source_state"`
	BlockedAt       string  `json:"first_card_blocked_at"`
	BlockedSnapshot string  `json:"first_card_blocked_snapshot_id"`
	QueueState      string  `json:"queue_state"`
	QueueReason     string  `json:"queue_reason"`
	HasLocation     bool    `json:"has_location"`
	Latitude        float64 `json:"latitude"`
	Longitude       float64 `json:"longitude"`
	AccuracyMeters  float64 `json:"accuracy_meters"`
}

type firstCardHistoryBoundary struct {
	ID              string  `json:"id"`
	AssetID         string  `json:"asset_id"`
	LocalIdentifier string  `json:"local_identifier"`
	ObservationType string  `json:"observation_type"`
	ValueText       string  `json:"value_text"`
	ValueJSON       string  `json:"value_json"`
	Confidence      float64 `json:"confidence"`
	Source          string  `json:"source"`
	ModelID         string  `json:"model_id"`
	PromptVersion   string  `json:"prompt_version"`
	GenerationID    string  `json:"generation_id"`
	EvidenceID      string  `json:"evidence_id"`
	StaleSince      string  `json:"stale_since"`
	StaleReason     string  `json:"stale_reason"`
	SupersededAt    string  `json:"superseded_at"`
}

func firstCardFixtureAsset(localIdentifier, creationDate, localPath string) photos.Asset {
	resource := photos.Resource{
		Type:             "photo",
		UTI:              "public.jpeg",
		OriginalFilename: localIdentifier + ".jpeg",
		Availability:     "remote",
		NeedsDownload:    true,
	}
	if localPath != "" {
		resource.Type = "local_original"
		resource.LocalPath = localPath
		resource.Availability = "local"
		resource.AvailableLocally = true
		resource.NeedsDownload = false
	}
	return photos.Asset{
		LocalIdentifier:  localIdentifier,
		MediaType:        "image",
		MediaSubtypes:    "0",
		CreationDate:     creationDate,
		ModificationDate: creationDate,
		AddedDate:        creationDate,
		TimezoneName:     "UTC",
		Width:            100,
		Height:           80,
		Resources:        []photos.Resource{resource},
	}
}

func firstCardSnapshot(assets ...photos.Asset) photos.LibrarySnapshot {
	return photos.LibrarySnapshot{
		Provider:            "fixture",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Completeness: photos.SnapshotCompleteness{
			State:    photos.SnapshotComplete,
			Evidence: map[string]string{"fixture": "complete"},
		},
		Assets: assets,
	}
}

func syncFirstCardSnapshot(t *testing.T, ctx context.Context, paths Paths, libraryPath string, snapshot photos.LibrarySnapshot, at string) SyncResult {
	t.Helper()
	rawInput, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=first_card_source_snapshot input=%s", rawInput)
	result, err := Sync(ctx, paths, SyncOptions{LibraryPath: libraryPath, Provider: fakeProvider{snapshot: snapshot}, Now: fixedClock(at)})
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "first_card_source_transition", result)
	return result
}

func forceFirstCardQueueState(t *testing.T, ctx context.Context, paths Paths, assetID, state string) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.DB().ExecContext(ctx, `update classification_queue set state = ?, reason = 'synthetic damaged queue' where asset_id = ?`, state, assetID); err != nil {
		t.Fatal(err)
	}
}

func seedFirstCardHistory(t *testing.T, ctx context.Context, paths Paths, assetID, history string) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var staleSince, staleReason, supersededAt any
	switch history {
	case "stale":
		staleSince = "2026-07-11T09:30:00Z"
		staleReason = "synthetic input changed"
	case "superseded":
		supersededAt = "2026-07-11T09:45:00Z"
	default:
		t.Fatalf("unsupported card history %q", history)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into model_observation(
  id, asset_id, observation_type, value_text, value_json, confidence, source,
  model_id, prompt_version, evidence_id, stale_since, stale_reason, superseded_at
)
values (?, ?, ?, ?, '{}', 1.0, 'fixture', 'fixture-history-model', 'fixture-history-prompt', '', ?, ?, ?)
`, "fixture-card-"+history, assetID, modelObservationCardSummary, "Synthetic "+history+" card history.", staleSince, staleReason, supersededAt); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
update classification_queue set state = ?, reason = 'fixture card history' where asset_id = ?
`, classifyQueueStateContentClassified, assetID); err != nil {
		t.Fatal(err)
	}
}

func readFirstCardHistory(t *testing.T, ctx context.Context, paths Paths, assetIDs ...string) []firstCardHistoryBoundary {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	result := make([]firstCardHistoryBoundary, 0, len(assetIDs))
	for _, assetID := range assetIDs {
		var row firstCardHistoryBoundary
		if err := db.DB().QueryRowContext(ctx, `
select mo.id, mo.asset_id, a.local_identifier, mo.observation_type, mo.value_text, mo.value_json,
       coalesce(mo.confidence, 0), mo.source, mo.model_id, mo.prompt_version,
       coalesce(mo.generation_id, ''), mo.evidence_id,
       coalesce(mo.stale_since, ''), coalesce(mo.stale_reason, ''), coalesce(mo.superseded_at, '')
from model_observation mo
join asset a on a.id = mo.asset_id
where mo.asset_id = ? and mo.observation_type = ?
`, assetID, modelObservationCardSummary).Scan(
			&row.ID, &row.AssetID, &row.LocalIdentifier, &row.ObservationType,
			&row.ValueText, &row.ValueJSON, &row.Confidence, &row.Source,
			&row.ModelID, &row.PromptVersion, &row.GenerationID, &row.EvidenceID,
			&row.StaleSince, &row.StaleReason, &row.SupersededAt,
		); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	return result
}

func readFirstCardMetadataBoundary(t *testing.T, ctx context.Context, paths Paths, assetID string) firstCardMetadataBoundary {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var boundary firstCardMetadataBoundary
	if err := db.DB().QueryRowContext(ctx, `
select state, reason, updated_at from classification_queue where asset_id = ?
`, assetID).Scan(&boundary.QueueState, &boundary.QueueReason, &boundary.QueueUpdated); err != nil {
		t.Fatal(err)
	}
	rows, err := db.DB().QueryContext(ctx, `
select id, observation_type, label, source, classifier_id, evidence_id
from metadata_observation where asset_id = ?
order by observation_type, label, id
`, assetID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var observation firstCardMetadataObservation
		if err := rows.Scan(&observation.ID, &observation.ObservationType, &observation.Label, &observation.Source, &observation.ClassifierID, &observation.EvidenceID); err != nil {
			t.Fatal(err)
		}
		boundary.Observations = append(boundary.Observations, observation)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return boundary
}

func readFirstCardGateBoundary(t *testing.T, ctx context.Context, paths Paths, assetID string) firstCardGateBoundary {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var boundary firstCardGateBoundary
	if err := db.DB().QueryRowContext(ctx, `
select a.source_state, coalesce(a.first_card_blocked_at, ''), coalesce(a.first_card_blocked_snapshot_id, ''),
       q.state, q.reason,
       exists(select 1 from location_observation where asset_id = a.id),
       coalesce((select latitude from location_observation where asset_id = a.id order by id limit 1), 0),
       coalesce((select longitude from location_observation where asset_id = a.id order by id limit 1), 0),
       coalesce((select horizontal_accuracy from location_observation where asset_id = a.id order by id limit 1), 0)
from asset a join classification_queue q on q.asset_id = a.id where a.id = ?
`, assetID).Scan(
		&boundary.SourceState, &boundary.BlockedAt, &boundary.BlockedSnapshot,
		&boundary.QueueState, &boundary.QueueReason, &boundary.HasLocation,
		&boundary.Latitude, &boundary.Longitude, &boundary.AccuracyMeters,
	); err != nil {
		t.Fatal(err)
	}
	return boundary
}

func proveFirstCardPlaceSeamNotCalled(t *testing.T, ctx context.Context, paths Paths, inputs []classifyInput) {
	t.Helper()
	db, err := openArchive(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var keyCalls, cacheCalls, providerCalls int
	resolver := classifyPlaceResolver{
		key: func(place.Input) string {
			keyCalls++
			t.Error("prohibited input reached place key selection")
			return "unexpected"
		},
		resolveCached: func(context.Context, place.Input) place.ResolveResult {
			cacheCalls++
			t.Error("prohibited input reached place cache resolution")
			return place.ResolveResult{}
		},
		resolveProvider: func(context.Context, place.Input) place.ResolveResult {
			providerCalls++
			t.Error("prohibited input reached place provider")
			return place.ResolveResult{}
		},
	}
	var result ClassifyResult
	ready, err := resolveClassifyPlaces(ctx, db, inputs, nil, nil, resolver, fixedClock("2026-07-11T13:09:00Z"), &result, classifyLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if len(ready) != 0 || keyCalls != 0 || cacheCalls != 0 || providerCalls != 0 {
		t.Fatalf("prohibited place seam: ready=%#v key=%d cache=%d provider=%d", ready, keyCalls, cacheCalls, providerCalls)
	}
}

func loadFirstCardPaidInputs(t *testing.T, ctx context.Context, paths Paths) []classifyInput {
	t.Helper()
	db, err := openArchive(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var inputs []classifyInput
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var loadErr error
		inputs, loadErr = loadClassifyInputs(ctx, tx, 1, "fixture-vision")
		return loadErr
	}); err != nil {
		t.Fatal(err)
	}
	return inputs
}

func logFirstCardStoredGeneration(t *testing.T, ctx context.Context, paths Paths, localIdentifier string, wantRequest, wantResponse []byte) {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var request, response []byte
	var summary string
	if err := db.DB().QueryRowContext(ctx, `
select g.request_body, a.response_body, mo.value_text
from asset asset
join model_generation_asset ga on ga.asset_id = asset.id
join model_generation g on g.id = ga.generation_id
join model_generation_attempt a on a.generation_id = g.id
join model_observation mo on mo.generation_id = g.id and mo.observation_type = ?
where asset.local_identifier = ?
`, modelObservationCardSummary, localIdentifier).Scan(&request, &response, &summary); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(request, wantRequest) || !reflect.DeepEqual(response, wantResponse) || strings.TrimSpace(summary) == "" {
		t.Fatalf("stored generation mismatch: request=%s response=%s summary=%q", request, response, summary)
	}
}
