package developmentcache

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type StoragePaths struct {
	ArchivePath string
	SourceRoot  string
	CacheRoot   string
	StatePath   string
}

var currentEffectiveUID = os.Geteuid

func ValidateStoragePaths(paths StoragePaths) (StoragePaths, error) {
	archivePath, archiveInfo, err := existingPathWithoutSymlinks(paths.ArchivePath)
	if err != nil {
		return StoragePaths{}, fmt.Errorf("validate read-only Photos archive: %w", err)
	}
	if !archiveInfo.Mode().IsRegular() || !ownedPath(archiveInfo) || archiveInfo.Mode().Perm()&0o022 != 0 {
		return StoragePaths{}, errors.New("read-only Photos archive must be an owner-controlled regular file")
	}
	sourceRoot, sourceInfo, err := existingPathWithoutSymlinks(paths.SourceRoot)
	if err != nil {
		return StoragePaths{}, fmt.Errorf("validate Photos source root: %w", err)
	}
	if !sourceInfo.IsDir() || !ownedPath(sourceInfo) || sourceInfo.Mode().Perm()&0o022 != 0 {
		return StoragePaths{}, errors.New("Photos source root must be an owner-controlled directory")
	}
	cacheRoot, cacheInfo, err := existingPathWithoutSymlinks(paths.CacheRoot)
	if err != nil {
		return StoragePaths{}, fmt.Errorf("validate development cache root: %w", err)
	}
	if !cacheInfo.IsDir() || !ownedPath(cacheInfo) || cacheInfo.Mode().Perm() != 0o700 {
		return StoragePaths{}, errors.New("development cache root must be an owner-only 0700 directory")
	}
	statePath, stateInfo, err := statePathWithoutSymlinks(paths.StatePath)
	if err != nil {
		return StoragePaths{}, fmt.Errorf("validate development cache state: %w", err)
	}
	if stateInfo != nil && (!stateInfo.Mode().IsRegular() || !ownedPath(stateInfo) || stateInfo.Mode().Perm() != 0o600) {
		return StoragePaths{}, errors.New("development cache state must be an owner-only 0600 regular file")
	}
	if stateInfo != nil && os.SameFile(archiveInfo, stateInfo) {
		return StoragePaths{}, errors.New("development cache state aliases the read-only Photos archive")
	}

	checked := []struct {
		name string
		path string
	}{
		{name: "archive", path: archivePath},
		{name: "source", path: sourceRoot},
		{name: "cache", path: cacheRoot},
		{name: "state", path: statePath},
	}
	for i := range checked {
		for j := i + 1; j < len(checked); j++ {
			if storagePathsOverlap(checked[i].path, checked[j].path) {
				return StoragePaths{}, fmt.Errorf("development cache %s path overlaps the %s path", checked[i].name, checked[j].name)
			}
		}
	}
	return StoragePaths{ArchivePath: archivePath, SourceRoot: sourceRoot, CacheRoot: cacheRoot, StatePath: statePath}, nil
}

func existingPathWithoutSymlinks(path string) (string, os.FileInfo, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", nil, err
	}
	if err := rejectSymlinkComponents(absPath, false); err != nil {
		return "", nil, err
	}
	info, err := os.Lstat(absPath)
	return absPath, info, err
}

func statePathWithoutSymlinks(path string) (string, os.FileInfo, error) {
	absPath, err := filepath.Abs(strings.TrimSpace(path))
	if err != nil {
		return "", nil, err
	}
	if err := rejectSymlinkComponents(absPath, true); err != nil {
		return "", nil, err
	}
	parentInfo, err := os.Lstat(filepath.Dir(absPath))
	if err != nil {
		return "", nil, err
	}
	if !parentInfo.IsDir() || !ownedPath(parentInfo) || parentInfo.Mode().Perm() != 0o700 {
		return "", nil, errors.New("state directory must be an owner-only 0700 directory")
	}
	info, err := os.Lstat(absPath)
	if os.IsNotExist(err) {
		return absPath, nil, nil
	}
	return absPath, info, err
}

func rejectSymlinkComponents(absPath string, allowMissingFinal bool) error {
	clean := filepath.Clean(absPath)
	if !filepath.IsAbs(clean) {
		return errors.New("path must be absolute")
	}
	current := string(filepath.Separator)
	parts := strings.Split(strings.TrimPrefix(clean, current), string(filepath.Separator))
	for index, part := range parts {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) && allowMissingFinal && index == len(parts)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path contains symlink component %q", current)
		}
	}
	return nil
}

func storagePathsOverlap(first, second string) bool {
	return storagePathContains(first, second) || storagePathContains(second, first)
}

func storagePathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)))
}

func ownedPath(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == currentEffectiveUID()
}
