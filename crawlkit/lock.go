package crawlkit

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/openclaw/crawlkit/output"
)

type runLock struct {
	file *os.File
	path string
}

func acquireRunLock(base string) (*runLock, error) {
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	path := filepath.Join(base, "run.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open run lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if err == syscall.EWOULDBLOCK {
			return nil, lockHeldError{path: path}
		}
		return nil, fmt.Errorf("lock archive: %w", err)
	}
	if err := file.Truncate(0); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("clear run lock: %w", err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d started_at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339)); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write run lock: %w", err)
	}
	return &runLock{file: file, path: path}, nil
}

func (l *runLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	return l.file.Close()
}

type lockHeldError struct {
	path string
}

func (e lockHeldError) Error() string {
	return "another run is already using this archive"
}

func (e lockHeldError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{
		Code:    "archive_busy",
		Message: e.Error(),
		Remedy:  "wait for the other run to finish, then run the command again",
		Fields:  map[string]any{"lock_path": e.path},
	}
}
