package place

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	campaignStateComplete  = "complete"
	campaignStateStopped   = "stopped"
	campaignPhaseCanary    = "canary"
	campaignPhaseApple     = "apple_ramp"
	campaignPhaseGeo       = "geoapify"
	campaignPhaseRestart   = "restart"
	campaignPhaseCompare   = "comparison"
	campaignTargetCases    = 400
	campaignSparseCases    = 94
	campaignRandomCases    = 300
	geoapifyDailyLimit     = 2400
	evidenceAdapterCommand = "photos-place-evidence"
)

var targetCategories = []string{
	"dense_venue",
	"camera_vs_depicted_landmark",
	"park_or_trail",
	"mainland_china",
	"sparse_rural",
	"apparent_no_result_risk",
}

type EvidenceCampaignOptions struct {
	ManifestPath          string
	TargetsPath           string
	InspectionReceiptPath string
	OutputDir             string
	CacheDir              string
	Resume                bool
	Geoapify              ConfiguredGeoapifyEvidence
	LogSink               EvidenceLogSink
}

type EvidenceCampaignSummary struct {
	State          string                 `json:"state"`
	Phase          string                 `json:"phase"`
	ManifestDigest string                 `json:"manifest_digest"`
	Counts         EvidenceCampaignCounts `json:"counts"`
	StopReason     string                 `json:"stop_reason"`
}

type EvidenceCampaignCounts struct {
	TargetedCases         int `json:"targeted_cases"`
	SparseCases           int `json:"sparse_cases"`
	RandomCases           int `json:"random_cases"`
	CoverageGaps          int `json:"coverage_gaps"`
	CompleteCases         int `json:"complete_cases"`
	StoppedCases          int `json:"stopped_cases"`
	CacheReusedCases      int `json:"cache_reused_cases"`
	GeoapifyRequestsToday int `json:"geoapify_requests_today"`
}

type EvidenceCampaignTarget struct {
	AssetID  string `json:"asset_id"`
	Category string `json:"category"`
}

type EvidenceCampaignMetrics struct {
	AppleRuns                            int     `json:"apple_runs"`
	AppleDurationMilliseconds            float64 `json:"apple_duration_milliseconds"`
	AppleMinStartIntervalMilliseconds    float64 `json:"apple_min_start_interval_milliseconds"`
	AppleMaxConcurrency                  int     `json:"apple_max_concurrency"`
	GeoapifyRequests                     int     `json:"geoapify_requests"`
	GeoapifyDurationMilliseconds         float64 `json:"geoapify_duration_milliseconds"`
	GeoapifyMinStartIntervalMilliseconds float64 `json:"geoapify_min_start_interval_milliseconds"`
	GeoapifyMaxConcurrency               int     `json:"geoapify_max_concurrency"`
}

type evidenceCampaignState struct {
	TargetsDigest                 string                  `json:"targets_digest"`
	Phase                         string                  `json:"phase"`
	State                         string                  `json:"state"`
	StopReason                    string                  `json:"stop_reason"`
	Counts                        EvidenceCampaignCounts  `json:"counts"`
	CoverageGaps                  []evidenceCoverageGap   `json:"coverage_gaps,omitempty"`
	Metrics                       EvidenceCampaignMetrics `json:"metrics"`
	StopReasons                   map[string]int          `json:"stop_reasons,omitempty"`
	Cases                         []evidenceCampaignCase  `json:"cases"`
	CanariesComplete              bool                    `json:"canaries_complete"`
	CanariesInspected             bool                    `json:"canaries_inspected"`
	CanaryEvidenceDigest          string                  `json:"canary_evidence_digest,omitempty"`
	CanaryInspectionReceiptDigest string                  `json:"canary_inspection_receipt_digest,omitempty"`
	AppleComplete                 bool                    `json:"apple_complete"`
	GeoapifyComplete              bool                    `json:"geoapify_complete"`
	RestartComplete               bool                    `json:"restart_complete"`
	GeoapifyDate                  string                  `json:"geoapify_date"`
	LastAppleStart                string                  `json:"last_apple_start,omitempty"`
	LastGeoapifyStart             string                  `json:"last_geoapify_start,omitempty"`
}

type evidenceCampaignCase struct {
	AssetID        string `json:"asset_id"`
	Stratum        string `json:"stratum"`
	TargetCategory string `json:"target_category,omitempty"`
	Canary         bool   `json:"canary"`
	AppleComplete  bool   `json:"apple_complete"`
	GeoComplete    bool   `json:"geoapify_complete"`
	RestartChecked bool   `json:"restart_checked"`
}

type campaignRuntime struct {
	now          func() time.Time
	sleep        func(context.Context, time.Duration) error
	runOperation func(context.Context, EvidenceOptions, evidenceOperation) (EvidenceResult, error)
}

func RunEvidenceCampaign(ctx context.Context, opts EvidenceCampaignOptions) (EvidenceCampaignSummary, error) {
	return runEvidenceCampaign(ctx, opts, campaignRuntime{now: time.Now, sleep: sleepContext, runOperation: runAdapterEvidenceOperation})
}

func runEvidenceCampaign(ctx context.Context, opts EvidenceCampaignOptions, runtime campaignRuntime) (EvidenceCampaignSummary, error) {
	if !opts.Resume {
		return EvidenceCampaignSummary{}, errors.New("place evidence campaign requires --resume")
	}
	if err := ensurePrivateOutputRoot(opts.OutputDir); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	if err := ensurePrivateInputFile(opts.ManifestPath); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	if err := ensurePrivateInputFile(opts.TargetsPath); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	if err := validateConfiguredGeoapifyShape(opts.Geoapify); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	if strings.TrimSpace(opts.CacheDir) == "" {
		opts.CacheDir = filepath.Join(opts.OutputDir, "cache")
	}
	if err := ensurePrivateEvidenceCacheRoot(opts.CacheDir); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	manifest, err := readEvidenceManifest(opts.ManifestPath)
	if err != nil {
		return EvidenceCampaignSummary{}, err
	}
	if !manifestProviderMatches(manifest.Provider, opts.Geoapify) {
		return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseCanary, "mismatched")
	}
	targetBytes, targets, err := readCampaignTargets(opts.TargetsPath)
	if err != nil {
		return EvidenceCampaignSummary{}, err
	}
	targetsDigest := evidenceDigest(targetBytes)
	if manifest.Campaign == nil {
		state, err := selectEvidenceCampaign(&manifest, targets, targetsDigest)
		if err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseCanary, campaignStopReason(err))
		}
		manifest.Campaign = state
		if err := saveEvidenceManifest(opts.ManifestPath, &manifest); err != nil {
			return EvidenceCampaignSummary{}, err
		}
	} else if manifest.Campaign.TargetsDigest != targetsDigest {
		return stopCampaign(opts.ManifestPath, &manifest, manifest.Campaign.Phase, "targets_changed")
	}
	state := manifest.Campaign
	if state.CanariesComplete {
		currentCanaryDigest, err := digestCampaignCanaries(opts.OutputDir, state.Cases)
		if err != nil || currentCanaryDigest != state.CanaryEvidenceDigest {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseCanary, "mismatched")
		}
	}
	if state.StopReason != "" {
		return campaignSummary(manifest), nil
	}
	if runtime.now == nil {
		runtime.now = time.Now
	}
	if runtime.sleep == nil {
		runtime.sleep = sleepContext
	}
	if runtime.runOperation == nil {
		return EvidenceCampaignSummary{}, errors.New("place evidence operation boundary is required")
	}
	if !state.CanariesComplete {
		state.Phase = campaignPhaseCanary
		if err := runEvidencePhase(opts.LogSink, campaignPhaseCanary, func() error { return runCampaignCases(ctx, opts, &manifest, runtime, true, true) }); err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseCanary, campaignStopReason(err))
		}
		state.CanariesComplete = true
		state.State = campaignStateStopped
		state.StopReason = ""
		state.CanaryEvidenceDigest, err = digestCampaignCanaries(opts.OutputDir, state.Cases)
		if err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseCanary, campaignStopReason(err))
		}
		if err := saveEvidenceManifest(opts.ManifestPath, &manifest); err != nil {
			return EvidenceCampaignSummary{}, err
		}
		return campaignSummary(manifest), nil
	}
	if !state.CanariesInspected {
		if strings.TrimSpace(opts.InspectionReceiptPath) == "" {
			logEvidence(opts.LogSink, false, "place_evidence_campaign_phase", "phase=canary_inspection", "outcome=required", "duration_ms=0")
			return campaignSummary(manifest), nil
		}
		receiptDigest, err := validateCanaryInspectionReceipt(opts.InspectionReceiptPath, manifest.ManifestDigest, state.CanaryEvidenceDigest)
		if err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseCanary, campaignStopReason(err))
		}
		state.CanariesInspected = true
		state.CanaryInspectionReceiptDigest = receiptDigest
		if err := saveEvidenceManifest(opts.ManifestPath, &manifest); err != nil {
			return EvidenceCampaignSummary{}, err
		}
	}
	state.Phase = campaignPhaseApple
	if !state.AppleComplete {
		if err := runEvidencePhase(opts.LogSink, campaignPhaseApple, func() error { return runCampaignCases(ctx, opts, &manifest, runtime, false, false) }); err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseApple, campaignStopReason(err))
		}
		state.AppleComplete = true
	}
	state.Phase = campaignPhaseGeo
	if !state.GeoapifyComplete {
		if err := runEvidencePhase(opts.LogSink, campaignPhaseGeo, func() error { return runGeoapifyCases(ctx, opts, &manifest, runtime) }); err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseGeo, campaignStopReason(err))
		}
		state.GeoapifyComplete = true
	}
	state.Phase = campaignPhaseRestart
	if !state.RestartComplete {
		if err := runEvidencePhase(opts.LogSink, campaignPhaseRestart, func() error { return checkCampaignRestart(ctx, opts, &manifest, runtime) }); err != nil {
			return stopCampaign(opts.ManifestPath, &manifest, campaignPhaseRestart, campaignStopReason(err))
		}
		state.RestartComplete = true
	}
	state.Phase = campaignPhaseCompare
	state.State = campaignStateComplete
	state.StopReason = ""
	state.Counts.CompleteCases = len(state.Cases)
	if err := runEvidencePhase(opts.LogSink, campaignPhaseCompare, func() error { return writeCampaignComparison(opts.OutputDir, manifest) }); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	if err := saveEvidenceManifest(opts.ManifestPath, &manifest); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	return campaignSummary(manifest), nil
}

func manifestProviderMatches(manifest evidenceInventoryProvider, config ConfiguredGeoapifyEvidence) bool {
	return manifest.Identity == strings.TrimSpace(config.ProviderIdentity) &&
		manifest.ReverseEndpoint == strings.TrimSpace(config.ReverseEndpoint) &&
		manifest.NearbyEndpoint == strings.TrimSpace(config.NearbyEndpoint) &&
		manifest.CredentialReference == strings.TrimSpace(config.CredentialReference) &&
		manifest.CredentialParameter == strings.TrimSpace(config.CredentialParameter) &&
		manifest.ReverseLimit == config.ReverseLimit && manifest.NearbyLimit == config.NearbyLimit &&
		slices.Equal(manifest.NearbyCategories, config.NearbyCategories)
}

func runCampaignCases(ctx context.Context, opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, runtime campaignRuntime, canariesOnly, includeGeo bool) error {
	for index := range manifest.Campaign.Cases {
		campaignCase := manifest.Campaign.Cases[index]
		if campaignCase.Canary != canariesOnly || campaignCase.AppleComplete && (!includeGeo || campaignCase.GeoComplete) {
			continue
		}
		appleCached, err := campaignCaseOperationCached(opts, manifest, index, evidenceOperationApple)
		if err != nil {
			return err
		}
		if !appleCached {
			interval := 15 * time.Second
			if canariesOnly {
				interval = 60 * time.Second
			}
			var previous time.Time
			if manifest.Campaign.LastAppleStart != "" {
				parsed, err := time.Parse(time.RFC3339Nano, manifest.Campaign.LastAppleStart)
				if err != nil {
					return errEvidenceCacheIncomplete
				}
				previous = parsed
			}
			if err := waitForStart(ctx, runtime, previous, interval); err != nil {
				return err
			}
			if !previous.IsZero() {
				setMinimumDuration(&manifest.Campaign.Metrics.AppleMinStartIntervalMilliseconds, runtime.now().UTC().Sub(previous))
			}
			manifest.Campaign.Metrics.AppleMaxConcurrency = 1
			manifest.Campaign.LastAppleStart = runtime.now().UTC().Format(time.RFC3339Nano)
			if err := saveEvidenceManifest(opts.ManifestPath, manifest); err != nil {
				return err
			}
		}
		operations := []evidenceOperation{evidenceOperationApple}
		if includeGeo {
			operations = append(operations, evidenceOperationReverse, evidenceOperationNearby)
		}
		started := time.Now()
		result, err := runCampaignEvidence(ctx, opts, manifest, index, runtime, operations)
		logEvidenceCase(opts.LogSink, manifest.Campaign.Phase, index, "asset", started, err)
		if err != nil {
			return campaignEvidenceError(result, err)
		}
		manifest.Campaign.Cases[index].AppleComplete = true
		manifest.Campaign.Cases[index].GeoComplete = includeGeo
		if err := saveEvidenceManifest(opts.ManifestPath, manifest); err != nil {
			return err
		}
	}
	return nil
}

func runGeoapifyCases(ctx context.Context, opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, runtime campaignRuntime) error {
	for index := range manifest.Campaign.Cases {
		if manifest.Campaign.Cases[index].GeoComplete {
			continue
		}
		for _, operation := range []evidenceOperation{evidenceOperationReverse, evidenceOperationNearby} {
			started := time.Now()
			result, err := runCampaignEvidence(ctx, opts, manifest, index, runtime, []evidenceOperation{operation})
			logEvidenceCase(opts.LogSink, manifest.Campaign.Phase, index, string(operation), started, err)
			if err != nil {
				return campaignEvidenceError(result, err)
			}
		}
		manifest.Campaign.Cases[index].GeoComplete = true
		if err := saveEvidenceManifest(opts.ManifestPath, manifest); err != nil {
			return err
		}
	}
	return nil
}

func runCampaignEvidence(ctx context.Context, opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, index int, runtime campaignRuntime, operations []evidenceOperation) (EvidenceResult, error) {
	row := inventoryAsset(manifest.Assets, manifest.Campaign.Cases[index].AssetID)
	if row == nil || row.Location == nil {
		return EvidenceResult{}, errors.New("mismatched")
	}
	input := Input{AssetID: row.AssetID, TakenAt: row.TakenAt, Location: *row.Location}
	caseDir := filepath.Join(opts.OutputDir, "cases", fmt.Sprintf("%04d", index+1), manifest.Campaign.Phase)
	if err := ensurePrivateEvidenceDirectory(caseDir); err != nil {
		return EvidenceResult{}, err
	}
	combined := EvidenceResult{State: evidenceStateComplete, CoordinateVariant: evidenceCoordinateVariant}
	for _, operation := range operations {
		operationDir := filepath.Join(caseDir, string(operation))
		if err := ensurePrivateEvidenceDirectory(operationDir); err != nil {
			return combined, err
		}
		capture, found, cacheErr := campaignCachedCapture(opts, manifest, row, input, operation)
		var result EvidenceResult
		var err error
		if cacheErr != nil {
			return combined, cacheErr
		}
		if found {
			if err := writeEvidenceCapture(operationDir, &capture); err != nil {
				return combined, err
			}
			result = EvidenceResult{State: evidenceStateComplete, CoordinateVariant: evidenceCoordinateVariant, Records: []EvidenceRecord{capture.record}}
		} else {
			if operation != evidenceOperationApple {
				if err := governGeoapifyStart(ctx, opts.ManifestPath, manifest, runtime); err != nil {
					return combined, err
				}
			}
			result, err = runtime.runOperation(ctx, EvidenceOptions{Input: input, CoordinateVariant: evidenceCoordinateVariant, RadiusMeters: manifest.RadiusMeters, OutputDir: operationDir, CacheDir: opts.CacheDir, Geoapify: opts.Geoapify}, operation)
		}
		if len(result.Records) > 0 && !campaignResultMatches(result, manifest, row, input, operation) {
			return combined, errors.New("mismatched")
		}
		recordCampaignMetrics(manifest.Campaign, result)
		combined.Records = append(combined.Records, result.Records...)
		combined.StopReasons = append(combined.StopReasons, result.StopReasons...)
		if err != nil {
			combined.State = evidenceStateStopped
			return combined, err
		}
		if result.State != evidenceStateComplete || len(result.Records) != 1 {
			return combined, errors.New("mismatched")
		}
	}
	return combined, nil
}

func checkCampaignRestart(ctx context.Context, opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, runtime campaignRuntime) error {
	before := manifest.Campaign.Counts.GeoapifyRequestsToday
	for index := range manifest.Campaign.Cases {
		started := time.Now()
		err := reopenCampaignCase(opts, manifest, index)
		logEvidenceCase(opts.LogSink, campaignPhaseRestart, index, "cache_reopen", started, err)
		if err != nil {
			return err
		}
		manifest.Campaign.Cases[index].RestartChecked = true
		manifest.Campaign.Counts.CacheReusedCases++
	}
	if manifest.Campaign.Counts.GeoapifyRequestsToday != before {
		return errEvidenceCacheIncomplete
	}
	return nil
}

func reopenCampaignCase(opts EvidenceCampaignOptions, manifest *evidenceInventoryManifest, index int) error {
	row := inventoryAsset(manifest.Assets, manifest.Campaign.Cases[index].AssetID)
	if row == nil || row.Location == nil {
		return errEvidenceCacheIncomplete
	}
	input := Input{AssetID: row.AssetID, TakenAt: row.TakenAt, Location: *row.Location}
	for _, operation := range []evidenceOperation{evidenceOperationApple, evidenceOperationReverse, evidenceOperationNearby} {
		capture, found, err := campaignCachedCapture(opts, manifest, row, input, operation)
		request := inventoryRequestForOperation(row.Requests, operation)
		if !found || err != nil || !capture.record.Cached || capture.record.CacheIdentity != request.CacheIdentity {
			return fmt.Errorf("%w: %s campaign cache", errEvidenceCacheIncomplete, operation)
		}
		dir := filepath.Join(opts.OutputDir, "cases", fmt.Sprintf("%04d", index+1), campaignPhaseRestart, string(operation))
		if err := writeEvidenceCapture(dir, &capture); err != nil {
			return err
		}
	}
	return nil
}

func inventoryRequestForOperation(requests []evidenceInventoryRequest, operation evidenceOperation) *evidenceInventoryRequest {
	want := appleEvidenceOperation
	if operation == evidenceOperationReverse {
		want = geoapifyReverseOperation
	}
	if operation == evidenceOperationNearby {
		want = geoapifyNearbyOperation
	}
	for index := range requests {
		if requests[index].Operation == want {
			return &requests[index]
		}
	}
	return nil
}

func recordCampaignMetrics(state *evidenceCampaignState, result EvidenceResult) {
	for _, record := range result.Records {
		if record.Cached {
			continue
		}
		if record.ProviderIdentity == appleEvidenceProvider {
			state.Metrics.AppleRuns++
			state.Metrics.AppleDurationMilliseconds += record.DurationMilliseconds
			continue
		}
		state.Metrics.GeoapifyDurationMilliseconds += record.DurationMilliseconds
	}
}
