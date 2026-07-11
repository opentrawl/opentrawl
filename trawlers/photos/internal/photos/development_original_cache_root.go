package photos

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type developmentVolume struct {
	MountPoint string
	Mounted    bool
	Local      bool
	External   bool
	Physical   bool
	Writable   bool
}

type developmentVolumeInspector func(string) (developmentVolume, error)

func validateDevelopmentCacheRoot(root, sourceRoot string, inspectVolume developmentVolumeInspector) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", errors.New("external development cache path is required")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve external development cache path: %w", err)
	}
	info, err := os.Lstat(absRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("external development cache root must already be mounted and created")
		}
		return "", fmt.Errorf("inspect external development cache root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("external development cache root must not be a symlink")
	}
	if !info.IsDir() {
		return "", errors.New("external development cache root must be a directory")
	}
	if info.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("external development cache root permissions are %04o, want 0700", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return "", errors.New("external development cache root is not owned by the current user")
	}
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("resolve external development cache root: %w", err)
	}
	canonicalSource, err := canonicalExistingPath(sourceRoot)
	if err != nil {
		return "", fmt.Errorf("resolve Photos source root: %w", err)
	}
	if pathsOverlap(canonicalRoot, canonicalSource) {
		return "", errors.New("external development cache root overlaps the Photos source root")
	}
	volume, err := inspectVolume(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("inspect external development cache volume: %w", err)
	}
	if !volume.Mounted || !volume.Local || !volume.External || !volume.Physical || !volume.Writable {
		return "", errors.New("development cache root must be on a mounted, writable, local external physical volume")
	}
	canonicalMount, err := canonicalExistingPath(volume.MountPoint)
	if err != nil || !pathContains(canonicalMount, canonicalRoot) {
		return "", errors.New("development cache root does not belong to the inspected mounted volume")
	}
	return canonicalRoot, nil
}

func ensureDevelopmentCacheLocks(root string) error {
	locks := filepath.Join(root, ".locks")
	info, err := os.Lstat(locks)
	if os.IsNotExist(err) {
		if err := os.Mkdir(locks, 0o700); err != nil {
			return fmt.Errorf("create development cache locks: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect development cache locks: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("development cache locks path must be a private directory, not a symlink")
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("development cache locks permissions are %04o, want 0700", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("development cache locks path is not owned by the current user")
	}
	return nil
}

func canonicalExistingPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(absPath); err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absPath)
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
