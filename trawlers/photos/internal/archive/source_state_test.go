package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit/state"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type sourceStateRow struct {
	State           string `json:"state"`
	FirstMissingAt  string `json:"first_missing_at"`
	SourceDeletedAt string `json:"source_deleted_at"`
	StateSnapshotID string `json:"state_snapshot_id"`
	QueueState      string `json:"queue_state"`
	ModelRows       int    `json:"model_rows"`
	ModelRuns       int    `json:"model_runs"`
}

type rawSourceStateProvider struct {
	snapshot photos.LibrarySnapshot
}

func (p rawSourceStateProvider) Snapshot(context.Context, string) (photos.LibrarySnapshot, error) {
	return p.snapshot, nil
}

func TestSourceStateLifecycleUsesOnlyCompleteSameLibrarySnapshots(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	present := fakeSnapshot(false, true)
	asset := present.Assets[0]
	uncardedAsset := present.Assets[1]

	first := syncSourceStateSnapshot(t, ctx, paths, libraryPath, present, "2026-07-11T10:00:00Z")
	assetID := stableID("asset", first.SourceLibraryID, asset.LocalIdentifier)
	uncardedAssetID := stableID("asset", first.SourceLibraryID, uncardedAsset.LocalIdentifier)
	seedExistingCard(t, ctx, paths, assetID)
	current := readSourceStateRow(t, ctx, paths, assetID)
	logBoundary(t, "archive_current", current)
	if current.State != sourceStateCurrent || current.StateSnapshotID != first.SnapshotID || current.FirstMissingAt != "" || current.QueueState != classifyQueueStateContentClassified {
		t.Fatalf("current row = %#v", current)
	}
	assertOpenAndSearchSourceState(t, ctx, paths, assetID, sourceStateCurrent, "", "")

	absent := present
	absent.Assets = nil
	missing := syncSourceStateSnapshot(t, ctx, paths, libraryPath, absent, "2026-07-11T11:00:00Z")
	deleted := readSourceStateRow(t, ctx, paths, assetID)
	logBoundary(t, "archive_deleted", deleted)
	if deleted.State != sourceStateDeletedUpstream || deleted.FirstMissingAt != "2026-07-11T11:00:00Z" || deleted.StateSnapshotID != missing.SnapshotID || deleted.SourceDeletedAt != "" || deleted.QueueState != queueStateSourceDeleted || deleted.ModelRows != 1 || deleted.ModelRuns != 0 {
		t.Fatalf("deleted row = %#v", deleted)
	}
	uncardedDeleted := readSourceStateRow(t, ctx, paths, uncardedAssetID)
	logBoundary(t, "archive_uncarded_deleted", uncardedDeleted)
	if uncardedDeleted.State != sourceStateDeletedUpstream || uncardedDeleted.QueueState != queueStateSourceDeleted || uncardedDeleted.ModelRows != 0 || uncardedDeleted.ModelRuns != 0 {
		t.Fatalf("uncarded deleted row = %#v", uncardedDeleted)
	}
	assertNoSourceDeletedClassifyInputs(t, ctx, paths)
	assertOpenAndSearchSourceState(t, ctx, paths, assetID, sourceStateDeletedUpstream, deleted.FirstMissingAt, "Deleted upstream · ")

	syncSourceStateSnapshot(t, ctx, paths, libraryPath, absent, "2026-07-11T12:00:00Z")
	repeated := readSourceStateRow(t, ctx, paths, assetID)
	logBoundary(t, "archive_repeated_absence", repeated)
	if repeated.FirstMissingAt != deleted.FirstMissingAt || repeated.StateSnapshotID != deleted.StateSnapshotID || repeated.ModelRows != 1 || repeated.ModelRuns != 0 {
		t.Fatalf("repeated absence = %#v, want first missing interval %#v", repeated, deleted)
	}
	successfulCursor := readSourceCursor(t, ctx, paths, first.SourceLibraryID)
	successfulFreshness := readSourceFreshness(t, ctx, paths)
	successfulLibrarySnapshot := readSourceLibrarySnapshot(t, ctx, paths, first.SourceLibraryID)

	for index, state := range []photos.SnapshotCompletenessState{
		photos.SnapshotPartial,
		photos.SnapshotLimited,
		photos.SnapshotFailed,
		photos.SnapshotCancelled,
	} {
		incomplete := present
		incomplete.Completeness = photos.SnapshotCompleteness{
			State:    state,
			Evidence: map[string]string{"fixture_result": string(state)},
		}
		at := time.Date(2026, 7, 11, 13+index, 0, 0, 0, time.UTC).Format(time.RFC3339)
		incompleteResult := syncIncompleteSourceStateSnapshot(t, ctx, paths, libraryPath, incomplete, at)
		if incompleteResult.SnapshotCompleteness != string(state) {
			t.Fatalf("%s result completeness = %q", state, incompleteResult.SnapshotCompleteness)
		}
		unchanged := readSourceStateRow(t, ctx, paths, assetID)
		logBoundary(t, "archive_incomplete_"+string(state), unchanged)
		if unchanged != repeated {
			t.Fatalf("%s snapshot changed source state: got %#v want %#v", state, unchanged, repeated)
		}
		if cursor := readSourceCursor(t, ctx, paths, first.SourceLibraryID); cursor != successfulCursor {
			t.Fatalf("%s snapshot advanced cursor: got %#v want %#v", state, cursor, successfulCursor)
		}
		if freshness := readSourceFreshness(t, ctx, paths); freshness != successfulFreshness {
			t.Fatalf("%s snapshot advanced status freshness: got %q want %q", state, freshness, successfulFreshness)
		}
		if librarySnapshot := readSourceLibrarySnapshot(t, ctx, paths, first.SourceLibraryID); librarySnapshot != successfulLibrarySnapshot {
			t.Fatalf("%s snapshot advanced source library snapshot: got %#v want %#v", state, librarySnapshot, successfulLibrarySnapshot)
		}
	}

	otherLibrary := filepath.Join(t.TempDir(), "Other Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(otherLibrary); err != nil {
		t.Fatal(err)
	}
	syncSourceStateSnapshot(t, ctx, paths, otherLibrary, absent, "2026-07-11T17:00:00Z")
	otherUnchanged := readSourceStateRow(t, ctx, paths, assetID)
	logBoundary(t, "archive_other_library_absence", otherUnchanged)
	if otherUnchanged != repeated {
		t.Fatalf("other library changed source state: got %#v want %#v", otherUnchanged, repeated)
	}

	restoredResult := syncSourceStateSnapshot(t, ctx, paths, libraryPath, present, "2026-07-11T18:00:00Z")
	restored := readSourceStateRow(t, ctx, paths, assetID)
	logBoundary(t, "archive_restored", restored)
	if restored.State != sourceStateCurrent || restored.FirstMissingAt != "" || restored.SourceDeletedAt != "" || restored.StateSnapshotID != restoredResult.SnapshotID || restored.QueueState != classifyQueueStateContentClassified || restored.ModelRows != 1 || restored.ModelRuns != 0 {
		t.Fatalf("restored row = %#v", restored)
	}
	uncardedRestored := readSourceStateRow(t, ctx, paths, uncardedAssetID)
	logBoundary(t, "archive_uncarded_restored", uncardedRestored)
	if uncardedRestored.State != sourceStateCurrent || uncardedRestored.QueueState != classifyQueueStatePending || uncardedRestored.ModelRows != 0 || uncardedRestored.ModelRuns != 0 {
		t.Fatalf("uncarded restored row = %#v", uncardedRestored)
	}
	assertOpenAndSearchSourceState(t, ctx, paths, assetID, sourceStateCurrent, "", "")
}

func TestIncompleteFirstSnapshotIsAuditableButDoesNotLookSuccessful(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeSnapshot(false, false)
	snapshot.Completeness = photos.SnapshotCompleteness{
		State:    photos.SnapshotLimited,
		Evidence: map[string]string{"fixture_result": "limited"},
	}
	result := syncIncompleteSourceStateSnapshot(t, ctx, paths, libraryPath, snapshot, "2026-07-11T10:00:00Z")
	if result.AssetsNew != 0 || result.AssetsChanged != 0 || result.PreviouslySeenMissing != 0 {
		t.Fatalf("incomplete result changed source state: %#v", result)
	}
	if cursor := readSourceCursor(t, ctx, paths, result.SourceLibraryID); cursor != "" {
		t.Fatalf("incomplete first snapshot cursor = %q, want empty", cursor)
	}
	if freshness := readSourceFreshness(t, ctx, paths); freshness != "" {
		t.Fatalf("incomplete first snapshot freshness = %q, want empty", freshness)
	}
	status, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "status_after_incomplete_first_snapshot", map[string]any{
		"state":          status.State,
		"last_import_at": status.LastImportAt,
		"photos":         status.Counts[0].Value,
	})
	if status.State != "empty" || status.LastImportAt != "" {
		t.Fatalf("status after incomplete first snapshot = %#v", status)
	}
}

func TestSyncRejectsMissingSnapshotCompleteness(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	snapshot := fakeSnapshot(false, false)
	snapshot.Completeness = photos.SnapshotCompleteness{}
	input, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=source_snapshot input=%s", input)
	_, err = Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    rawSourceStateProvider{snapshot: snapshot},
		Now:         fixedClock("2026-07-11T10:00:00Z"),
	})
	t.Logf("boundary=sync_importer output_error=%q", err)
	if err == nil || !strings.Contains(err.Error(), "snapshot completeness") {
		t.Fatalf("Sync error = %v, want missing completeness", err)
	}
}

func syncSourceStateSnapshot(t *testing.T, ctx context.Context, paths Paths, libraryPath string, snapshot photos.LibrarySnapshot, at string) SyncResult {
	t.Helper()
	input, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=source_snapshot input=%s", input)
	result, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: snapshot},
		Now:         fixedClock(at),
	})
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "sync_importer", result)
	readSnapshotCompleteness(t, ctx, paths, result.SnapshotID)
	return result
}

func syncIncompleteSourceStateSnapshot(t *testing.T, ctx context.Context, paths Paths, libraryPath string, snapshot photos.LibrarySnapshot, at string) SyncResult {
	t.Helper()
	input, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=source_snapshot input=%s", input)
	result, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    fakeProvider{snapshot: snapshot},
		Now:         fixedClock(at),
	})
	logBoundary(t, "sync_importer", result)
	t.Logf("boundary=sync_importer output_error=%q", err)
	var incomplete *SnapshotIncompleteError
	if !errors.As(err, &incomplete) || incomplete.State != string(snapshot.Completeness.State) {
		t.Fatalf("Sync error = %T %v, want SnapshotIncompleteError(%q)", err, err, snapshot.Completeness.State)
	}
	readSnapshotCompleteness(t, ctx, paths, result.SnapshotID)
	return result
}

func readSnapshotCompleteness(t *testing.T, ctx context.Context, paths Paths, snapshotID string) {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var state, evidence string
	if err := db.DB().QueryRowContext(ctx, `
select completeness_state, completeness_evidence_json
from crawl_snapshot
where id = ?
`, snapshotID).Scan(&state, &evidence); err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=archive_snapshot output={\"state\":%q,\"evidence\":%s}", state, evidence)
}

func readSourceCursor(t *testing.T, ctx context.Context, paths Paths, sourceID string) string {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	record, found, err := state.NewCursor(db.DB()).Get(ctx, "fake", "source_library", sourceID)
	if err != nil {
		t.Fatal(err)
	}
	cursor := ""
	if found {
		cursor = record.Cursor
	}
	logBoundary(t, "successful_sync_cursor", map[string]any{"cursor": cursor})
	return cursor
}

func readSourceFreshness(t *testing.T, ctx context.Context, paths Paths) string {
	t.Helper()
	status, err := Status(ctx, paths)
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "successful_sync_freshness", map[string]any{"last_import_at": status.LastImportAt})
	return status.LastImportAt
}

type sourceLibrarySnapshot struct {
	Path      string `json:"path"`
	CreatedAt string `json:"created_at"`
}

func readSourceLibrarySnapshot(t *testing.T, ctx context.Context, paths Paths, sourceID string) sourceLibrarySnapshot {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var snapshot sourceLibrarySnapshot
	if err := db.DB().QueryRowContext(ctx, `
select snapshot_path, snapshot_created_at
from source_library
where id = ?
`, sourceID).Scan(&snapshot.Path, &snapshot.CreatedAt); err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "successful_source_library_snapshot", snapshot)
	return snapshot
}

func seedExistingCard(t *testing.T, ctx context.Context, paths Paths, assetID string) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.DB().ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values ('fixture-card', ?, ?, 'Synthetic beach card.', '{}', 1.0, 'fixture', 'fixture-model', 'fixture-prompt', '')
`, assetID, modelObservationCardSummary); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('fixture-card', ?, '', 'Synthetic beach card.')
`, assetID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, `
update classification_queue set state = ?, reason = 'fixture existing card' where asset_id = ?
`, classifyQueueStateContentClassified, assetID); err != nil {
		t.Fatal(err)
	}
}

func readSourceStateRow(t *testing.T, ctx context.Context, paths Paths, assetID string) sourceStateRow {
	t.Helper()
	db, err := store.OpenReadOnly(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var row sourceStateRow
	if err := db.DB().QueryRowContext(ctx, `
select a.source_state,
       coalesce(a.first_missing_at, ''),
       coalesce(a.source_deleted_at, ''),
       a.source_state_snapshot_id,
       q.state,
       (select count(*) from model_observation where asset_id = a.id),
       (select count(*) from model_run)
from asset a
join classification_queue q on q.asset_id = a.id
where a.id = ?
`, assetID).Scan(&row.State, &row.FirstMissingAt, &row.SourceDeletedAt, &row.StateSnapshotID, &row.QueueState, &row.ModelRows, &row.ModelRuns); err != nil {
		t.Fatal(err)
	}
	return row
}

func assertNoSourceDeletedClassifyInputs(t *testing.T, ctx context.Context, paths Paths) {
	t.Helper()
	db, err := store.Open(ctx, store.Options{Path: paths.Database})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var inputs []classifyInput
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var loadErr error
		inputs, loadErr = loadClassifyInputs(ctx, tx, 10, "fixture-model")
		return loadErr
	}); err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "classification_selection", inputs)
	if len(inputs) != 0 {
		t.Fatalf("deleted upstream assets selected for classification: %#v", inputs)
	}
}

func assertOpenAndSearchSourceState(t *testing.T, ctx context.Context, paths Paths, assetID, wantState, wantFirstMissingAt, wantSnippetPrefix string) {
	t.Helper()
	opened, err := Open(ctx, paths, assetID)
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "open_json", opened)
	if opened.Mechanical.Source.State != wantState || opened.Mechanical.Source.FirstMissingAt != wantFirstMissingAt {
		t.Fatalf("open source = %#v", opened.Mechanical.Source)
	}
	search, err := Search(ctx, paths, SearchOptions{Query: "beach", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "search_json", search)
	if len(search.Results) != 1 || !strings.HasPrefix(search.Results[0].Snippet, wantSnippetPrefix) {
		t.Fatalf("search results = %#v, want snippet prefix %q", search.Results, wantSnippetPrefix)
	}
}

func logBoundary(t *testing.T, boundary string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("boundary=%s output=%s", boundary, data)
}
