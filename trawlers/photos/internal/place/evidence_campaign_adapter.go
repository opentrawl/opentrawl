package place

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type EvidenceLogSink interface {
	Info(event, message string) error
	Warn(event, message string) error
}

func logEvidence(sink EvidenceLogSink, warning bool, event string, fields ...string) {
	if sink == nil {
		return
	}
	message := strings.Join(fields, " ")
	if warning {
		_ = sink.Warn(event, message)
		return
	}
	_ = sink.Info(event, message)
}

func durationField(duration time.Duration) string {
	return "duration_ms=" + strconv.FormatInt(duration.Milliseconds(), 10)
}

func runEvidencePhase(sink EvidenceLogSink, phase string, run func() error) error {
	started := time.Now()
	err := run()
	outcome := "complete"
	if err != nil {
		outcome = "stopped"
	}
	logEvidence(sink, err != nil, "place_evidence_campaign_phase", "phase="+phase, "outcome="+outcome, durationField(time.Since(started)))
	return err
}

func logEvidenceCase(sink EvidenceLogSink, phase string, index int, operation string, started time.Time, err error) {
	outcome := "complete"
	if err != nil {
		outcome = "stopped"
	}
	logEvidence(sink, err != nil, "place_evidence_campaign_case", "phase="+phase, "case="+strconv.Itoa(index+1), "operation="+operation, "outcome="+outcome, durationField(time.Since(started)))
}

func governGeoapifyStart(ctx context.Context, manifestPath string, manifest *evidenceInventoryManifest, runtime campaignRuntime) error {
	state := manifest.Campaign
	now := runtime.now().UTC()
	date := now.Format("2006-01-02")
	if state.GeoapifyDate != date {
		state.GeoapifyDate, state.LastGeoapifyStart = date, ""
		state.Counts.GeoapifyRequestsToday = 0
	}
	if state.Counts.GeoapifyRequestsToday >= geoapifyDailyLimit {
		return errEvidenceRateLimited
	}
	if state.LastGeoapifyStart != "" {
		last, err := time.Parse(time.RFC3339Nano, state.LastGeoapifyStart)
		if err != nil {
			return errEvidenceCacheIncomplete
		}
		if err := waitForStart(ctx, runtime, last, 250*time.Millisecond); err != nil {
			return err
		}
		now = runtime.now().UTC()
		setMinimumDuration(&state.Metrics.GeoapifyMinStartIntervalMilliseconds, now.Sub(last))
	}
	state.LastGeoapifyStart = now.Format(time.RFC3339Nano)
	state.Counts.GeoapifyRequestsToday++
	state.Metrics.GeoapifyRequests++
	state.Metrics.GeoapifyMaxConcurrency = 1
	return saveEvidenceManifest(manifestPath, manifest)
}

func setMinimumDuration(current *float64, duration time.Duration) {
	milliseconds := float64(duration) / float64(time.Millisecond)
	if *current == 0 || milliseconds < *current {
		*current = milliseconds
	}
}

func waitForStart(ctx context.Context, runtime campaignRuntime, previous time.Time, interval time.Duration) error {
	if previous.IsZero() {
		return nil
	}
	wait := interval - runtime.now().UTC().Sub(previous)
	if wait <= 0 {
		return nil
	}
	return runtime.sleep(ctx, wait)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runAdapterEvidenceOperation(ctx context.Context, opts EvidenceOptions, operation evidenceOperation) (EvidenceResult, error) {
	publicOperation, err := publicEvidenceOperation(operation)
	if err != nil {
		return EvidenceResult{}, err
	}
	adapter, err := exec.LookPath(evidenceAdapterCommand)
	if err != nil || !filepath.IsAbs(adapter) {
		return EvidenceResult{}, errors.New("photos place evidence adapter is unavailable")
	}
	input, err := json.MarshalIndent(opts.Input, "", "  ")
	if err != nil {
		return EvidenceResult{}, err
	}
	inputPath := filepath.Join(opts.OutputDir, "input.json")
	if err := writePrivateFile(inputPath, append(input, '\n')); err != nil {
		return EvidenceResult{}, err
	}
	args := campaignAdapterArguments(opts, inputPath, publicOperation)
	argv, err := json.MarshalIndent(append([]string{adapter}, args...), "", "  ")
	if err != nil {
		return EvidenceResult{}, err
	}
	if err := writePrivateFile(filepath.Join(opts.OutputDir, "adapter.argv.json"), append(argv, '\n')); err != nil {
		return EvidenceResult{}, err
	}
	command := exec.CommandContext(ctx, adapter, args...)
	command.Env = campaignAdapterEnvironment(opts.Geoapify.CredentialReference)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	status := 0
	if runErr != nil {
		status = 1
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			status = exitErr.ExitCode()
		}
	}
	for name, data := range map[string][]byte{"adapter.stdout.raw": stdout.Bytes(), "adapter.stderr.raw": stderr.Bytes(), "adapter.status": []byte(strconv.Itoa(status) + "\n")} {
		if err := writePrivateFile(filepath.Join(opts.OutputDir, name), data); err != nil {
			return EvidenceResult{}, err
		}
	}
	if runErr != nil {
		if stopped, stoppedErr := readStoppedAdapterResult(opts.OutputDir); stoppedErr != nil {
			return stopped, stoppedErr
		}
		return EvidenceResult{}, fmt.Errorf("photos place evidence adapter exited %d", status)
	}
	var result EvidenceResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return EvidenceResult{}, errors.New("photos place evidence adapter returned malformed JSON")
	}
	if len(result.Records) != 1 || result.Records[0].Operation != inventoryOperation(operation) {
		return EvidenceResult{}, errors.New("photos place evidence adapter returned mismatched evidence")
	}
	if result.State != evidenceStateComplete {
		return result, &EvidenceStoppedError{OutputDir: opts.OutputDir, StopReasons: result.StopReasons}
	}
	return result, nil
}

func campaignAdapterArguments(opts EvidenceOptions, inputPath string, operation EvidenceOperation) []string {
	return []string{"--input", inputPath, "--coordinate-variant", opts.CoordinateVariant, "--radius", strconv.FormatFloat(opts.RadiusMeters, 'f', -1, 64), "--out", opts.OutputDir, "--operation", string(operation)}
}

func publicEvidenceOperation(operation evidenceOperation) (EvidenceOperation, error) {
	switch operation {
	case evidenceOperationApple:
		return EvidenceOperationApple, nil
	case evidenceOperationReverse:
		return EvidenceOperationGeoapifyReverse, nil
	case evidenceOperationNearby:
		return EvidenceOperationGeoapifyNearby, nil
	default:
		return "", errors.New("unknown campaign evidence operation")
	}
}

func inventoryOperation(operation evidenceOperation) string {
	if operation == evidenceOperationReverse {
		return geoapifyReverseOperation
	}
	if operation == evidenceOperationNearby {
		return geoapifyNearbyOperation
	}
	return appleEvidenceOperation
}

func campaignAdapterEnvironment(credentialReference string) []string {
	blocked := map[string]bool{strings.TrimSpace(credentialReference): true, "PHOTOS_PLACE_EVIDENCE_OPERATION": true}
	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !blocked[name] {
			environment = append(environment, entry)
		}
	}
	return environment
}

func readStoppedAdapterResult(outputDir string) (EvidenceResult, error) {
	matches, err := filepath.Glob(filepath.Join(outputDir, "*", "record.json"))
	if err != nil || len(matches) != 1 {
		return EvidenceResult{}, errors.New("photos place evidence adapter returned no single stop record")
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		return EvidenceResult{}, err
	}
	var record EvidenceRecord
	if err := json.Unmarshal(data, &record); err != nil || record.CompletionState != evidenceStateStopped {
		return EvidenceResult{}, errors.New("photos place evidence adapter returned malformed stop evidence")
	}
	record.RecordDir = filepath.Dir(matches[0])
	result := EvidenceResult{State: evidenceStateStopped, CoordinateVariant: record.CoordinateVariant, Records: []EvidenceRecord{record}, StopReasons: []string{record.ProviderIdentity + " " + record.Operation + ": " + record.StopReason}}
	return result, &EvidenceStoppedError{OutputDir: outputDir, StopReasons: result.StopReasons}
}
