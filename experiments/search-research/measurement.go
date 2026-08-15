package main

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type processMemoryMeasurement struct {
	baselineHarnessProcessMemoryBytes  int64
	baselineRuntimeProcessMemoryBytes  int64
	peakHarnessAndRuntimeMemoryBytes   int64
	steadyHarnessAndRuntimeMemoryBytes int64
}

type processMemorySampler struct {
	stop                         chan struct{}
	completed                    chan struct{}
	runtimeRootProcessIdentifier int
	mu                           sync.Mutex
	measurement                  processMemoryMeasurement
}

func startProcessMemorySampler(runtimeRootProcessIdentifier int) *processMemorySampler {
	sampler := &processMemorySampler{
		stop:                         make(chan struct{}),
		completed:                    make(chan struct{}),
		runtimeRootProcessIdentifier: runtimeRootProcessIdentifier,
	}
	harnessBytes, runtimeBytes, _ := sampledProcessTreeMemoryBytes(runtimeRootProcessIdentifier)
	sampler.measurement.baselineHarnessProcessMemoryBytes = harnessBytes
	sampler.measurement.baselineRuntimeProcessMemoryBytes = runtimeBytes
	sampler.recordSample(harnessBytes, runtimeBytes)
	go func() {
		defer close(sampler.completed)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				sampler.sample()
			case <-sampler.stop:
				sampler.sample()
				return
			}
		}
	}()
	return sampler
}

func (sampler *processMemorySampler) finish() processMemoryMeasurement {
	close(sampler.stop)
	<-sampler.completed
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	return sampler.measurement
}

func (sampler *processMemorySampler) sample() {
	harnessBytes, runtimeBytes, err := sampledProcessTreeMemoryBytes(sampler.runtimeRootProcessIdentifier)
	if err != nil {
		return
	}
	sampler.recordSample(harnessBytes, runtimeBytes)
}

func (sampler *processMemorySampler) recordSample(harnessBytes, runtimeBytes int64) {
	totalBytes := harnessBytes + runtimeBytes
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	sampler.measurement.steadyHarnessAndRuntimeMemoryBytes = totalBytes
	if totalBytes > sampler.measurement.peakHarnessAndRuntimeMemoryBytes {
		sampler.measurement.peakHarnessAndRuntimeMemoryBytes = totalBytes
	}
}

type processResidentMemoryRow struct {
	parentProcessIdentifier int
	residentMemoryBytes     int64
}

func sampledProcessTreeMemoryBytes(runtimeRootProcessIdentifier int) (harnessBytes int64, runtimeBytes int64, returnedError error) {
	command := exec.Command("/bin/ps", "-axo", "pid=,ppid=,rss=")
	output, err := command.Output()
	if err != nil {
		return 0, 0, err
	}
	processes := make(map[int]processResidentMemoryRow)
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		processIdentifier, processIdentifierError := strconv.Atoi(fields[0])
		parentProcessIdentifier, parentProcessIdentifierError := strconv.Atoi(fields[1])
		residentKilobytes, residentKilobytesError := strconv.ParseInt(fields[2], 10, 64)
		if processIdentifierError != nil || parentProcessIdentifierError != nil || residentKilobytesError != nil {
			continue
		}
		processes[processIdentifier] = processResidentMemoryRow{
			parentProcessIdentifier: parentProcessIdentifier,
			residentMemoryBytes:     residentKilobytes * 1024,
		}
	}
	harnessProcessIdentifiers := descendantProcessIdentifiers(processes, os.Getpid())
	for processIdentifier := range harnessProcessIdentifiers {
		harnessBytes += processes[processIdentifier].residentMemoryBytes
	}
	if runtimeRootProcessIdentifier > 0 {
		if _, exists := processes[runtimeRootProcessIdentifier]; !exists {
			return harnessBytes, 0, fmt.Errorf("runtime root process %d does not exist", runtimeRootProcessIdentifier)
		}
		for processIdentifier := range descendantProcessIdentifiers(processes, runtimeRootProcessIdentifier) {
			if _, belongsToHarness := harnessProcessIdentifiers[processIdentifier]; !belongsToHarness {
				runtimeBytes += processes[processIdentifier].residentMemoryBytes
			}
		}
	}
	return harnessBytes, runtimeBytes, nil
}

func descendantProcessIdentifiers(processes map[int]processResidentMemoryRow, rootProcessIdentifier int) map[int]struct{} {
	descendants := map[int]struct{}{rootProcessIdentifier: {}}
	for foundNewDescendant := true; foundNewDescendant; {
		foundNewDescendant = false
		for processIdentifier, process := range processes {
			if _, known := descendants[processIdentifier]; known {
				continue
			}
			if _, parentKnown := descendants[process.parentProcessIdentifier]; parentKnown {
				descendants[processIdentifier] = struct{}{}
				foundNewDescendant = true
			}
		}
	}
	return descendants
}

func initializeExperimentRunSchema(database *sql.DB) error {
	_, err := database.Exec(`
		create table if not exists experiment_runs (
			experiment_run_identifier integer primary key,
			operation_name text not null,
			started_at text not null,
			completed_at text,
			wall_nanoseconds integer not null,
			inference_nanoseconds integer not null,
			storage_nanoseconds integer not null,
			candidate_generation_nanoseconds integer not null,
			ordering_nanoseconds integer not null,
			model_artifact_bytes integer not null,
			index_bytes integer not null,
			runtime_root_process_identifier integer not null,
			baseline_harness_process_memory_bytes integer not null,
			baseline_runtime_process_memory_bytes integer not null,
			peak_harness_and_runtime_process_memory_bytes integer not null,
			steady_harness_and_runtime_process_memory_bytes integer not null,
			prompt_tokens integer not null,
			failure_stage text not null,
			failure_message text not null
		)`)
	return err
}

func startExperimentRun(database *sql.DB, operationName string, modelArtifactBytes int64, runtimeRootProcessIdentifier int) (int64, experimentRunMeasurement, error) {
	measurement := experimentRunMeasurement{
		operationName:                operationName,
		startedAt:                    time.Now().UTC(),
		modelArtifactBytes:           modelArtifactBytes,
		runtimeRootProcessIdentifier: runtimeRootProcessIdentifier,
	}
	result, err := database.Exec(`
		insert into experiment_runs(
			operation_name, started_at, wall_nanoseconds, inference_nanoseconds,
			storage_nanoseconds, candidate_generation_nanoseconds, ordering_nanoseconds,
			model_artifact_bytes, index_bytes, runtime_root_process_identifier,
			baseline_harness_process_memory_bytes, baseline_runtime_process_memory_bytes,
			peak_harness_and_runtime_process_memory_bytes,
			steady_harness_and_runtime_process_memory_bytes, prompt_tokens,
			failure_stage, failure_message
		) values (?, ?, 0, 0, 0, 0, 0, ?, 0, ?, 0, 0, 0, 0, 0, '', '')`,
		operationName, measurement.startedAt.Format(time.RFC3339Nano), modelArtifactBytes, runtimeRootProcessIdentifier)
	if err != nil {
		return 0, measurement, err
	}
	runIdentifier, err := result.LastInsertId()
	return runIdentifier, measurement, err
}

func finishExperimentRun(database *sql.DB, runIdentifier int64, measurement experimentRunMeasurement) error {
	measurement.completedAt = time.Now().UTC()
	measurement.wallElapsed = measurement.completedAt.Sub(measurement.startedAt)
	_, err := database.Exec(`
		update experiment_runs set
			completed_at = ?, wall_nanoseconds = ?, inference_nanoseconds = ?,
			storage_nanoseconds = ?, candidate_generation_nanoseconds = ?, ordering_nanoseconds = ?,
			model_artifact_bytes = ?, index_bytes = ?, runtime_root_process_identifier = ?,
			baseline_harness_process_memory_bytes = ?, baseline_runtime_process_memory_bytes = ?,
			peak_harness_and_runtime_process_memory_bytes = ?,
			steady_harness_and_runtime_process_memory_bytes = ?, prompt_tokens = ?,
			failure_stage = ?, failure_message = ?
		where experiment_run_identifier = ?`,
		measurement.completedAt.Format(time.RFC3339Nano), measurement.wallElapsed.Nanoseconds(),
		measurement.inferenceElapsed.Nanoseconds(), measurement.storageElapsed.Nanoseconds(),
		measurement.candidateGenerationElapsed.Nanoseconds(), measurement.orderingElapsed.Nanoseconds(),
		measurement.modelArtifactBytes, measurement.indexBytes, measurement.runtimeRootProcessIdentifier,
		measurement.baselineHarnessProcessMemoryBytes, measurement.baselineRuntimeProcessMemoryBytes,
		measurement.peakProcessMemoryBytes, measurement.steadyProcessMemoryBytes, measurement.promptTokens,
		measurement.failureStage, measurement.failureMessage, runIdentifier)
	return err
}

func fileSize(path string) int64 {
	fileInformation, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fileInformation.Size()
}

func runBaseTrawl(arguments []string, stdout io.Writer, stderr io.Writer) error {
	baseTrawlPath := strings.TrimSpace(os.Getenv("TRAWL_SEARCH_RESEARCH_BASE_TRAWL"))
	if baseTrawlPath == "" {
		return errors.New("TRAWL_SEARCH_RESEARCH_BASE_TRAWL is required for non-search commands")
	}
	if !filepath.IsAbs(baseTrawlPath) {
		return errors.New("TRAWL_SEARCH_RESEARCH_BASE_TRAWL must be an absolute path")
	}
	command := exec.Command(baseTrawlPath, arguments...)
	if len(arguments) > 0 && arguments[0] == "open" {
		sourceSnapshot, err := searchResearchSourceSnapshotForDelegatedOpen()
		if err != nil {
			return err
		}
		command.Env = environmentWithValue(
			os.Environ(),
			"OPENTRAWL_STATE_ROOT",
			sourceSnapshot.stateRoot,
		)
	}
	command.Stdin = os.Stdin
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("base trawl: %w", err)
	}
	return nil
}

func searchResearchSourceSnapshotForDelegatedOpen() (searchResearchSourceSnapshot, error) {
	corpusPath := strings.TrimSpace(os.Getenv("TRAWL_SEARCH_RESEARCH_CORPUS"))
	if corpusPath == "" {
		return searchResearchSourceSnapshot{}, errors.New("TRAWL_SEARCH_RESEARCH_CORPUS is required for open")
	}
	corpusDatabase, err := sql.Open("sqlite3", "file:"+corpusPath+"?mode=ro&immutable=1")
	if err != nil {
		return searchResearchSourceSnapshot{}, err
	}
	defer func() { _ = corpusDatabase.Close() }()
	return readSearchResearchSourceSnapshot(corpusDatabase)
}

func environmentWithValue(environment []string, name, value string) []string {
	prefix := name + "="
	updatedEnvironment := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			updatedEnvironment = append(updatedEnvironment, entry)
		}
	}
	return append(updatedEnvironment, prefix+value)
}
