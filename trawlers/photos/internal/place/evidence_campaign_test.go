package place

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type syntheticEvidenceLog struct{ lines []string }

func (log *syntheticEvidenceLog) Info(event, message string) error {
	log.lines = append(log.lines, "info "+event+" "+message)
	return nil
}

func (log *syntheticEvidenceLog) Warn(event, message string) error {
	log.lines = append(log.lines, "warn "+event+" "+message)
	return nil
}

func (log *syntheticEvidenceLog) contains(parts ...string) bool {
	for _, line := range log.lines {
		matched := true
		for _, part := range parts {
			matched = matched && strings.Contains(line, part)
		}
		if matched {
			return true
		}
	}
	return false
}

func TestEvidenceCampaignCacheRootIsPrivateAndNonSymlink(t *testing.T) {
	parent := privateTempDir(t)
	cache := filepath.Join(parent, "cache")
	if err := ensurePrivateEvidenceCacheRoot(cache); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(cache)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("private cache root = %#v error=%v", info, err)
	}
	link := filepath.Join(parent, "cache-link")
	if err := os.Symlink(cache, link); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateEvidenceCacheRoot(link); err == nil {
		t.Fatal("symlink cache root passed")
	}
}

func TestEvidenceCampaignTamperedCanariesBlockBeforeAndAfterInspectionReceipt(t *testing.T) {
	manifest := syntheticCampaignManifest(101)
	config := syntheticInventoryGeoapify()
	manifest.Provider = evidenceInventoryProviderFromConfig(config)
	targets := syntheticCampaignTargets(manifest.Assets[:6])
	targetBytes, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	state, err := selectEvidenceCampaign(&manifest, targets, evidenceDigest(targetBytes))
	if err != nil {
		t.Fatal(err)
	}
	state.CanariesComplete = true
	manifest.Campaign = state
	root := privateTempDir(t)
	for index, campaignCase := range state.Cases {
		if !campaignCase.Canary {
			continue
		}
		dir := filepath.Join(root, "cases", fmt.Sprintf("%04d", index+1), campaignPhaseCanary, "synthetic")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "boundary.raw"), []byte("synthetic boundary\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	state.CanaryEvidenceDigest, err = digestCampaignCanaries(root, state.Cases)
	if err != nil {
		t.Fatal(err)
	}
	inventoryRoot := privateTempDir(t)
	manifestPath := filepath.Join(inventoryRoot, "manifest.json")
	if err := saveEvidenceManifest(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(inventoryRoot, "targets.json")
	if err := os.WriteFile(targetsPath, targetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	receiptPath := writeSyntheticInspectionReceipt(t, manifestPath)
	tampered := firstCanaryBoundaryFile(t, root)
	if err := os.WriteFile(tampered, []byte("tampered before receipt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runtime := campaignRuntime{now: time.Now, sleep: sleepContext, runOperation: func(context.Context, EvidenceOptions, evidenceOperation) (EvidenceResult, error) {
		calls++
		return EvidenceResult{}, nil
	}}
	opts := EvidenceCampaignOptions{ManifestPath: manifestPath, TargetsPath: targetsPath, InspectionReceiptPath: receiptPath, OutputDir: root, Resume: true, Geoapify: config}
	stopped, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil || stopped.StopReason != "mismatched" || calls != 0 {
		t.Fatalf("tamper before receipt = %#v calls=%d error=%v", stopped, calls, err)
	}
	if err := os.WriteFile(tampered, []byte("synthetic boundary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err = readEvidenceManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Campaign.StopReason = ""
	manifest.Campaign.CanariesInspected = true
	receiptBytes, err := os.ReadFile(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Campaign.CanaryInspectionReceiptDigest = evidenceDigest(receiptBytes)
	if err := saveEvidenceManifest(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tampered, []byte("tampered after receipt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stopped, err = runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil || stopped.StopReason != "mismatched" || calls != 0 {
		t.Fatalf("tamper after receipt = %#v calls=%d error=%v", stopped, calls, err)
	}
}

func TestEvidenceCampaignProviderStopIsDurableAcrossResume(t *testing.T) {
	manifest := syntheticCampaignManifest(101)
	config := syntheticInventoryGeoapify()
	configureSyntheticCampaignManifest(t, &manifest, config)
	targets := syntheticCampaignTargets(manifest.Assets[:6])
	targetBytes, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	root := privateTempDir(t)
	manifestPath := filepath.Join(root, "manifest.json")
	if err := saveEvidenceManifest(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	targetsPath := filepath.Join(root, "targets.json")
	if err := os.WriteFile(targetsPath, targetBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runtime := campaignRuntime{now: time.Now, sleep: sleepContext, runOperation: func(_ context.Context, opts EvidenceOptions, operation evidenceOperation) (EvidenceResult, error) {
		calls++
		request := inventoryRequestForOperation(manifest.Assets[0].Requests, operation)
		record := EvidenceRecord{Input: opts.Input, ProviderIdentity: request.Provider, Operation: request.Operation, CoordinateVariant: evidenceCoordinateVariant, PreAuthRequestSHA256: request.SHA256, CacheIdentity: request.CacheIdentity, CompletionState: evidenceStateStopped, StopReason: evidenceStopMalformed}
		result := EvidenceResult{State: evidenceStateStopped, CoordinateVariant: evidenceCoordinateVariant, Records: []EvidenceRecord{record}, StopReasons: []string{evidenceStopMalformed}}
		return result, &EvidenceStoppedError{OutputDir: opts.OutputDir, StopReasons: result.StopReasons}
	}}
	opts := EvidenceCampaignOptions{ManifestPath: manifestPath, TargetsPath: targetsPath, OutputDir: privateTempDir(t), Resume: true, Geoapify: config}
	first, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil || first.StopReason != evidenceStopMalformed || calls != 1 {
		t.Fatalf("first provider stop = %#v calls=%d error=%v", first, calls, err)
	}
	second, err := runEvidenceCampaign(context.Background(), opts, runtime)
	if err != nil || second.StopReason != evidenceStopMalformed || calls != 1 {
		t.Fatalf("durable provider stop = %#v calls=%d error=%v", second, calls, err)
	}
}

func TestEvidenceCampaignRejectsMismatchedOperationBeforeNextCall(t *testing.T) {
	manifest := syntheticCampaignManifest(7)
	config := syntheticInventoryGeoapify()
	configureSyntheticCampaignManifest(t, &manifest, config)
	manifest.Campaign = &evidenceCampaignState{Phase: campaignPhaseCanary, Cases: []evidenceCampaignCase{{AssetID: manifest.Assets[0].AssetID}}}
	calls := 0
	runtime := campaignRuntime{now: time.Now, sleep: sleepContext, runOperation: func(_ context.Context, opts EvidenceOptions, operation evidenceOperation) (EvidenceResult, error) {
		calls++
		request := inventoryRequestForOperation(manifest.Assets[0].Requests, operation)
		record := EvidenceRecord{Input: Input{AssetID: "wrong"}, ProviderIdentity: request.Provider, Operation: request.Operation, CoordinateVariant: evidenceCoordinateVariant, PreAuthRequestSHA256: request.SHA256, CacheIdentity: request.CacheIdentity, CompletionState: evidenceStateComplete}
		return EvidenceResult{State: evidenceStateComplete, CoordinateVariant: evidenceCoordinateVariant, Records: []EvidenceRecord{record}}, nil
	}}
	opts := EvidenceCampaignOptions{OutputDir: privateTempDir(t), CacheDir: privateTempDir(t), Geoapify: config}
	result, err := runCampaignEvidence(context.Background(), opts, &manifest, 0, runtime, []evidenceOperation{evidenceOperationApple, evidenceOperationReverse})
	if err == nil || err.Error() != "mismatched" || len(result.Records) != 0 || calls != 1 {
		t.Fatalf("mismatched operation = %#v calls=%d error=%v", result, calls, err)
	}
}

func TestEvidenceCampaignRejectsSymlinkedOutputBeforeProviderCall(t *testing.T) {
	manifest := syntheticCampaignManifest(7)
	config := syntheticInventoryGeoapify()
	configureSyntheticCampaignManifest(t, &manifest, config)
	manifest.Campaign = &evidenceCampaignState{Phase: campaignPhaseCanary, Cases: []evidenceCampaignCase{{AssetID: manifest.Assets[0].AssetID}}}
	output := privateTempDir(t)
	outside := privateTempDir(t)
	if err := os.Symlink(outside, filepath.Join(output, "cases")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	runtime := campaignRuntime{now: time.Now, sleep: sleepContext, runOperation: func(context.Context, EvidenceOptions, evidenceOperation) (EvidenceResult, error) {
		calls++
		return EvidenceResult{}, nil
	}}
	_, err := runCampaignEvidence(context.Background(), EvidenceCampaignOptions{OutputDir: output, CacheDir: privateTempDir(t), Geoapify: config}, &manifest, 0, runtime, []evidenceOperation{evidenceOperationApple})
	entries, readErr := os.ReadDir(outside)
	if err == nil || calls != 0 || readErr != nil || len(entries) != 0 {
		t.Fatalf("symlink output boundary calls=%d outside=%#v error=%v read_error=%v", calls, entries, err, readErr)
	}
}

func TestEvidenceCaptureAtomicallyReplacesSymlinkLeaf(t *testing.T) {
	root := privateTempDir(t)
	dir := filepath.Join(root, "capture")
	if err := ensurePrivateEvidenceDirectory(dir); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.raw")
	outsideBytes := []byte("outside must remain unchanged")
	if err := os.WriteFile(outside, outsideBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "response.raw")); err != nil {
		t.Fatal(err)
	}
	capture := completeCapture(Input{AssetID: "synthetic"}, appleEvidenceProvider, appleEvidenceOperation, evidenceCoordinateVariant, "", []byte("request"), []byte("response"), 0, parsedEvidence{})
	if err := writeEvidenceCapture(dir, &capture); err != nil {
		t.Fatal(err)
	}
	gotOutside, err := os.ReadFile(outside)
	if err != nil || string(gotOutside) != string(outsideBytes) {
		t.Fatalf("outside leaf changed: %q error=%v", gotOutside, err)
	}
	info, err := os.Lstat(filepath.Join(dir, "response.raw"))
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("response leaf = %#v error=%v", info, err)
	}
}

func firstCanaryBoundaryFile(t *testing.T, root string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "cases", "*", campaignPhaseCanary, "synthetic", "boundary.raw"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("canary boundary matches=%#v error=%v", matches, err)
	}
	return matches[0]
}

func TestEvidenceCampaignSparsePopulationIncludesTargetedRows(t *testing.T) {
	manifest := evidenceInventoryManifest{Snapshot: EvidenceSnapshotReceipt{ID: "snapshot:synthetic"}}
	coordinate := Coordinate{Latitude: 1, Longitude: 1}
	for index := 0; index < 101; index++ {
		asset := evidenceInventoryAsset{
			AssetID:        fmt.Sprintf("asset:%03d", index),
			Location:       &coordinate,
			CellKey:        fmt.Sprintf("b:%03d", index),
			CellPopulation: 1,
			RandomDigest:   fmt.Sprintf("%064d", index),
		}
		if index == 0 || index == 6 {
			asset.CellKey = "a:targeted-cell"
			asset.CellPopulation = 2
		}
		manifest.Assets = append(manifest.Assets, asset)
	}
	state, err := selectEvidenceCampaign(&manifest, syntheticCampaignTargets(manifest.Assets[:6]), evidenceDigest([]byte("targets")))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, campaignCase := range state.Cases {
		if campaignCase.AssetID != "asset:006" {
			continue
		}
		found = true
		if campaignCase.Stratum != "random" {
			t.Fatalf("targeted cell population was reduced after target selection: %#v", campaignCase)
		}
	}
	if !found {
		t.Fatal("remaining row from the targeted cell was not selected")
	}
}

func TestEvidenceCampaignAdapterBoundaryKeepsCredentialInBroker(t *testing.T) {
	t.Setenv("SYNTHETIC_OSM_KEY", "synthetic-secret")
	t.Setenv("PHOTOS_PLACE_EVIDENCE_OPERATION", "all")
	t.Setenv("SYNTHETIC_KEEP", "kept")

	opts := EvidenceOptions{
		CoordinateVariant: "source-coordinate",
		RadiusMeters:      150,
		OutputDir:         "/tmp/synthetic-campaign/case",
	}
	inputPath := filepath.Join(opts.OutputDir, "input.json")
	wantArgs := []string{
		"--input", inputPath,
		"--coordinate-variant", "source-coordinate",
		"--radius", "150",
		"--out", opts.OutputDir,
		"--operation", "geoapify-nearby",
	}
	if got := campaignAdapterArguments(opts, inputPath, EvidenceOperationGeoapifyNearby); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("adapter arguments = %#v, want %#v", got, wantArgs)
	}

	environment := campaignAdapterEnvironment("SYNTHETIC_OSM_KEY")
	for _, entry := range environment {
		if strings.HasPrefix(entry, "SYNTHETIC_OSM_KEY=") || strings.HasPrefix(entry, "PHOTOS_PLACE_EVIDENCE_OPERATION=") {
			t.Fatalf("campaign passed broker-owned environment: %q", entry)
		}
	}
	if !containsEnvironment(environment, "SYNTHETIC_KEEP=kept") {
		t.Fatalf("adapter environment dropped unrelated state: %#v", environment)
	}

	for operation, want := range map[evidenceOperation]EvidenceOperation{
		evidenceOperationApple:   EvidenceOperationApple,
		evidenceOperationReverse: EvidenceOperationGeoapifyReverse,
		evidenceOperationNearby:  EvidenceOperationGeoapifyNearby,
	} {
		got, err := publicEvidenceOperation(operation)
		if err != nil || got != want {
			t.Fatalf("campaign operation %q = %q, %v", operation, got, err)
		}
	}
}

func containsEnvironment(environment []string, want string) bool {
	for _, entry := range environment {
		if entry == want {
			return true
		}
	}
	return false
}
