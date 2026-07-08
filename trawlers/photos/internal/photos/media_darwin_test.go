//go:build darwin

package photos

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

const (
	exportLockHelperEnv   = "PHOTOSCRAWL_EXPORT_LOCK_HELPER"
	exportLockPathEnv     = "PHOTOSCRAWL_EXPORT_LOCK_PATH"
	exportLockOwnerEnv    = "PHOTOSCRAWL_EXPORT_LOCK_OWNER"
	exportLockReadyEnv    = "PHOTOSCRAWL_EXPORT_LOCK_READY"
	exportLockLiveOwner   = "live"
	exportLockHelperSleep = 10 * time.Minute
)

func TestAcquireExportLockWritesOwnerPID(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "original.jpeg")
	lock, err := acquireExportLock(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	pid, ok, err := readExportLockOwner(exportLockPath(destinationPath))
	if err != nil {
		t.Fatal(err)
	}
	if !ok || pid != os.Getpid() {
		t.Fatalf("lock owner pid = %d ok=%t, want %d", pid, ok, os.Getpid())
	}
}

func TestAcquireExportLockReplacesDeadPIDFile(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "original.jpeg")
	lockPath := exportLockPath(destinationPath)
	deadPID := deadPID(t)
	if err := os.WriteFile(lockPath, []byte(fmt.Sprintf("%d\n", deadPID)), 0o600); err != nil {
		t.Fatal(err)
	}

	lock, err := acquireExportLock(destinationPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	pid, ok, err := readExportLockOwner(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || pid != os.Getpid() {
		t.Fatalf("lock owner pid = %d ok=%t, want %d", pid, ok, os.Getpid())
	}
}

func TestAcquireExportLockLeavesLivePIDConflict(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "original.jpeg")
	lockPath := exportLockPath(destinationPath)
	cmd, cleanup := startExportLockHelper(t, lockPath, exportLockLiveOwner)
	defer cleanup()

	lock, err := acquireExportLock(destinationPath)
	if lock != nil {
		defer lock.Close()
	}
	if !errors.Is(err, ErrExportAlreadyRunning) {
		t.Fatalf("acquire error = %v, want %v", err, ErrExportAlreadyRunning)
	}

	pid, ok, err := readExportLockOwner(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || pid != cmd.Process.Pid {
		t.Fatalf("lock owner pid = %d ok=%t, want helper pid %d", pid, ok, cmd.Process.Pid)
	}
}

func TestAcquireExportLockRemovesDeadPIDConflict(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "original.jpeg")
	lockPath := exportLockPath(destinationPath)
	cmd, cleanup := startExportLockHelper(t, lockPath, strconv.Itoa(deadPID(t)))
	defer cleanup()

	lock, err := acquireExportLock(destinationPath)
	if err != nil {
		t.Fatalf("acquire with stale owner held by helper pid %d: %v", cmd.Process.Pid, err)
	}
	defer lock.Close()

	pid, ok, err := readExportLockOwner(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || pid != os.Getpid() {
		t.Fatalf("lock owner pid = %d ok=%t, want %d", pid, ok, os.Getpid())
	}
}

func TestExportLockHelperProcess(t *testing.T) {
	if os.Getenv(exportLockHelperEnv) != "1" {
		return
	}
	lockPath := os.Getenv(exportLockPathEnv)
	owner := os.Getenv(exportLockOwnerEnv)
	readyPath := os.Getenv(exportLockReadyEnv)
	if lockPath == "" || owner == "" || readyPath == "" {
		t.Fatal("lock helper env is incomplete")
	}
	if owner == exportLockLiveOwner {
		owner = strconv.Itoa(os.Getpid())
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := fmt.Fprintln(file, owner); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()
	if err := os.WriteFile(readyPath, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(exportLockHelperSleep)
}

func exportLockPath(destinationPath string) string {
	return filepath.Join(filepath.Dir(destinationPath), ".photokit-export.lock")
}

func deadPID(t *testing.T) int {
	t.Helper()
	for pid := 999999; pid >= 900000; pid-- {
		if !processAlive(pid) {
			return pid
		}
	}
	t.Fatal("no dead pid found")
	return 0
}

func startExportLockHelper(t *testing.T, lockPath, owner string) (*exec.Cmd, func()) {
	t.Helper()
	readyPath := lockPath + ".ready"
	cmd := exec.Command(os.Args[0], "-test.run=TestExportLockHelperProcess")
	cmd.Env = append(os.Environ(),
		exportLockHelperEnv+"=1",
		exportLockPathEnv+"="+lockPath,
		exportLockOwnerEnv+"="+owner,
		exportLockReadyEnv+"="+readyPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			return cmd, cleanup
		}
		if time.Now().After(deadline) {
			cleanup()
			t.Fatalf("lock helper did not report ready for %s", lockPath)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
