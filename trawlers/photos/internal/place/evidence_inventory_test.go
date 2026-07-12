package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEvidenceInventoryMakesNoProviderCallsAndWritesOnlyPrivateManifest(t *testing.T) {
	root := privateTempDir(t)
	log := &syntheticEvidenceLog{}
	summary, err := RunEvidenceInventory(context.Background(), EvidenceInventoryOptions{
		Source: EvidenceInventorySource{
			SourceLibraryID: "source:synthetic",
			Snapshot:        EvidenceSnapshotReceipt{ID: "snapshot:complete", CompletedAt: "2026-07-12T10:00:00Z", CompletenessState: "complete", CompletenessEvidenceJSON: `{"fixture":"complete"}`},
			Assets: []EvidenceInventorySourceAsset{
				{AssetID: "asset:located", TakenAt: "2026-07-01T10:00:00Z", Location: &Coordinate{Latitude: 52.36, Longitude: 4.89}},
				{AssetID: "asset:missing", TakenAt: "2026-07-02T10:00:00Z"},
			},
		},
		OutputDir: root,
		Geoapify:  syntheticInventoryGeoapify(),
		LogSink:   log,
	})
	if err != nil {
		t.Fatal(err)
	}
	if summary.State != inventoryStateComplete || summary.Counts.CurrentImages != 2 || summary.Counts.ProviderEligibleImages != 1 || summary.Counts.MissingLocationImages != 1 {
		t.Fatalf("inventory summary = %#v", summary)
	}
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"asset:located", "52.36", root} {
		if strings.Contains(string(summaryJSON), private) {
			t.Fatalf("content-safe summary leaked %q: %s", private, summaryJSON)
		}
	}
	manifest, err := readEvidenceManifest(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Assets) != 2 || len(manifest.Assets[0].Requests) != 3 || manifest.Assets[1].Location != nil {
		t.Fatalf("private manifest = %#v", manifest)
	}
	for _, request := range manifest.Assets[0].Requests {
		if request.Bytes == "" || request.SHA256 != evidenceDigest([]byte(request.Bytes)) || strings.Contains(request.Bytes, "synthetic-secret") {
			t.Fatalf("credential-free request = %#v", request)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("RAW INVENTORY MANIFEST %s", data)
	if !log.contains("place_evidence_inventory_item", "outcome=eligible", "duration_ms=") || !log.contains("place_evidence_inventory_phase", "outcome=complete", "duration_ms=") {
		t.Fatalf("inventory observability = %#v", log.lines)
	}
}

func TestEvidenceInventoryStopsOnZeroPairAndUnsafeOutputRoots(t *testing.T) {
	root := privateTempDir(t)
	summary, err := RunEvidenceInventory(context.Background(), EvidenceInventoryOptions{
		Source: EvidenceInventorySource{
			SourceLibraryID: "source:synthetic",
			Snapshot:        EvidenceSnapshotReceipt{ID: "snapshot:complete", CompletenessState: "complete"},
			Assets: []EvidenceInventorySourceAsset{
				{AssetID: "asset:zero", Location: &Coordinate{}},
				{AssetID: "asset:valid", Location: &Coordinate{Latitude: 52, Longitude: 4}},
				{AssetID: "asset:missing"},
			},
		},
		OutputDir: root,
		Geoapify:  syntheticInventoryGeoapify(),
	})
	if err != nil || summary.State != inventoryStateStopped || summary.StopReason != EvidenceInventoryStopUnsafe || summary.Counts.CurrentImages != 3 || summary.Counts.ProviderEligibleImages != 1 || summary.Counts.MissingLocationImages != 1 {
		t.Fatalf("zero-pair inventory = %#v error=%v", summary, err)
	}
	data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil || strings.Count(string(data), `"asset_id"`) != 3 {
		t.Fatalf("stopped inventory did not retain all rows: %q error=%v", data, err)
	}
	if err := ensurePrivateOutputRoot("relative-output"); err == nil {
		t.Fatal("relative output root passed")
	}
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateOutputRoot(repo); err == nil {
		t.Fatal("repository output root passed")
	}
	symlink := filepath.Join(t.TempDir(), "output-link")
	if err := os.Symlink(root, symlink); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateOutputRoot(symlink); err == nil {
		t.Fatal("symlink output root passed")
	}
	public := t.TempDir()
	if err := os.Chmod(public, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateOutputRoot(public); err == nil {
		t.Fatal("public output root passed")
	}
	privateInput := filepath.Join(root, "input.json")
	if err := os.WriteFile(privateInput, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateEvidenceInputFile(privateInput); err != nil {
		t.Fatalf("private input failed: %v", err)
	}
	if err := os.Chmod(privateInput, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ValidatePrivateEvidenceInputFile(privateInput); err == nil {
		t.Fatal("public input file passed")
	}
}

func TestEvidenceInventoryKeepsSingleZeroAxisCoordinates(t *testing.T) {
	root := privateTempDir(t)
	summary, err := RunEvidenceInventory(context.Background(), EvidenceInventoryOptions{
		Source: EvidenceInventorySource{
			SourceLibraryID: "source:synthetic",
			Snapshot:        EvidenceSnapshotReceipt{ID: "snapshot:complete", CompletenessState: "complete"},
			Assets: []EvidenceInventorySourceAsset{
				{AssetID: "asset:equator", Location: &Coordinate{Latitude: 0, Longitude: 4.89}},
				{AssetID: "asset:meridian", Location: &Coordinate{Latitude: 52.36, Longitude: 0}},
			},
		},
		OutputDir: root,
		Geoapify:  syntheticInventoryGeoapify(),
	})
	if err != nil || summary.State != inventoryStateComplete || summary.Counts.ProviderEligibleImages != 2 {
		t.Fatalf("single-zero-axis inventory = %#v error=%v", summary, err)
	}
}

func TestEvidenceInventoryStopsAndPersistsNonFiniteCoordinates(t *testing.T) {
	for name, coordinate := range map[string]Coordinate{
		"nan":      {Latitude: math.NaN(), Longitude: 4},
		"positive": {Latitude: 52, Longitude: math.Inf(1)},
		"negative": {Latitude: math.Inf(-1), Longitude: 4},
	} {
		t.Run(name, func(t *testing.T) {
			root := privateTempDir(t)
			providerCalls := 0
			config := syntheticInventoryGeoapify()
			config.HTTPClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				providerCalls++
				return nil, errors.New("unexpected provider call")
			})}
			summary, err := RunEvidenceInventory(context.Background(), EvidenceInventoryOptions{
				Source:    EvidenceInventorySource{SourceLibraryID: "source:synthetic", Snapshot: EvidenceSnapshotReceipt{ID: "snapshot:complete", CompletenessState: "complete"}, Assets: []EvidenceInventorySourceAsset{{AssetID: "asset:invalid", Location: &coordinate}}},
				OutputDir: root,
				Geoapify:  config,
			})
			if err != nil || summary.State != inventoryStateStopped || summary.StopReason != EvidenceInventoryStopUnsafe || summary.Counts.ProviderEligibleImages != 0 || providerCalls != 0 {
				t.Fatalf("non-finite summary = %#v error=%v", summary, err)
			}
			data, err := os.ReadFile(filepath.Join(root, "manifest.json"))
			var manifest evidenceInventoryManifest
			if err != nil || json.Unmarshal(data, &manifest) != nil || len(manifest.Assets) != 1 || manifest.Assets[0].Location != nil || !manifest.Assets[0].LocationInvalid {
				t.Fatalf("non-finite manifest = %q parsed=%#v error=%v", data, manifest, err)
			}
		})
	}
}

func TestEvidenceInventoryNormalisesSignedZeroCellAxes(t *testing.T) {
	assets := []evidenceInventoryAsset{
		{Location: &Coordinate{Latitude: math.Copysign(0, -1), Longitude: 4.89}},
		{Location: &Coordinate{Latitude: 0, Longitude: 4.89}},
	}
	populateEvidenceCells(assets)
	if assets[0].CellKey != assets[1].CellKey || assets[0].CellPopulation != 2 || assets[1].CellPopulation != 2 {
		t.Fatalf("signed-zero cells = %#v", assets)
	}
}

func TestEvidenceCampaignSelectionIsStableAndRepresentative(t *testing.T) {
	manifest := syntheticCampaignManifest(410)
	targets := syntheticCampaignTargets(manifest.Assets[:6])
	state, err := selectEvidenceCampaign(&manifest, targets, evidenceDigest([]byte("targets")))
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Cases) != 400 || state.Counts.TargetedCases != 6 || state.Counts.SparseCases != 94 || state.Counts.RandomCases != 300 || state.Counts.CoverageGaps != 0 {
		t.Fatalf("campaign selection = %#v", state.Counts)
	}
	canaries := map[string]int{}
	for _, row := range state.Cases {
		if row.Canary {
			canaries[row.Stratum]++
		}
	}
	if canaries["targeted"] != 1 || canaries["sparse"] != 1 || canaries["random"] != 1 {
		t.Fatalf("canary strata = %#v", canaries)
	}
	second := syntheticCampaignManifest(410)
	again, err := selectEvidenceCampaign(&second, targets, evidenceDigest([]byte("targets")))
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(state.Cases)
	secondJSON, _ := json.Marshal(again.Cases)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("campaign selection changed for the same snapshot and targets")
	}
	partial := syntheticCampaignManifest(410)
	partialState, err := selectEvidenceCampaign(&partial, targets[:5], evidenceDigest([]byte("partial-targets")))
	if err != nil {
		t.Fatal(err)
	}
	wantCategory := targetCategories[5]
	if partialState.Counts.CoverageGaps != 1 || len(partialState.CoverageGaps) != 1 || partialState.CoverageGaps[0].Kind != "target_category" || partialState.CoverageGaps[0].Label != wantCategory || partialState.CoverageGaps[0].Count != 1 {
		t.Fatalf("target coverage gap = %#v", partialState.CoverageGaps)
	}
}

func TestEvidenceCampaignSmallCorpusKeepsSelectionAndCanaryGap(t *testing.T) {
	manifest := syntheticCampaignManifest(2)
	targets := []EvidenceCampaignTarget{{AssetID: manifest.Assets[0].AssetID, Category: targetCategories[0]}}
	state, err := selectEvidenceCampaign(&manifest, targets, evidenceDigest([]byte("small-targets")))
	if err != nil || len(state.Cases) != 2 {
		t.Fatalf("small campaign = %#v error=%v", state, err)
	}
	foundCanaryGap := false
	for _, gap := range state.CoverageGaps {
		if gap.Kind == "canary_stratum" && gap.Label == "random" && gap.Count == 1 {
			foundCanaryGap = true
		}
	}
	if !foundCanaryGap {
		t.Fatalf("small campaign gaps = %#v", state.CoverageGaps)
	}
}

func TestEvidenceCampaignCanariesThenWarmRestartMakeNoDuplicateCalls(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	inventoryRoot := privateTempDir(t)
	manifest := syntheticCampaignManifest(101)
	geoapify := syntheticCampaignGeoapify(server)
	configureSyntheticCampaignManifest(t, &manifest, geoapify)
	if err := saveEvidenceManifest(filepath.Join(inventoryRoot, "manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(privateTempDir(t), "targets.json")
	targets := syntheticCampaignTargets(manifest.Assets[:6])
	targetBytes, _ := json.Marshal(targets)
	if err := os.WriteFile(targetsPath, targetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	clock := &syntheticCampaignClock{at: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	log := &syntheticEvidenceLog{}
	appleCalls := 0
	evidenceRunner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		appleCalls++
		request, _ := appleRequestJSON(input, radius)
		return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
	}, now: clock.Now}
	runtime := campaignRuntime{
		now:   clock.Now,
		sleep: clock.Sleep,
		runOperation: func(ctx context.Context, opts EvidenceOptions, operation evidenceOperation) (EvidenceResult, error) {
			return runEvidenceOperations(ctx, opts, evidenceRunner, []evidenceOperation{operation})
		},
	}
	opts := EvidenceCampaignOptions{
		ManifestPath: filepath.Join(inventoryRoot, "manifest.json"),
		TargetsPath:  targetsPath,
		OutputDir:    privateTempDir(t),
		CacheDir:     "",
		Resume:       true,
		Geoapify:     geoapify,
		LogSink:      log,
	}
	opts.CacheDir = filepath.Join(opts.OutputDir, "cache")
	canary, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if canary.State != campaignStateStopped || canary.Phase != campaignPhaseCanary || canary.StopReason != "" || appleCalls != 3 || len(*requests) != 6 {
		t.Fatalf("canary boundary = %#v Apple=%d HTTP=%d", canary, appleCalls, len(*requests))
	}
	if !log.contains("place_evidence_campaign_case", "phase=canary", "outcome=complete", "duration_ms=") || !log.contains("place_evidence_campaign_phase", "phase=canary", "duration_ms=") {
		t.Fatalf("campaign observability = %#v", log.lines)
	}
	atLimit, err := readEvidenceManifest(opts.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(atLimit.Campaign.CoverageGaps) != 1 || atLimit.Campaign.CoverageGaps[0].Kind != "corpus" || atLimit.Campaign.CoverageGaps[0].Count != 299 {
		t.Fatalf("corpus coverage gap = %#v", atLimit.Campaign.CoverageGaps)
	}
	atLimit.Campaign.Phase = campaignPhaseGeo
	atLimit.Campaign.Counts.GeoapifyRequestsToday = geoapifyDailyLimit
	appleBefore, httpBefore := appleCalls, len(*requests)
	cached, err := runCampaignEvidence(context.Background(), opts, &atLimit, 0, runtime, []evidenceOperation{evidenceOperationReverse})
	if err != nil || len(cached.Records) != 1 || !cached.Records[0].Cached || atLimit.Campaign.Counts.GeoapifyRequestsToday != geoapifyDailyLimit || appleCalls != appleBefore || len(*requests) != httpBefore {
		t.Fatalf("at-limit cached resume = %#v count=%d Apple=%d HTTP=%d error=%v", cached, atLimit.Campaign.Counts.GeoapifyRequestsToday, appleCalls, len(*requests), err)
	}
	warm := atLimit
	warm.Campaign.Cases[0].AppleComplete = false
	warm.Campaign.Cases[0].GeoComplete = true
	warmManifestRoot := privateTempDir(t)
	warmOpts := opts
	warmOpts.ManifestPath = filepath.Join(warmManifestRoot, "manifest.json")
	if err := saveEvidenceManifest(warmOpts.ManifestPath, &warm); err != nil {
		t.Fatal(err)
	}
	sleepsBefore := len(clock.slept)
	lastAppleStart := warm.Campaign.LastAppleStart
	if err := runCampaignCases(context.Background(), warmOpts, &warm, runtime, true, false); err != nil {
		t.Fatal(err)
	}
	if appleCalls != appleBefore || len(*requests) != httpBefore || len(clock.slept) != sleepsBefore || warm.Campaign.LastAppleStart != lastAppleStart {
		t.Fatalf("cached Apple resume recorded a false start: Apple=%d HTTP=%d sleeps=%d last=%q", appleCalls, len(*requests), len(clock.slept), warm.Campaign.LastAppleStart)
	}
	for _, entry := range []string{"headers.raw", "response.raw", "record.json", "request.raw"} {
		matches, err := filepath.Glob(filepath.Join(opts.OutputDir, "cases", "*", "*", "*", "*", entry))
		if err != nil || len(matches) == 0 {
			t.Fatalf("raw canary %s matches=%#v error=%v", entry, matches, err)
		}
	}
	held, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil || held.Phase != campaignPhaseCanary || held.State != campaignStateStopped || appleCalls != 3 || len(*requests) != 6 {
		t.Fatalf("uninspected canary gate = %#v Apple=%d HTTP=%d error=%v", held, appleCalls, len(*requests), err)
	}
	opts.InspectionReceiptPath = writeSyntheticInspectionReceipt(t, opts.ManifestPath)
	complete, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if complete.State != campaignStateComplete || complete.Phase != campaignPhaseCompare || complete.Counts.CompleteCases != 101 || complete.Counts.CacheReusedCases != 101 {
		failedManifest, readErr := readEvidenceManifest(opts.ManifestPath)
		if readErr == nil {
			for index := range failedManifest.Campaign.Cases {
				if restartErr := reopenCampaignCase(opts, &failedManifest, index); restartErr != nil {
					t.Fatalf("complete campaign = %#v; restart case %d: %v", complete, index+1, restartErr)
				}
			}
		}
		t.Fatalf("complete campaign = %#v", complete)
	}
	if appleCalls != 101 || len(*requests) != 202 {
		t.Fatalf("campaign calls after restart: Apple=%d HTTP=%d", appleCalls, len(*requests))
	}
	if clock.sleepCount(250*time.Millisecond) == 0 {
		t.Fatalf("Geoapify governor recorded no 250ms start interval: %#v", clock.slept)
	}
	comparison, err := os.ReadFile(filepath.Join(opts.OutputDir, "comparison.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CONTENT-SAFE COMPARISON %s", comparison)
	manifest, err = readEvidenceManifest(opts.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	corruptFirstCampaignCache(t, filepath.Join(opts.OutputDir, "cache"), geoapifyReverseOperation)
	manifest.Campaign.RestartComplete = false
	manifest.Campaign.Counts.CacheReusedCases = 0
	for index := range manifest.Campaign.Cases {
		manifest.Campaign.Cases[index].RestartChecked = false
	}
	if err := saveEvidenceManifest(opts.ManifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	stopped, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State != campaignStateStopped || stopped.Phase != campaignPhaseRestart || stopped.StopReason != evidenceStopCacheIncomplete {
		t.Fatalf("incomplete restart cache = %#v", stopped)
	}
	if appleCalls != 101 || len(*requests) != 202 {
		t.Fatalf("incomplete cache triggered provider work: Apple=%d HTTP=%d", appleCalls, len(*requests))
	}
}

func TestEvidenceCampaignStopsChangedTargetsAndDailyLimitBeforeProviderCalls(t *testing.T) {
	server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
		"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
		"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
	})
	defer server.Close()
	root := privateTempDir(t)
	manifest := syntheticCampaignManifest(101)
	geoapify := syntheticCampaignGeoapify(server)
	configureSyntheticCampaignManifest(t, &manifest, geoapify)
	manifestPath := filepath.Join(root, "manifest.json")
	if err := saveEvidenceManifest(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(root, "targets.json")
	targets := syntheticCampaignTargets(manifest.Assets[:6])
	targetBytes, _ := json.Marshal(targets)
	if err := os.WriteFile(targetsPath, targetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	clock := &syntheticCampaignClock{at: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)}
	appleCalls := 0
	evidenceRunner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
		appleCalls++
		request, _ := appleRequestJSON(input, radius)
		return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
	}, now: clock.Now}
	runtime := campaignRuntime{now: clock.Now, sleep: clock.Sleep, runOperation: func(ctx context.Context, opts EvidenceOptions, operation evidenceOperation) (EvidenceResult, error) {
		return runEvidenceOperations(ctx, opts, evidenceRunner, []evidenceOperation{operation})
	}}
	opts := EvidenceCampaignOptions{ManifestPath: manifestPath, TargetsPath: targetsPath, OutputDir: privateTempDir(t), Resume: true, Geoapify: geoapify}
	if _, err := runEvidenceCampaign(context.Background(), opts, runtime); err != nil {
		t.Fatal(err)
	}
	canaryHTTP := len(*requests)
	reordered := append([]EvidenceCampaignTarget(nil), targets...)
	reordered[0], reordered[1] = reordered[1], reordered[0]
	changedBytes, _ := json.Marshal(reordered)
	if err := os.WriteFile(targetsPath, changedBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	changed, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if changed.StopReason != "targets_changed" || len(*requests) != canaryHTTP || appleCalls != 3 {
		t.Fatalf("changed targets boundary = %#v Apple=%d HTTP=%d", changed, appleCalls, len(*requests))
	}
	if err := os.WriteFile(targetsPath, targetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err = readEvidenceManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Campaign.CanariesComplete = true
	manifest.Campaign.CanariesInspected = true
	manifest.Campaign.AppleComplete = true
	manifest.Campaign.GeoapifyComplete = false
	manifest.Campaign.GeoapifyDate = clock.Now().UTC().Format("2006-01-02")
	manifest.Campaign.Counts.GeoapifyRequestsToday = geoapifyDailyLimit
	manifest.Campaign.StopReason = ""
	for index := range manifest.Campaign.Cases {
		manifest.Campaign.Cases[index].AppleComplete = true
		manifest.Campaign.Cases[index].GeoComplete = false
	}
	if err := saveEvidenceManifest(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	limited, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil {
		t.Fatal(err)
	}
	if limited.StopReason != evidenceStopRateLimited || len(*requests) != canaryHTTP || appleCalls != 3 {
		t.Fatalf("daily limit boundary = %#v Apple=%d HTTP=%d", limited, appleCalls, len(*requests))
	}
}

func TestEvidenceOperationRunsOnlyTheNamedBoundary(t *testing.T) {
	tests := []struct {
		operation EvidenceOperation
		wantApple int
		wantHTTP  int
		wantOps   []string
	}{
		{EvidenceOperationApple, 1, 0, []string{appleEvidenceOperation}},
		{EvidenceOperationGeoapifyReverse, 0, 1, []string{geoapifyReverseOperation}},
		{EvidenceOperationGeoapifyNearby, 0, 1, []string{geoapifyNearbyOperation}},
		{EvidenceOperationAll, 1, 2, []string{appleEvidenceOperation, geoapifyReverseOperation, geoapifyNearbyOperation}},
	}
	for _, test := range tests {
		t.Run(string(test.operation), func(t *testing.T) {
			server, requests := syntheticEvidenceServer(t, map[string]syntheticHTTPResponse{
				"/reverse": {status: http.StatusOK, body: syntheticReverseResponse},
				"/nearby":  {status: http.StatusOK, body: syntheticNearbyResponse},
			})
			defer server.Close()
			appleCalls := 0
			runner := evidenceRunner{callApple: func(_ context.Context, input Input, radius float64) appleBoundaryOutput {
				appleCalls++
				request, _ := appleRequestJSON(input, radius)
				return appleBoundaryOutput{Request: request, Response: []byte(syntheticAppleResponse)}
			}}
			selected, err := selectedEvidenceOperations(test.operation)
			if err != nil {
				t.Fatal(err)
			}
			opts := syntheticEvidenceOptions(server, syntheticEvidenceInput(52.36, 4.89), filepath.Join(t.TempDir(), "output"), filepath.Join(t.TempDir(), "cache"))
			result, err := runEvidenceOperations(context.Background(), opts, runner, selected)
			if err != nil {
				t.Fatal(err)
			}
			gotOps := make([]string, 0, len(result.Records))
			for _, record := range result.Records {
				gotOps = append(gotOps, record.Operation)
			}
			if strings.Join(gotOps, ",") != strings.Join(test.wantOps, ",") || appleCalls != test.wantApple || len(*requests) != test.wantHTTP {
				t.Fatalf("operation result=%#v Apple=%d HTTP=%d", gotOps, appleCalls, len(*requests))
			}
		})
	}
	if _, err := ParseEvidenceOperation("unknown"); err == nil {
		t.Fatal("unknown operation passed")
	}
}

func syntheticInventoryGeoapify() ConfiguredGeoapifyEvidence {
	return ConfiguredGeoapifyEvidence{ProviderIdentity: "synthetic-osm", ReverseEndpoint: "https://geo.example.com/reverse", NearbyEndpoint: "https://geo.example.com/nearby", CredentialReference: "SYNTHETIC_OSM_KEY", CredentialParameter: "syntheticKey", NearbyCategories: []string{"natural"}, ReverseLimit: 3, NearbyLimit: 4}
}

func syntheticCampaignGeoapify(server *httptest.Server) ConfiguredGeoapifyEvidence {
	config := syntheticInventoryGeoapify()
	config.ReverseEndpoint = server.URL + "/reverse"
	config.NearbyEndpoint = server.URL + "/nearby"
	config.Credential = "synthetic-secret"
	config.HTTPClient = server.Client()
	return config
}

func syntheticCampaignManifest(count int) evidenceInventoryManifest {
	manifest := evidenceInventoryManifest{Version: evidenceInventoryVersion, State: inventoryStateComplete, SourceLibraryID: "source:synthetic", Snapshot: EvidenceSnapshotReceipt{ID: "snapshot:synthetic", CompletenessState: "complete"}, RadiusMeters: evidenceCampaignRadiusMeters}
	for index := 0; index < count; index++ {
		assetID := "asset:" + fmt.Sprintf("%04d", index)
		manifest.Assets = append(manifest.Assets, evidenceInventoryAsset{AssetID: assetID, TakenAt: "2026-07-01T10:00:00Z", Location: &Coordinate{Latitude: 10 + float64(index)/10, Longitude: 20 + float64(index%17)/10}, RandomDigest: evidenceRandomDigest(manifest.Snapshot.ID, assetID)})
	}
	populateEvidenceCells(manifest.Assets)
	return manifest
}

func configureSyntheticCampaignManifest(t *testing.T, manifest *evidenceInventoryManifest, config ConfiguredGeoapifyEvidence) {
	t.Helper()
	manifest.Provider = evidenceInventoryProviderFromConfig(config)
	for index := range manifest.Assets {
		asset := &manifest.Assets[index]
		input := Input{AssetID: asset.AssetID, TakenAt: asset.TakenAt, Location: *asset.Location}
		requests, err := evidenceInventoryRequests(context.Background(), input, config)
		if err != nil {
			t.Fatal(err)
		}
		asset.Requests = requests
	}
}

func writeSyntheticInspectionReceipt(t *testing.T, manifestPath string) string {
	t.Helper()
	manifest, err := readEvidenceManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	receipt := map[string]any{"manifest_digest": manifest.ManifestDigest, "canary_evidence_digest": manifest.Campaign.CanaryEvidenceDigest, "inspected": true}
	data, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateTempDir(t), "inspection.json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func syntheticCampaignTargets(assets []evidenceInventoryAsset) []EvidenceCampaignTarget {
	targets := make([]EvidenceCampaignTarget, 0, len(targetCategories))
	for index, category := range targetCategories {
		targets = append(targets, EvidenceCampaignTarget{AssetID: assets[index].AssetID, Category: category})
	}
	return targets
}

type syntheticCampaignClock struct {
	at    time.Time
	slept []time.Duration
}

func privateTempDir(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func corruptFirstCampaignCache(t *testing.T, cacheDir, operation string) {
	t.Helper()
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(cacheDir, entry.Name(), "record.json"))
		if err != nil {
			t.Fatal(err)
		}
		var record EvidenceRecord
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.Operation != operation {
			continue
		}
		if err := os.WriteFile(filepath.Join(cacheDir, entry.Name(), "response.raw"), []byte(`{"features":`), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("cache operation %q is missing", operation)
}

func (c *syntheticCampaignClock) Now() time.Time {
	return c.at
}

func (c *syntheticCampaignClock) Sleep(_ context.Context, duration time.Duration) error {
	c.slept = append(c.slept, duration)
	c.at = c.at.Add(duration)
	return nil
}

func (c *syntheticCampaignClock) sleepCount(duration time.Duration) int {
	count := 0
	for _, slept := range c.slept {
		if slept == duration {
			count++
		}
	}
	return count
}
