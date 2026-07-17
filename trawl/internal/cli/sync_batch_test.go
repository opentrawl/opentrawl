package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
)

func TestSyncPhasesOverlapAcquisitionThenReconcileSuccessfulSourcesInOrder(t *testing.T) {
	sources := []Source{{ID: "telegram"}, {ID: "imessage"}, {ID: "gmail"}}
	entered := make(chan string, len(sources))
	release := make(chan struct{})
	done := make(chan []SyncResult, 1)

	var mu sync.Mutex
	var reconciled []string
	activeReconciliations := 0
	maxReconciliations := 0
	go func() {
		done <- runSyncPhases(
			context.Background(),
			sources,
			func(_ context.Context, source Source) SyncResult {
				entered <- source.ID
				<-release
				if source.ID == "imessage" {
					return SyncResult{Source: source.ID, State: "error"}
				}
				return SyncResult{Source: source.ID, State: "ok"}
			},
			func(_ context.Context, source Source) error {
				mu.Lock()
				activeReconciliations++
				if activeReconciliations > maxReconciliations {
					maxReconciliations = activeReconciliations
				}
				reconciled = append(reconciled, source.ID)
				activeReconciliations--
				mu.Unlock()
				return nil
			},
		)
	}()

	seen := make(map[string]bool, len(sources))
	for range sources {
		select {
		case source := <-entered:
			seen[source] = true
		case <-time.After(time.Second):
			t.Fatal("acquisitions did not overlap before any was released")
		}
	}
	close(release)
	results := <-done

	if !reflect.DeepEqual(seen, map[string]bool{"telegram": true, "imessage": true, "gmail": true}) {
		t.Fatalf("acquired sources = %#v", seen)
	}
	if got := []string{results[0].Source, results[1].Source, results[2].Source}; !reflect.DeepEqual(got, []string{"telegram", "imessage", "gmail"}) {
		t.Fatalf("result order = %#v", got)
	}
	if !reflect.DeepEqual(reconciled, []string{"telegram", "gmail"}) {
		t.Fatalf("reconciled snapshots = %#v", reconciled)
	}
	if maxReconciliations != 1 {
		t.Fatalf("maximum concurrent People reconciliations = %d", maxReconciliations)
	}
}

func TestBusySyncReturnsOneTypedResultAndDoesNotStartOrQueueWork(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeFakeCrawlers(t, fakeCrawler{
		name:     "messages",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:     `{"state":"ok","added":1}`,
	})

	lock, err := acquireSyncBatchLock("")
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := runCLI(t, "--json", "sync", "imessage")
	if code != 1 || stderr != "" {
		t.Fatalf("busy CLI code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil || envelope.Error.Code != "already_syncing" {
		t.Fatalf("busy CLI result=%q err=%v", stdout, err)
	}

	stdout, stderr, code = runCLI(t, "__app", "sync", "--source", "imessage")
	if code != 0 || stderr != "" {
		t.Fatalf("busy app transport code=%d stderr=%q", code, stderr)
	}
	response := decodeAppSync(t, []byte(stdout))
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED || len(response.GetSources()) != 0 || len(response.GetFailures()) != 1 || response.GetFailures()[0].GetCode() != appv1.FailureCode_FAILURE_CODE_ALREADY_SYNCING {
		t.Fatalf("busy app response = %#v", response)
	}

	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code = runCLI(t, "--json", "sync", "imessage")
	if code != 0 || !strings.Contains(stdout, `"source":"imessage"`) {
		t.Fatalf("later sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestAppSyncAcceptsOneOrderedDeduplicatedBatch(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	writeFakeCrawlers(t,
		fakeCrawler{name: "messages", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`, sync: `{"state":"ok","added":1}`},
		fakeCrawler{name: "telegram", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`, sync: `{"state":"ok","added":1}`},
	)

	stdout, stderr, code := runCLI(t, "__app", "sync", "--source", "messages", "--source", "telegram", "--source", "imessage")
	if code != 0 || stderr != "" {
		t.Fatalf("app batch code=%d stderr=%q", code, stderr)
	}
	response := decodeAppSync(t, []byte(stdout))
	if len(response.GetSources()) != 2 || response.GetSources()[0].GetAppId() != "imessage" || response.GetSources()[1].GetAppId() != "telegram" {
		t.Fatalf("app batch order = %#v", response.GetSources())
	}
}

func TestAppSyncFullHistoryCanonicalizesTelegramAliases(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	marker := filepath.Join(t.TempDir(), "telegram-acquisitions")
	writeFakeCrawlers(t, fakeCrawler{
		name:          "telecrawl",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`,
		sync:          `{"state":"ok","added":1}`,
		prepareMarker: marker,
	})

	stdout, stderr, code := runCLI(t, "__app", "sync", "--source", "telecrawl", "--source", "telegram", "--full-history")
	if code != 0 || stderr != "" {
		t.Fatalf("full-history alias batch code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	response := decodeAppSync(t, []byte(stdout))
	if len(response.GetSources()) != 1 || response.GetSources()[0].GetAppId() != "telegram" {
		t.Fatalf("full-history alias response = %#v", response.GetSources())
	}
	if acquisitions := markerLineCount(t, marker); acquisitions != 1 {
		t.Fatalf("telegram acquisitions = %d, want 1", acquisitions)
	}
}

func TestAppSyncFullHistoryRejectsNonTelegramCanonicalSelections(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	writeFakeCrawlers(t,
		fakeCrawler{name: "telecrawl", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`, sync: `{"state":"ok","added":1}`},
		fakeCrawler{name: "messages", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`, sync: `{"state":"ok","added":1}`},
	)

	for _, args := range [][]string{
		{"__app", "sync", "--source", "imessage", "--full-history"},
		{"__app", "sync", "--source", "telecrawl", "--source", "imessage", "--full-history"},
	} {
		var stdout, stderr bytes.Buffer
		err := Execute(args, &stdout, &stderr)
		if err == nil || !strings.Contains(err.Error(), "--full-history requires --source telegram") {
			t.Fatalf("invalid full-history args=%#v err=%v stdout=%q stderr=%q", args, err, stdout.String(), stderr.String())
		}
	}
}

func TestCLISyncCanonicalizesRepeatedIDsAndAliases(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{name: "repeated identical id", args: []string{"imessage", "imessage"}, want: []string{"imessage"}},
		{name: "binary alias and id", args: []string{"messages", "telegram", "imessage"}, want: []string{"imessage", "telegram"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := syntheticHome(t)
			t.Setenv("HOME", home)
			marker := filepath.Join(t.TempDir(), "imessage-acquisitions")
			writeFakeCrawlers(t,
				fakeCrawler{name: "messages", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`, sync: `{"state":"ok","added":1}`, prepareMarker: marker},
				fakeCrawler{name: "telegram", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`, sync: `{"state":"ok","added":1}`},
			)

			stdout, stderr, code := runCLI(t, append([]string{"--json", "sync"}, tc.args...)...)
			if code != 0 || strings.Contains(stdout+stderr, "already_syncing") {
				t.Fatalf("canonical sync code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
				var result SyncResult
				if err := json.Unmarshal([]byte(line), &result); err != nil {
					t.Fatalf("decode sync result %q: %v", line, err)
				}
				got = append(got, result.Source)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("canonical result order = %#v, want %#v", got, tc.want)
			}
			if acquisitions := markerLineCount(t, marker); acquisitions != 1 {
				t.Fatalf("imessage acquisitions = %d, want 1", acquisitions)
			}
		})
	}
}

func TestCLISyncAllowsSourceFlagsAfterAliasesCanonicalizeToOneSource(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	writeFakeCrawlers(t, fakeCrawler{
		name:     "messages",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:     `{"state":"ok","added":1}`,
	})

	stdout, stderr, code := runCLI(t, "sync", "messages", "imessage", "--help")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "trawl sync imessage") {
		t.Fatalf("canonical alias help code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCLISyncRejectsSourceFlagsForDistinctCanonicalSources(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	writeFakeCrawlers(t,
		fakeCrawler{name: "messages", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`, sync: `{"state":"ok","added":1}`},
		fakeCrawler{name: "telegram", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`, sync: `{"state":"ok","added":1}`},
	)

	ensureFakeArchives(t)
	var stdout, stderr bytes.Buffer
	err := Execute([]string{"sync", "messages", "telegram", "--help"}, &stdout, &stderr)
	if ExitCode(err) != 2 || err == nil || !strings.Contains(err.Error(), "source-specific sync flags require exactly one source") {
		t.Fatalf("distinct source flags err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestOverlappingSyncProcessesRunOnceReturnBusyAndNeverQueue(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	marker := filepath.Join(t.TempDir(), "acquisitions")
	writeFakeCrawlers(t, fakeCrawler{
		name:          "messages",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:          `{"state":"ok","added":1}`,
		syncSleep:     "500ms",
		prepareMarker: marker,
	})
	ensureFakeArchives(t)

	first := syncCallerProcess(t)
	var firstOut, firstErr bytes.Buffer
	first.Stdout, first.Stderr = &firstOut, &firstErr
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	waitForMarkerLines(t, marker, 1)

	second := syncCallerProcess(t)
	secondOut, err := second.Output()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("second sync err=%v stdout=%q", err, secondOut)
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(secondOut, &envelope); err != nil || envelope.Error.Code != "already_syncing" {
		t.Fatalf("second sync result=%q err=%v", secondOut, err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first sync err=%v stdout=%q stderr=%q", err, firstOut.String(), firstErr.String())
	}
	if lines := markerLineCount(t, marker); lines != 1 {
		t.Fatalf("acquisitions after overlap = %d, want 1", lines)
	}

	third := syncCallerProcess(t)
	if output, err := third.CombinedOutput(); err != nil {
		t.Fatalf("later sync err=%v output=%q", err, output)
	}
	if lines := markerLineCount(t, marker); lines != 2 {
		t.Fatalf("acquisitions after later explicit sync = %d, want 2", lines)
	}
}

func TestKilledSyncParentCannotLeaveMutationOrLocksBehind(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	marker := filepath.Join(t.TempDir(), "active-acquisition")
	writeFakeCrawlers(t, fakeCrawler{
		name:          "messages",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:          `{"state":"ok","added":1}`,
		syncSleep:     "10s",
		prepareMarker: marker,
	})
	ensureFakeArchives(t)

	parent := syncCallerProcess(t)
	var parentOut, parentErr bytes.Buffer
	parent.Stdout, parent.Stderr = &parentOut, &parentErr
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	waitForMarkerLines(t, marker, 1)
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := parent.Wait(); err == nil {
		t.Fatal("killed sync parent exited successfully")
	}
	waitForSourceLockRelease(t, home, "imessage")
	batch, err := acquireSyncBatchLock("")
	if err != nil {
		t.Fatalf("batch lock survived killed parent: %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatal(err)
	}

	writeFakeCrawlers(t, fakeCrawler{
		name:     "messages",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:     `{"state":"ok","added":1}`,
	})
	later := syncCallerProcess(t)
	if output, err := later.CombinedOutput(); err != nil {
		t.Fatalf("sync after killed parent err=%v output=%q", err, output)
	}
}

func TestKilledParentCannotOverlapImmediateDifferentSourceBatch(t *testing.T) {
	const attempts = 20
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	activeMarker := filepath.Join(t.TempDir(), "active-acquisition")
	probeMarker := filepath.Join(t.TempDir(), "different-source-acquisition")
	writeFakeCrawlers(t,
		fakeCrawler{name: "messages", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`, sync: `{"state":"ok","added":1}`, syncSleep: "10s", prepareMarker: activeMarker},
		fakeCrawler{name: "telegram", metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"telegram","display_name":"Telegram"}`, sync: `{"state":"ok","added":1}`, prepareMarker: probeMarker},
	)
	ensureFakeArchives(t)
	imessageLock := filepath.Join(home, ".opentrawl", "imessage", "run.lock")

	for attempt := 1; attempt <= attempts; attempt++ {
		parent := syncCallerProcess(t)
		parent.Env = append(parent.Env, "TRAWL_TEST_SYNC_SOURCE=imessage")
		if err := parent.Start(); err != nil {
			t.Fatal(err)
		}
		waitForMarkerLines(t, activeMarker, attempt)
		if err := parent.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := parent.Wait(); err == nil {
			t.Fatal("killed sync parent exited successfully")
		}

		next := syncCallerProcess(t)
		next.Env = append(next.Env,
			"TRAWL_TEST_SYNC_SOURCE=telegram",
			"TRAWL_TEST_PREPARE_PROBE_LOCK="+imessageLock,
		)
		if output, err := next.CombinedOutput(); err != nil {
			t.Fatalf("immediate different-source sync attempt %d err=%v output=%q", attempt, err, output)
		}
		data, err := os.ReadFile(probeMarker)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if strings.Contains(lines[len(lines)-1], "overlap=true") {
			t.Fatalf("different-source acquisition overlapped orphaned child on attempt %d: %s", attempt, lines[len(lines)-1])
		}
		waitForSourceLockRelease(t, home, "imessage")
	}
}

func syncCallerProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$") // #nosec G204 -- the current test binary is the controlled helper.
	cmd.Env = append(os.Environ(), "TRAWL_TEST_SYNC_CALLER=1")
	return cmd
}

func waitForMarkerLines(t *testing.T, path string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if markerLineCount(t, path) >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("marker %s did not reach %d lines", path, count)
}

func markerLineCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "\n")
}

func waitForSourceLockRelease(t *testing.T, home, source string) {
	t.Helper()
	path := filepath.Join(home, ".opentrawl", source, "run.lock")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
			_ = file.Close()
			return
		}
		_ = file.Close()
		if err != syscall.EWOULDBLOCK {
			t.Fatalf("probe source lock: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("source lock %s survived killed sync parent", path)
}
