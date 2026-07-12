package place

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	evidenceInventoryVersion                = "photos-place-evidence-inventory-v1"
	evidenceCoordinateVariant               = "source-coordinate"
	evidenceCampaignRadiusMeters            = 150.0
	inventoryStateComplete                  = "complete"
	inventoryStateStopped                   = "stopped"
	EvidenceInventoryStopSnapshotIncomplete = "snapshot_incomplete"
	EvidenceInventoryStopUnsafe             = "inventory_unsafe"
)

type EvidenceInventoryOptions struct {
	Source    EvidenceInventorySource
	OutputDir string
	Geoapify  ConfiguredGeoapifyEvidence
	LogSink   EvidenceLogSink
}

type EvidenceInventorySource struct {
	SourceLibraryID string
	Snapshot        EvidenceSnapshotReceipt
	Assets          []EvidenceInventorySourceAsset
	StopReason      string
}

type EvidenceSnapshotReceipt struct {
	ID                       string `json:"id"`
	CompletedAt              string `json:"completed_at"`
	CompletenessState        string `json:"completeness_state"`
	CompletenessEvidenceJSON string `json:"completeness_evidence_json"`
}

type EvidenceInventorySourceAsset struct {
	AssetID  string
	TakenAt  string
	Location *Coordinate
}

type EvidenceInventorySummary struct {
	State          string                  `json:"state"`
	ManifestDigest string                  `json:"manifest_digest"`
	Counts         EvidenceInventoryCounts `json:"counts"`
	StopReason     string                  `json:"stop_reason"`
}

type EvidenceInventoryCounts struct {
	CurrentImages          int `json:"current_images"`
	ProviderEligibleImages int `json:"provider_eligible_images"`
	MissingLocationImages  int `json:"missing_location_images"`
}

type evidenceInventoryManifest struct {
	Version         string                    `json:"version"`
	ManifestDigest  string                    `json:"manifest_digest"`
	State           string                    `json:"state"`
	StopReason      string                    `json:"stop_reason"`
	SourceLibraryID string                    `json:"source_library_id"`
	Snapshot        EvidenceSnapshotReceipt   `json:"snapshot"`
	RadiusMeters    float64                   `json:"radius_meters"`
	Provider        evidenceInventoryProvider `json:"provider"`
	Counts          EvidenceInventoryCounts   `json:"counts"`
	Assets          []evidenceInventoryAsset  `json:"assets"`
	Campaign        *evidenceCampaignState    `json:"campaign,omitempty"`
}

type evidenceInventoryProvider struct {
	Identity            string   `json:"identity"`
	ReverseEndpoint     string   `json:"reverse_endpoint"`
	NearbyEndpoint      string   `json:"nearby_endpoint"`
	CredentialReference string   `json:"credential_reference"`
	CredentialParameter string   `json:"credential_parameter"`
	NearbyCategories    []string `json:"nearby_categories"`
	ReverseLimit        int      `json:"reverse_limit"`
	NearbyLimit         int      `json:"nearby_limit"`
}

type evidenceInventoryAsset struct {
	AssetID         string                     `json:"asset_id"`
	TakenAt         string                     `json:"taken_at"`
	Location        *Coordinate                `json:"location,omitempty"`
	LocationInvalid bool                       `json:"location_invalid,omitempty"`
	CellKey         string                     `json:"cell_key,omitempty"`
	CellPopulation  int                        `json:"cell_population,omitempty"`
	RandomDigest    string                     `json:"random_digest,omitempty"`
	Requests        []evidenceInventoryRequest `json:"requests,omitempty"`
	Stratum         string                     `json:"stratum,omitempty"`
	TargetCategory  string                     `json:"target_category,omitempty"`
}

type evidenceInventoryRequest struct {
	Provider      string `json:"provider"`
	Operation     string `json:"operation"`
	Bytes         string `json:"bytes"`
	SHA256        string `json:"sha256"`
	CacheIdentity string `json:"cache_identity"`
}

type evidenceCoverageGap struct {
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
	Count int    `json:"count"`
}

func RunEvidenceInventory(ctx context.Context, opts EvidenceInventoryOptions) (summary EvidenceInventorySummary, runErr error) {
	phaseStarted := time.Now()
	defer func() {
		outcome := summary.State
		if runErr != nil {
			outcome = "error"
		}
		logEvidence(opts.LogSink, runErr != nil, "place_evidence_inventory_phase", "phase=inventory", "outcome="+outcome, durationField(time.Since(phaseStarted)))
	}()
	if err := ensurePrivateOutputRoot(opts.OutputDir); err != nil {
		return EvidenceInventorySummary{}, err
	}
	if err := validateConfiguredGeoapifyShape(opts.Geoapify); err != nil {
		return EvidenceInventorySummary{}, err
	}
	manifest := evidenceInventoryManifest{
		Version:         evidenceInventoryVersion,
		State:           inventoryStateComplete,
		SourceLibraryID: strings.TrimSpace(opts.Source.SourceLibraryID),
		RadiusMeters:    evidenceCampaignRadiusMeters,
		Provider:        evidenceInventoryProviderFromConfig(opts.Geoapify),
	}
	if opts.Source.StopReason != "" {
		manifest.State = inventoryStateStopped
		manifest.StopReason = opts.Source.StopReason
		return writeEvidenceInventoryResult(opts.OutputDir, manifest)
	}
	if manifest.SourceLibraryID == "" || opts.Source.Snapshot.ID == "" || opts.Source.Snapshot.CompletenessState != inventoryStateComplete {
		manifest.State = inventoryStateStopped
		manifest.StopReason = EvidenceInventoryStopUnsafe
		return writeEvidenceInventoryResult(opts.OutputDir, manifest)
	}
	manifest.Snapshot = opts.Source.Snapshot
	manifest.Counts.CurrentImages = len(opts.Source.Assets)
	unsafeCoordinate := false
	for index, row := range opts.Source.Assets {
		itemStarted := time.Now()
		asset := evidenceInventoryAsset{AssetID: row.AssetID, TakenAt: row.TakenAt}
		if row.Location == nil {
			manifest.Counts.MissingLocationImages++
			manifest.Assets = append(manifest.Assets, asset)
			logEvidence(opts.LogSink, false, "place_evidence_inventory_item", fmt.Sprintf("item=%d", index+1), "outcome=missing_location", durationField(time.Since(itemStarted)))
			continue
		}
		asset.Location = &Coordinate{Latitude: row.Location.Latitude, Longitude: row.Location.Longitude}
		input := Input{AssetID: row.AssetID, TakenAt: row.TakenAt, Location: *asset.Location}
		if err := validateInput(input); err != nil {
			unsafeCoordinate = true
			asset.Location = nil
			asset.LocationInvalid = true
			manifest.Assets = append(manifest.Assets, asset)
			logEvidence(opts.LogSink, true, "place_evidence_inventory_item", fmt.Sprintf("item=%d", index+1), "outcome=coordinate_invalid", durationField(time.Since(itemStarted)))
			continue
		}
		requests, err := evidenceInventoryRequests(ctx, input, opts.Geoapify)
		if err != nil {
			return EvidenceInventorySummary{}, err
		}
		asset.RandomDigest = evidenceRandomDigest(opts.Source.Snapshot.ID, row.AssetID)
		asset.Requests = requests
		manifest.Counts.ProviderEligibleImages++
		manifest.Assets = append(manifest.Assets, asset)
		logEvidence(opts.LogSink, false, "place_evidence_inventory_item", fmt.Sprintf("item=%d", index+1), "outcome=eligible", durationField(time.Since(itemStarted)))
	}
	populateEvidenceCells(manifest.Assets)
	if unsafeCoordinate {
		manifest.State = inventoryStateStopped
		manifest.StopReason = EvidenceInventoryStopUnsafe
	}
	return writeEvidenceInventoryResult(opts.OutputDir, manifest)
}

func evidenceInventoryProviderFromConfig(config ConfiguredGeoapifyEvidence) evidenceInventoryProvider {
	return evidenceInventoryProvider{
		Identity:            strings.TrimSpace(config.ProviderIdentity),
		ReverseEndpoint:     strings.TrimSpace(config.ReverseEndpoint),
		NearbyEndpoint:      strings.TrimSpace(config.NearbyEndpoint),
		CredentialReference: strings.TrimSpace(config.CredentialReference),
		CredentialParameter: strings.TrimSpace(config.CredentialParameter),
		NearbyCategories:    append([]string(nil), config.NearbyCategories...),
		ReverseLimit:        config.ReverseLimit,
		NearbyLimit:         config.NearbyLimit,
	}
}

func evidenceInventoryRequests(ctx context.Context, input Input, config ConfiguredGeoapifyEvidence) ([]evidenceInventoryRequest, error) {
	apple, err := appleRequestJSON(input, evidenceCampaignRadiusMeters)
	if err != nil {
		return nil, err
	}
	_, reverse, err := geoapifyRequest(ctx, config.ReverseEndpoint, geoapifyReverseQuery(input, config.ReverseLimit))
	if err != nil {
		return nil, err
	}
	_, nearby, err := geoapifyRequest(ctx, config.NearbyEndpoint, geoapifyNearbyQuery(input, evidenceCampaignRadiusMeters, config.NearbyCategories, config.NearbyLimit))
	if err != nil {
		return nil, err
	}
	return []evidenceInventoryRequest{
		inventoryRequest(input, appleEvidenceProvider, appleEvidenceOperation, "", apple),
		inventoryRequest(input, config.ProviderIdentity, geoapifyReverseOperation, config.CredentialReference, reverse),
		inventoryRequest(input, config.ProviderIdentity, geoapifyNearbyOperation, config.CredentialReference, nearby),
	}, nil
}

func inventoryRequest(input Input, provider, operation, credentialReference string, raw []byte) evidenceInventoryRequest {
	return evidenceInventoryRequest{
		Provider:      provider,
		Operation:     operation,
		Bytes:         string(raw),
		SHA256:        evidenceDigest(raw),
		CacheIdentity: evidenceCacheIdentity(input, provider, operation, evidenceCoordinateVariant, credentialReference, raw),
	}
}

func writeEvidenceInventoryResult(outputDir string, manifest evidenceInventoryManifest) (EvidenceInventorySummary, error) {
	digest, data, err := marshalEvidenceManifest(manifest)
	if err != nil {
		return EvidenceInventorySummary{}, err
	}
	if err := writePrivateFile(filepath.Join(outputDir, "manifest.json"), data); err != nil {
		return EvidenceInventorySummary{}, err
	}
	return EvidenceInventorySummary{State: manifest.State, ManifestDigest: digest, Counts: manifest.Counts, StopReason: manifest.StopReason}, nil
}

func populateEvidenceCells(assets []evidenceInventoryAsset) {
	population := map[string]int{}
	for index := range assets {
		if assets[index].Location == nil {
			continue
		}
		latitude := math.Floor(assets[index].Location.Latitude*10) / 10
		longitude := math.Floor(assets[index].Location.Longitude*10) / 10
		if latitude == 0 {
			latitude = 0
		}
		if longitude == 0 {
			longitude = 0
		}
		assets[index].CellKey = fmt.Sprintf("%.1f,%.1f", latitude, longitude)
		population[assets[index].CellKey]++
	}
	for index := range assets {
		assets[index].CellPopulation = population[assets[index].CellKey]
	}
}

func selectEvidenceCampaign(manifest *evidenceInventoryManifest, targets []EvidenceCampaignTarget, digest string) (*evidenceCampaignState, error) {
	eligible := map[string]*evidenceInventoryAsset{}
	for index := range manifest.Assets {
		asset := &manifest.Assets[index]
		if asset.Location != nil {
			eligible[asset.AssetID] = asset
		}
	}
	selected := map[string]bool{}
	cases := []evidenceCampaignCase{}
	counts := EvidenceCampaignCounts{}
	coverageGaps := []evidenceCoverageGap{}
	byCategory := map[string]EvidenceCampaignTarget{}
	for _, target := range targets {
		category := strings.TrimSpace(target.Category)
		assetID := strings.TrimSpace(target.AssetID)
		if !containsString(targetCategories, category) || byCategory[category].AssetID != "" || eligible[assetID] == nil || selected[assetID] {
			return nil, errors.New("mismatched")
		}
		byCategory[category] = EvidenceCampaignTarget{AssetID: assetID, Category: category}
		selected[assetID] = true
	}
	for _, category := range targetCategories {
		target := byCategory[category]
		if target.AssetID == "" {
			counts.CoverageGaps++
			coverageGaps = append(coverageGaps, evidenceCoverageGap{Kind: "target_category", Label: category, Count: 1})
			continue
		}
		cases = append(cases, evidenceCampaignCase{AssetID: target.AssetID, Stratum: "targeted", TargetCategory: category})
		counts.TargetedCases++
	}
	cells := map[string][]*evidenceInventoryAsset{}
	for _, asset := range eligible {
		if !selected[asset.AssetID] {
			cells[asset.CellKey] = append(cells[asset.CellKey], asset)
		}
	}
	type cell struct {
		key        string
		population int
	}
	orderedCells := make([]cell, 0, len(cells))
	for key, assets := range cells {
		orderedCells = append(orderedCells, cell{key: key, population: assets[0].CellPopulation})
	}
	sort.Slice(orderedCells, func(i, j int) bool {
		return orderedCells[i].population < orderedCells[j].population || orderedCells[i].population == orderedCells[j].population && orderedCells[i].key < orderedCells[j].key
	})
	for _, row := range orderedCells {
		if counts.SparseCases == campaignSparseCases {
			break
		}
		assets := cells[row.key]
		sortInventoryAssets(assets)
		asset := assets[0]
		selected[asset.AssetID] = true
		cases = append(cases, evidenceCampaignCase{AssetID: asset.AssetID, Stratum: "sparse"})
		counts.SparseCases++
	}
	if counts.SparseCases < campaignSparseCases {
		gap := campaignSparseCases - counts.SparseCases
		counts.CoverageGaps += gap
		coverageGaps = append(coverageGaps, evidenceCoverageGap{Kind: "sparse_cells", Count: gap})
	}
	remaining := make([]*evidenceInventoryAsset, 0, len(eligible))
	for _, asset := range eligible {
		if !selected[asset.AssetID] {
			remaining = append(remaining, asset)
		}
	}
	sortInventoryAssets(remaining)
	for _, asset := range remaining {
		if len(cases) == campaignTargetCases {
			break
		}
		cases = append(cases, evidenceCampaignCase{AssetID: asset.AssetID, Stratum: "random"})
		selected[asset.AssetID] = true
		counts.RandomCases++
	}
	if len(cases) < min(campaignTargetCases, len(eligible)) {
		return nil, errors.New("missing")
	}
	if len(cases) < campaignTargetCases {
		gap := campaignTargetCases - len(cases)
		counts.CoverageGaps += gap
		coverageGaps = append(coverageGaps, evidenceCoverageGap{Kind: "corpus", Count: gap})
	}
	for _, stratum := range []string{"targeted", "sparse", "random"} {
		found := false
		for index := range cases {
			if cases[index].Stratum == stratum {
				cases[index].Canary = true
				found = true
				break
			}
		}
		if !found {
			counts.CoverageGaps++
			coverageGaps = append(coverageGaps, evidenceCoverageGap{Kind: "canary_stratum", Label: stratum, Count: 1})
		}
	}
	for index := range manifest.Assets {
		for _, campaignCase := range cases {
			if manifest.Assets[index].AssetID == campaignCase.AssetID {
				manifest.Assets[index].Stratum = campaignCase.Stratum
				manifest.Assets[index].TargetCategory = campaignCase.TargetCategory
			}
		}
	}
	return &evidenceCampaignState{TargetsDigest: digest, Phase: campaignPhaseCanary, State: campaignStateStopped, Counts: counts, CoverageGaps: coverageGaps, StopReasons: map[string]int{}, Cases: cases}, nil
}

func readCampaignTargets(path string) ([]byte, []EvidenceCampaignTarget, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var targets []EvidenceCampaignTarget
	if err := json.Unmarshal(data, &targets); err != nil || len(targets) > len(targetCategories) {
		return nil, nil, errors.New("mismatched")
	}
	return data, targets, nil
}

func stopCampaign(path string, manifest *evidenceInventoryManifest, phase, reason string) (EvidenceCampaignSummary, error) {
	if manifest.Campaign == nil {
		manifest.Campaign = &evidenceCampaignState{}
	}
	manifest.Campaign.State = campaignStateStopped
	manifest.Campaign.Phase = phase
	manifest.Campaign.StopReason = reason
	if reason != "" {
		if manifest.Campaign.StopReasons == nil {
			manifest.Campaign.StopReasons = map[string]int{}
		}
		manifest.Campaign.StopReasons[reason]++
	}
	if manifest.Campaign.Counts.StoppedCases == 0 {
		manifest.Campaign.Counts.StoppedCases = 1
	}
	if err := saveEvidenceManifest(path, manifest); err != nil {
		return EvidenceCampaignSummary{}, err
	}
	return campaignSummary(*manifest), nil
}

func campaignSummary(manifest evidenceInventoryManifest) EvidenceCampaignSummary {
	state := manifest.Campaign
	return EvidenceCampaignSummary{State: state.State, Phase: state.Phase, ManifestDigest: manifest.ManifestDigest, Counts: state.Counts, StopReason: state.StopReason}
}

func campaignEvidenceError(result EvidenceResult, err error) error {
	if len(result.Records) > 0 {
		record := result.Records[len(result.Records)-1]
		if record.ProviderErrorClass == "throttled" {
			return errors.New(evidenceStopRateLimited)
		}
		if record.StopReason != "" {
			return errors.New(record.StopReason)
		}
	}
	return err
}

func campaignStopReason(err error) string {
	if errors.Is(err, errEvidenceCacheIncomplete) {
		return evidenceStopCacheIncomplete
	}
	reason := strings.TrimSpace(err.Error())
	switch reason {
	case evidenceStopBilling, evidenceStopCredential, evidenceStopRateLimited, evidenceStopCacheIncomplete, "mismatched", "missing", "empty", "malformed", "targets_changed":
		return reason
	case evidenceStopNoResult:
		return "missing"
	case evidenceStopSaturated, evidenceStopTooLarge:
		return "truncated"
	default:
		return "unsafe"
	}
}

func writeCampaignComparison(outputDir string, manifest evidenceInventoryManifest) error {
	china := 0
	chinaComplete := 0
	for _, campaignCase := range manifest.Campaign.Cases {
		if campaignCase.TargetCategory == "mainland_china" {
			china++
			if campaignCase.AppleComplete && campaignCase.GeoComplete && campaignCase.RestartChecked {
				chinaComplete++
			}
		}
	}
	comparison := struct {
		State             string                  `json:"state"`
		Counts            EvidenceCampaignCounts  `json:"counts"`
		Metrics           EvidenceCampaignMetrics `json:"metrics"`
		StopReasons       map[string]int          `json:"stop_reasons"`
		CoverageGaps      []evidenceCoverageGap   `json:"coverage_gaps"`
		ChinaCases        int                     `json:"china_cases"`
		ChinaComplete     int                     `json:"china_complete"`
		AppleTerms        string                  `json:"apple_terms"`
		GeoapifyFreeTerms string                  `json:"geoapify_free_terms"`
	}{campaignStateComplete, manifest.Campaign.Counts, manifest.Campaign.Metrics, manifest.Campaign.StopReasons, manifest.Campaign.CoverageGaps, china, chinaComplete, "no documented numeric ceiling; throttling stops the campaign", "3000 credits per day, up to 5 requests per second; campaign ceiling 2400 requests per day and 4 requests per second"}
	data, err := json.MarshalIndent(comparison, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(outputDir, "comparison.json"), append(data, '\n'))
}

func inventoryAsset(assets []evidenceInventoryAsset, id string) *evidenceInventoryAsset {
	for index := range assets {
		if assets[index].AssetID == id {
			return &assets[index]
		}
	}
	return nil
}

func sortInventoryAssets(assets []*evidenceInventoryAsset) {
	sort.Slice(assets, func(i, j int) bool {
		return assets[i].RandomDigest < assets[j].RandomDigest || assets[i].RandomDigest == assets[j].RandomDigest && assets[i].AssetID < assets[j].AssetID
	})
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
