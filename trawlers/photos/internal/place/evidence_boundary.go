package place

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func ensurePrivateOutputRoot(path string) error {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "." || !filepath.IsAbs(clean) {
		return errors.New("private output root must be an absolute path")
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("private output root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("private output root must be a non-symlink directory")
	}
	if info.Mode().Perm() != 0o700 {
		return errors.New("private output root permissions must be 0700")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("private output root must be owned by the current user")
	}
	for current := clean; ; current = filepath.Dir(current) {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return errors.New("private output root must be outside a repository or worktree")
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return nil
}

func ensurePrivateInputFile(path string) error {
	clean := filepath.Clean(strings.TrimSpace(path))
	if !filepath.IsAbs(clean) {
		return errors.New("private input file must use an absolute path")
	}
	if err := ensurePrivateOutputRoot(filepath.Dir(clean)); err != nil {
		return err
	}
	info, err := os.Lstat(clean)
	if err != nil {
		return fmt.Errorf("private input file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("private input file must be a non-symlink regular file with permissions 0600")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("private input file must be owned by the current user")
	}
	return nil
}
