package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type TrawlerArchiveFileSetLock struct {
	file *os.File
}

func AcquireExclusiveTrawlerArchiveFileSetLock(trawlerArchivePath string) (*TrawlerArchiveFileSetLock, error) {
	return acquireTrawlerArchiveFileSetLock(trawlerArchivePath, syscall.LOCK_EX)
}

func acquireSharedTrawlerArchiveFileSetLock(trawlerArchivePath string) (*TrawlerArchiveFileSetLock, error) {
	return acquireTrawlerArchiveFileSetLock(trawlerArchivePath, syscall.LOCK_SH)
}

func acquireTrawlerArchiveFileSetLock(trawlerArchivePath string, lockOperation int) (*TrawlerArchiveFileSetLock, error) {
	trawlerArchivePath = strings.TrimSpace(trawlerArchivePath)
	if trawlerArchivePath == "" {
		return nil, errors.New("trawler archive path is required")
	}
	if err := os.MkdirAll(filepath.Dir(trawlerArchivePath), 0o755); err != nil {
		return nil, fmt.Errorf("create trawler archive directory: %w", err)
	}
	lockFile, err := os.OpenFile(trawlerArchivePath+".archive-file-set.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open trawler archive file set lock: %w", err)
	}
	if err := syscall.Flock(int(lockFile.Fd()), lockOperation); err != nil {
		_ = lockFile.Close()
		return nil, fmt.Errorf("acquire trawler archive file set lock: %w", err)
	}
	return &TrawlerArchiveFileSetLock{file: lockFile}, nil
}

func (lock *TrawlerArchiveFileSetLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockError := syscall.Flock(int(lock.file.Fd()), syscall.LOCK_UN)
	closeError := lock.file.Close()
	lock.file = nil
	return errors.Join(unlockError, closeError)
}
