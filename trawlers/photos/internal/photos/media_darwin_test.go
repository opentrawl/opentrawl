//go:build darwin

package photos

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const (
	exportLockHelperEnv   = "PHOTOSCRAWL_EXPORT_LOCK_HELPER"
	exportLockPathEnv     = "PHOTOSCRAWL_EXPORT_LOCK_PATH"
	exportLockReadyEnv    = "PHOTOSCRAWL_EXPORT_LOCK_READY"
	exportLockHelperSleep = 10 * time.Minute
)

func TestPhotoKitAccessErrorsGiveExactRemedy(t *testing.T) {
	tests := []struct {
		status string
		want   string
	}{
		{
			status: "not_determined",
			want:   "Photos access has not been granted to Photoscrawl Fetch; open Photoscrawl Fetch in Applications, approve the macOS prompt, then retry",
		},
		{
			status: "denied",
			want:   "Photos access is denied for Photoscrawl Fetch; enable Photoscrawl Fetch in System Settings > Privacy & Security > Photos, then retry",
		},
		{
			status: "restricted",
			want:   "Photos access is restricted by macOS for Photoscrawl Fetch",
		},
	}
	for _, test := range tests {
		t.Run(test.status, func(t *testing.T) {
			err := photoKitError(photoLibraryAccessErrorPrefix + test.status)
			if !IsPhotoLibraryAccessError(err) {
				t.Fatalf("error type = %T, want PhotoLibraryAccessError", err)
			}
			var accessErr *PhotoLibraryAccessError
			if !errors.As(err, &accessErr) || accessErr.Status != test.status {
				t.Fatalf("access error = %#v", accessErr)
			}
			if err.Error() != test.want {
				t.Fatalf("error = %q, want %q", err, test.want)
			}
		})
	}
}

func TestPhotoKitExportErrorPreservesSafeDomainAndCode(t *testing.T) {
	exportErr := NewPhotoKitExportError("PHPhotosErrorDomain", 3303, "")
	if exportErr.Domain != "PHPhotosErrorDomain" || exportErr.Code != 3303 {
		t.Fatalf("PhotoKit error = %#v", exportErr)
	}
	if strings.ContainsAny(exportErr.Error(), "/\\\r\n") {
		t.Fatalf("PhotoKit error is not safe for logs: %q", exportErr.Error())
	}
}

func TestAcquireExportLockWaitsForCurrentExport(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "original.jpeg")
	lockPath := exportLockPath(destinationPath)
	_, release := startExportLockHelper(t, lockPath)

	type result struct {
		lock *exportLock
		err  error
	}
	done := make(chan result, 1)
	go func() {
		lock, err := acquireExportLock(context.Background(), destinationPath)
		done <- result{lock: lock, err: err}
	}()
	select {
	case got := <-done:
		if got.lock != nil {
			got.lock.Close()
		}
		release()
		t.Fatalf("lock returned before current export ended: %v", got.err)
	case <-time.After(75 * time.Millisecond):
	}
	release()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		got.lock.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("waiting export did not acquire the released lock")
	}
}

func TestAcquireExportLockHonoursContext(t *testing.T) {
	destinationPath := filepath.Join(t.TempDir(), "original.jpeg")
	_, release := startExportLockHelper(t, exportLockPath(destinationPath))
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	lock, err := acquireExportLock(ctx, destinationPath)
	if lock != nil {
		lock.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("acquire error = %v, want context deadline", err)
	}
}

func TestExportLockHelperProcess(t *testing.T) {
	if os.Getenv(exportLockHelperEnv) != "1" {
		return
	}
	lockPath := os.Getenv(exportLockPathEnv)
	readyPath := os.Getenv(exportLockReadyEnv)
	if lockPath == "" || readyPath == "" {
		t.Fatal("lock helper env is incomplete")
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
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

func startExportLockHelper(t *testing.T, lockPath string) (*exec.Cmd, func()) {
	t.Helper()
	readyPath := lockPath + ".ready"
	cmd := exec.Command(os.Args[0], "-test.run=TestExportLockHelperProcess")
	cmd.Env = append(os.Environ(),
		exportLockHelperEnv+"=1",
		exportLockPathEnv+"="+lockPath,
		exportLockReadyEnv+"="+readyPath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
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
