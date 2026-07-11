package archive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

func TestFirstCardEligibilityLifecycleStopsPaidCallBeforeMedia(t *testing.T) {
	ctx := context.Background()
	paths := testPaths(t)
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}

	accuracy := 4.0
	blockedAsset := firstCardFixtureAsset("blocked-before-first-card", "2026-07-11T09:00:00Z", "")
	blockedAsset.Location = &photos.Location{Latitude: 1.25, Longitude: 2.5, HorizontalAccuracy: &accuracy}
	cardedAsset := firstCardFixtureAsset("existing-card", "2026-07-11T08:00:00Z", "")
	staleCardAsset := firstCardFixtureAsset("stale-card-history", "2026-07-11T07:00:00Z", "")
	supersededCardAsset := firstCardFixtureAsset("superseded-card-history", "2026-07-11T06:00:00Z", "")
	present := firstCardSnapshot(blockedAsset, cardedAsset, staleCardAsset, supersededCardAsset)
	first := syncFirstCardSnapshot(t, ctx, paths, libraryPath, present, "2026-07-11T10:00:00Z")
	blockedID := stableID("asset", first.SourceLibraryID, blockedAsset.LocalIdentifier)
	cardedID := stableID("asset", first.SourceLibraryID, cardedAsset.LocalIdentifier)
	staleCardID := stableID("asset", first.SourceLibraryID, staleCardAsset.LocalIdentifier)
	supersededCardID := stableID("asset", first.SourceLibraryID, supersededCardAsset.LocalIdentifier)
	seedExistingCard(t, ctx, paths, cardedID)
	seedFirstCardHistory(t, ctx, paths, staleCardID, "stale")
	seedFirstCardHistory(t, ctx, paths, supersededCardID, "superseded")
	priorHistory := readFirstCardHistory(t, ctx, paths, staleCardID, supersededCardID)
	logBoundary(t, "first_card_prior_history_input", priorHistory)
	wantPriorHistory := []firstCardHistoryBoundary{
		{
			ID:              "fixture-card-stale",
			AssetID:         staleCardID,
			LocalIdentifier: staleCardAsset.LocalIdentifier,
			ObservationType: modelObservationCardSummary,
			ValueText:       "Synthetic stale card history.",
			ValueJSON:       "{}",
			Confidence:      1,
			Source:          "fixture",
			ModelID:         "fixture-history-model",
			PromptVersion:   "fixture-history-prompt",
			StaleSince:      "2026-07-11T09:30:00Z",
			StaleReason:     "synthetic input changed",
		},
		{
			ID:              "fixture-card-superseded",
			AssetID:         supersededCardID,
			LocalIdentifier: supersededCardAsset.LocalIdentifier,
			ObservationType: modelObservationCardSummary,
			ValueText:       "Synthetic superseded card history.",
			ValueJSON:       "{}",
			Confidence:      1,
			Source:          "fixture",
			ModelID:         "fixture-history-model",
			PromptVersion:   "fixture-history-prompt",
			SupersededAt:    "2026-07-11T09:45:00Z",
		},
	}
	if !reflect.DeepEqual(priorHistory, wantPriorHistory) {
		t.Fatalf("prior card history = %#v, want %#v", priorHistory, wantPriorHistory)
	}

	absent := firstCardSnapshot()
	missing := syncFirstCardSnapshot(t, ctx, paths, libraryPath, absent, "2026-07-11T11:00:00Z")
	blockedMissing := readSourceStateRow(t, ctx, paths, blockedID)
	cardedMissing := readSourceStateRow(t, ctx, paths, cardedID)
	staleCardMissing := readSourceStateRow(t, ctx, paths, staleCardID)
	supersededCardMissing := readSourceStateRow(t, ctx, paths, supersededCardID)
	logBoundary(t, "first_card_missing_asset_facts", blockedMissing)
	logBoundary(t, "existing_card_missing_asset_facts", cardedMissing)
	logBoundary(t, "stale_card_missing_asset_facts", staleCardMissing)
	logBoundary(t, "superseded_card_missing_asset_facts", supersededCardMissing)
	if blockedMissing.FirstCardBlockedAt != "2026-07-11T11:00:00Z" || blockedMissing.FirstCardBlockedSnapshotID != missing.SnapshotID {
		t.Fatalf("blocked facts after first absence = %#v", blockedMissing)
	}
	if cardedMissing.FirstCardBlockedAt != "" || cardedMissing.ModelRows != 1 {
		t.Fatalf("existing card facts after absence = %#v", cardedMissing)
	}
	if staleCardMissing.FirstCardBlockedAt != "" || supersededCardMissing.FirstCardBlockedAt != "" {
		t.Fatalf("historical card was treated as a first-card candidate: stale=%#v superseded=%#v", staleCardMissing, supersededCardMissing)
	}

	syncFirstCardSnapshot(t, ctx, paths, libraryPath, absent, "2026-07-11T12:00:00Z")
	repeated := readSourceStateRow(t, ctx, paths, blockedID)
	logBoundary(t, "first_card_repeated_absence", repeated)
	if repeated.FirstCardBlockedAt != blockedMissing.FirstCardBlockedAt || repeated.FirstCardBlockedSnapshotID != blockedMissing.FirstCardBlockedSnapshotID {
		t.Fatalf("repeated absence changed first-card facts: got %#v want %#v", repeated, blockedMissing)
	}

	restoredResult := syncFirstCardSnapshot(t, ctx, paths, libraryPath, present, "2026-07-11T13:00:00Z")
	blockedRestored := readSourceStateRow(t, ctx, paths, blockedID)
	cardedRestored := readSourceStateRow(t, ctx, paths, cardedID)
	staleCardRestored := readSourceStateRow(t, ctx, paths, staleCardID)
	supersededCardRestored := readSourceStateRow(t, ctx, paths, supersededCardID)
	logBoundary(t, "first_card_restored_asset_facts", blockedRestored)
	logBoundary(t, "existing_card_restored_asset_facts", cardedRestored)
	if blockedRestored.State != sourceStateCurrent || blockedRestored.StateSnapshotID != restoredResult.SnapshotID || blockedRestored.FirstMissingAt != "" || blockedRestored.FirstCardBlockedAt != blockedMissing.FirstCardBlockedAt || blockedRestored.FirstCardBlockedSnapshotID != blockedMissing.FirstCardBlockedSnapshotID || blockedRestored.QueueState != classifyQueueStateFirstCardProhibited {
		t.Fatalf("blocked restored row = %#v", blockedRestored)
	}
	if cardedRestored.State != sourceStateCurrent || cardedRestored.QueueState != classifyQueueStateContentClassified || cardedRestored.ModelRows != 1 {
		t.Fatalf("existing card restored row = %#v", cardedRestored)
	}
	logBoundary(t, "stale_card_restored_asset_facts", staleCardRestored)
	logBoundary(t, "superseded_card_restored_asset_facts", supersededCardRestored)
	staleCardRefresh := readFirstCardGateBoundary(t, ctx, paths, staleCardID)
	supersededCardRefresh := readFirstCardGateBoundary(t, ctx, paths, supersededCardID)
	logBoundary(t, "stale_card_restored_refresh_queue", staleCardRefresh)
	logBoundary(t, "superseded_card_restored_refresh_queue", supersededCardRefresh)
	if staleCardRestored.FirstCardBlockedAt != "" ||
		staleCardRefresh.SourceState != sourceStateCurrent ||
		staleCardRefresh.QueueState != classifyQueueStateMetadataClassified ||
		staleCardRefresh.QueueReason != "source_restored: card refresh required" ||
		supersededCardRestored.FirstCardBlockedAt != "" ||
		supersededCardRefresh.SourceState != sourceStateCurrent ||
		supersededCardRefresh.QueueState != classifyQueueStateMetadataClassified ||
		supersededCardRefresh.QueueReason != "source_restored: card refresh required" {
		t.Fatalf("historical card restoration: stale=%#v superseded=%#v", staleCardRestored, supersededCardRestored)
	}

	metadataFirst, err := Classify(ctx, paths, ClassifyOptions{Now: fixedClock("2026-07-11T13:05:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "first_card_metadata_once", metadataFirst)
	if metadataFirst.Processed != 1 || metadataFirst.MetadataClassified != 1 {
		t.Fatalf("first metadata result = %#v", metadataFirst)
	}
	metadataAfterFirst := readFirstCardMetadataBoundary(t, ctx, paths, blockedID)
	logBoundary(t, "first_card_metadata_storage_after_first", metadataAfterFirst)
	wantMetadataAfterFirst := firstCardMetadataBoundary{
		Observations: []firstCardMetadataObservation{
			{ID: stableID("metadata_observation", blockedID, metadataClassifierSource, "geometry", "landscape"), ObservationType: "geometry", Label: "landscape", Source: metadataClassifierSource, ClassifierID: metadataClassifierModelID},
			{ID: stableID("metadata_observation", blockedID, metadataClassifierSource, "media_type", "image"), ObservationType: "media_type", Label: "image", Source: metadataClassifierSource, ClassifierID: metadataClassifierModelID},
			{ID: stableID("metadata_observation", blockedID, metadataClassifierSource, "resource_type", "photo"), ObservationType: "resource_type", Label: "photo", Source: metadataClassifierSource, ClassifierID: metadataClassifierModelID},
		},
		QueueState:   classifyQueueStateFirstCardProhibited,
		QueueReason:  "deleted_before_first_card",
		QueueUpdated: "2026-07-11T13:05:00Z",
	}
	if !reflect.DeepEqual(metadataAfterFirst, wantMetadataAfterFirst) {
		t.Fatalf("stored metadata after first run = %#v, want %#v", metadataAfterFirst, wantMetadataAfterFirst)
	}
	metadataSecond, err := Classify(ctx, paths, ClassifyOptions{Now: fixedClock("2026-07-11T13:06:00Z")})
	if err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "first_card_metadata_repeat", metadataSecond)
	metadataAfterSecond := readFirstCardMetadataBoundary(t, ctx, paths, blockedID)
	logBoundary(t, "first_card_metadata_storage_after_second", metadataAfterSecond)
	if metadataSecond.Processed != 0 || !reflect.DeepEqual(metadataAfterSecond, wantMetadataAfterFirst) {
		t.Fatalf("repeated metadata result = %#v row=%#v", metadataSecond, readSourceStateRow(t, ctx, paths, blockedID))
	}

	restarted, err := openExistingArchive(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	blockedEligibility, err := firstCardEligibilityForAsset(ctx, restarted.DB(), blockedID)
	if err != nil {
		t.Fatal(err)
	}
	cardedEligibility, err := firstCardEligibilityForAsset(ctx, restarted.DB(), cardedID)
	if err != nil {
		t.Fatal(err)
	}
	staleCardEligibility, err := firstCardEligibilityForAsset(ctx, restarted.DB(), staleCardID)
	if err != nil {
		t.Fatal(err)
	}
	supersededCardEligibility, err := firstCardEligibilityForAsset(ctx, restarted.DB(), supersededCardID)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Close(); err != nil {
		t.Fatal(err)
	}
	logBoundary(t, "first_card_restart_eligibility", map[string]firstCardEligibility{"blocked": blockedEligibility, "existing_card": cardedEligibility, "stale_card": staleCardEligibility, "superseded_card": supersededCardEligibility})
	if blockedEligibility != firstCardProhibitedDeletedBeforeCard || cardedEligibility != firstCardEligible || staleCardEligibility != firstCardEligible || supersededCardEligibility != firstCardEligible {
		t.Fatalf("restart eligibility: blocked=%q carded=%q stale=%q superseded=%q", blockedEligibility, cardedEligibility, staleCardEligibility, supersededCardEligibility)
	}

	forceFirstCardQueueState(t, ctx, paths, blockedID, classifyQueueStatePending)
	damagedGateInput := readFirstCardGateBoundary(t, ctx, paths, blockedID)
	logBoundary(t, "first_card_damaged_queue_input", damagedGateInput)
	if !damagedGateInput.HasLocation || damagedGateInput.QueueState != classifyQueueStatePending {
		t.Fatalf("damaged located gate input = %#v", damagedGateInput)
	}
	selected := loadFirstCardPaidInputs(t, ctx, paths)
	logBoundary(t, "first_card_paid_selection", selected)
	if len(selected) != 0 {
		t.Fatalf("damaged paid selection = %#v", selected)
	}
	logBoundary(t, "first_card_paid_selection_queue_output", readFirstCardGateBoundary(t, ctx, paths, blockedID))
	proveFirstCardPlaceSeamNotCalled(t, ctx, paths, selected)
	forceFirstCardQueueState(t, ctx, paths, blockedID, classifyQueueStatePending)
	var mediaCalls atomic.Int32
	oldExport := exportOriginalResource
	exportOriginalResource = func(context.Context, photos.OriginalExportQuery, string, bool) error {
		mediaCalls.Add(1)
		t.Error("prohibited input reached media export")
		return errors.New("prohibited media export")
	}
	defer func() { exportOriginalResource = oldExport }()
	var prohibitedProviderCalls atomic.Int32
	restoreTransport := useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		prohibitedProviderCalls.Add(1)
		t.Error("prohibited input reached model provider")
		http.Error(w, "prohibited model call", http.StatusInternalServerError)
	}))
	prohibitedResult, err := Classify(ctx, paths, ClassifyOptions{
		Limit:    1,
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-07-11T13:10:00Z"),
	})
	restoreTransport()
	if err != nil {
		t.Fatal(err)
	}
	prohibitedRow := readSourceStateRow(t, ctx, paths, blockedID)
	logBoundary(t, "first_card_prohibited_paid_output", map[string]any{
		"classify":       prohibitedResult,
		"media_calls":    mediaCalls.Load(),
		"place_calls":    prohibitedResult.PlaceProviderAttempts,
		"provider_calls": prohibitedProviderCalls.Load(),
		"queue_state":    prohibitedRow.QueueState,
	})
	if mediaCalls.Load() != 0 || prohibitedResult.PlaceProviderAttempts != 0 || prohibitedProviderCalls.Load() != 0 || prohibitedResult.ModelCallAttempts != 0 || prohibitedRow.QueueState != classifyQueueStateFirstCardProhibited {
		t.Fatalf("prohibited paid path: result=%#v media=%d provider=%d row=%#v", prohibitedResult, mediaCalls.Load(), prohibitedProviderCalls.Load(), prohibitedRow)
	}

	imagePath := filepath.Join(t.TempDir(), "eligible-control.jpeg")
	writeSyntheticImage(t, imagePath)
	eligibleAsset := firstCardFixtureAsset("eligible-control", "2026-07-11T14:00:00Z", imagePath)
	changedBlockedAsset := blockedAsset
	changedBlockedAsset.Favorite = true
	syncFirstCardSnapshot(t, ctx, paths, libraryPath, firstCardSnapshot(changedBlockedAsset, cardedAsset, staleCardAsset, supersededCardAsset, eligibleAsset), "2026-07-11T14:05:00Z")
	changedBlockedRow := readSourceStateRow(t, ctx, paths, blockedID)
	logBoundary(t, "first_card_changed_asset_queue_projection", changedBlockedRow)
	if changedBlockedRow.QueueState != classifyQueueStateFirstCardProhibited || changedBlockedRow.FirstCardBlockedAt != blockedMissing.FirstCardBlockedAt {
		t.Fatalf("changed blocked asset queue projection = %#v", changedBlockedRow)
	}
	fixtureResponse := fixtureCardResponse(
		"A synthetic blue and gold image fixture.",
		"A 2 by 2 synthetic image contains blue and gold pixels for a local provider test.",
		"verdict: plausible\nreason: no place claim is made.",
		"None",
		"The fixture has no real-world scene.",
	)
	rawFixtureResponse, err := json.Marshal(map[string]any{"response": fixtureResponse, "done": true})
	if err != nil {
		t.Fatal(err)
	}
	var eligibleProviderCalls atomic.Int32
	var rawRequest []byte
	restoreTransport = useArchiveHandlerTransport(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		eligibleProviderCalls.Add(1)
		var readErr error
		rawRequest, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read fixture request: %v", readErr)
			return
		}
		t.Logf("boundary=fixture_provider input=%s", rawRequest)
		t.Logf("boundary=fixture_provider output=%s", rawFixtureResponse)
		_, _ = w.Write(rawFixtureResponse)
	}))
	eligibleResult, err := Classify(ctx, paths, ClassifyOptions{
		Limit:    1,
		Model:    "fixture-vision",
		ModelURL: fixtureModelURL,
		Now:      fixedClock("2026-07-11T14:10:00Z"),
	})
	restoreTransport()
	if err != nil {
		t.Fatal(err)
	}
	if eligibleProviderCalls.Load() != 1 || eligibleResult.ContentClassified != 1 || eligibleResult.ModelCallAttempts != 1 {
		t.Fatalf("eligible control: calls=%d result=%#v", eligibleProviderCalls.Load(), eligibleResult)
	}
	logBoundary(t, "eligible_control_classify", eligibleResult)
	logFirstCardStoredGeneration(t, ctx, paths, eligibleAsset.LocalIdentifier, rawRequest, rawFixtureResponse)
}
