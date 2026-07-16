package cli

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	appv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/app/v1"
	"google.golang.org/protobuf/proto"
)

func TestCLIAndAppSyncShareCanonicalPreparationAndLock(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	prepareMarker := filepath.Join(t.TempDir(), "prepare.txt")
	writeFakeCrawlers(t, fakeCrawler{
		name:          "messages",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:          `{"state":"ok","added":1}`,
		prepareMarker: prepareMarker,
	})

	stdout, stderr, code := runCLI(t, "sync", "imessage")
	if code != 0 {
		t.Fatalf("CLI sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	stdout, stderr, code = runCLI(t, "__app", "sync", "--source", "imessage")
	if code != 0 || stderr != "" {
		t.Fatalf("app sync code=%d stderr=%q", code, stderr)
	}
	response := decodeAppSync(t, []byte(stdout))
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_COMPLETE || len(response.GetSources()) != 1 {
		t.Fatalf("app sync response = %#v", response)
	}

	archive := filepath.Join(home, ".opentrawl", "imessage", "imessage.db")
	data, err := os.ReadFile(prepareMarker)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != archive+"\n"+archive+"\n" {
		t.Fatalf("preparation calls = %q", data)
	}

	lock := holdSourceLock(t, home, "imessage")
	defer releaseSourceLock(t, lock)
	if _, _, code := runCLI(t, "sync", "imessage"); code == 0 {
		t.Fatal("CLI sync bypassed the canonical source lock")
	}
	stdout, _, code = runCLI(t, "__app", "sync", "--source", "imessage")
	if code != 0 {
		t.Fatalf("app helper transport code=%d", code)
	}
	response = decodeAppSync(t, []byte(stdout))
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_FAILED {
		t.Fatalf("app sync bypassed the canonical source lock: %#v", response)
	}
	after, err := os.ReadFile(prepareMarker)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Fatalf("preparation ran past lock: before=%q after=%q", data, after)
	}
}

func TestCancelledCanonicalSyncReleasesChildAndSourceLock(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	prepareMarker := filepath.Join(t.TempDir(), "prepare.txt")
	writeFakeCrawlers(t, fakeCrawler{
		name:          "messages",
		metadata:      `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:          `{"state":"ok","added":1}`,
		syncSleep:     "10s",
		prepareMarker: prepareMarker,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if data, err := os.ReadFile(prepareMarker); err == nil && len(data) > 0 {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	runtime := &Runtime{ctx: ctx}
	source := discoverCrawlers(ctx)[0]
	if _, _, err := runtime.runSourceSync(source, nil); err == nil {
		t.Fatal("cancelled sync returned nil error")
	}
	if _, err := os.Stat(prepareMarker); err != nil {
		t.Fatalf("sync was cancelled before entering the mutation lifecycle: %v", err)
	}

	lock := holdSourceLock(t, home, "imessage")
	releaseSourceLock(t, lock)
}

func TestFailedCanonicalSyncReleasesSourceLock(t *testing.T) {
	home := syntheticHome(t)
	t.Setenv("HOME", home)
	writeFakeCrawlers(t, fakeCrawler{
		name:     "messages",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:     `{"error":{"code":"permission_denied","message":"Synthetic source failure."}}`,
		syncExit: 1,
	})

	if _, _, code := runCLI(t, "sync", "imessage"); code == 0 {
		t.Fatal("failed sync returned success")
	}
	lock := holdSourceLock(t, home, "imessage")
	releaseSourceLock(t, lock)
}

func TestCanonicalSyncPreservesPartialResultForCLIAndApp(t *testing.T) {
	t.Setenv("HOME", syntheticHome(t))
	writeFakeCrawlers(t, fakeCrawler{
		name:     "messages",
		metadata: `{"schema_version":1,"contract_version":1,"capabilities":["status","sync","search","open"],"id":"imessage","display_name":"Messages"}`,
		sync:     `{"state":"ok","warnings":["Synthetic source warning."]}`,
	})

	stdout, stderr, code := runCLI(t, "--json", "sync", "imessage")
	if code != 3 || !strings.Contains(stdout, `"state":"partial"`) || !strings.Contains(stdout, "Synthetic source warning.") {
		t.Fatalf("CLI partial result code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	stdout, stderr, code = runCLI(t, "__app", "sync", "--source", "imessage")
	if code != 0 || stderr != "" {
		t.Fatalf("app partial transport code=%d stderr=%q", code, stderr)
	}
	response := decodeAppSync(t, []byte(stdout))
	if response.GetOutcome() != appv1.OperationOutcome_OPERATION_OUTCOME_PARTIAL || len(response.GetFailures()) != 1 || response.GetFailures()[0].GetMessage() != "Synthetic source warning." {
		t.Fatalf("app partial response = %#v", response)
	}
}

func TestProductionCLIHasNoDirectMutationBypass(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	root := filepath.Dir(current)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, forbidden := range []string{"sourceStoreWrite", "syncer.Sync("} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("production CLI mutation bypass %q remains in %s", forbidden, filepath.Base(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func decodeAppSync(t *testing.T, frame []byte) *appv1.SyncResponse {
	t.Helper()
	if len(frame) < 4 || int(binary.LittleEndian.Uint32(frame[:4])) != len(frame)-4 {
		t.Fatalf("invalid app frame length %d", len(frame))
	}
	var response appv1.SyncResponse
	if err := proto.Unmarshal(frame[4:], &response); err != nil {
		t.Fatal(err)
	}
	return &response
}

func holdSourceLock(t *testing.T, home, source string) *os.File {
	t.Helper()
	base := filepath.Join(home, ".opentrawl", source)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(base, "run.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	return file
}

func releaseSourceLock(t *testing.T, file *os.File) {
	t.Helper()
	if file == nil {
		return
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
