package notesdb

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/filecopy"
)

func SnapshotPath(path string) (Snapshot, error) {
	if path == "" {
		var err error
		path, err = DefaultStorePath()
		if err != nil {
			return Snapshot{}, err
		}
	}
	if err := validateSourceFile(path); err != nil {
		return Snapshot{}, err
	}
	for attempt := 1; attempt <= 3; attempt++ {
		before, err := triadRaceStats(path)
		if err != nil {
			return Snapshot{}, err
		}
		root, err := os.MkdirTemp("", "opentrawl-notes-store-*")
		if err != nil {
			return Snapshot{}, err
		}
		target := filepath.Join(root, filepath.Base(path))
		if err := copyTriad(path, target); err != nil {
			_ = os.RemoveAll(root)
			return Snapshot{}, err
		}
		after, err := triadRaceStats(path)
		if err != nil {
			_ = os.RemoveAll(root)
			return Snapshot{}, err
		}
		if before.same(after) {
			return Snapshot{SourcePath: path, Path: target, root: root}, nil
		}
		_ = os.RemoveAll(root)
	}
	return Snapshot{}, fmt.Errorf("notes store changed while copying; retry when Notes is idle")
}

func validateSourceFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("notes database is not a regular file")
	}
	return nil
}

type fileRaceStat struct {
	exists  bool
	size    int64
	modTime time.Time
}

type triadStats struct {
	base fileRaceStat
	wal  fileRaceStat
}

func triadRaceStats(path string) (triadStats, error) {
	base, err := raceStat(path, true)
	if err != nil {
		return triadStats{}, err
	}
	wal, err := raceStat(path+"-wal", false)
	if err != nil {
		return triadStats{}, err
	}
	return triadStats{base: base, wal: wal}, nil
}

func raceStat(path string, required bool) (fileRaceStat, error) {
	info, err := os.Stat(path)
	if os.IsNotExist(err) && !required {
		return fileRaceStat{}, nil
	}
	if err != nil {
		return fileRaceStat{}, err
	}
	if !info.Mode().IsRegular() {
		return fileRaceStat{}, fmt.Errorf("notes store file is not regular")
	}
	return fileRaceStat{exists: true, size: info.Size(), modTime: info.ModTime()}, nil
}

func (s triadStats) same(other triadStats) bool {
	return s.base.same(other.base) && s.wal.same(other.wal)
}

func (s fileRaceStat) same(other fileRaceStat) bool {
	return s.exists == other.exists && s.size == other.size && s.modTime.Equal(other.modTime)
}

func copyTriad(path, target string) error {
	for _, suffix := range []string{"", "-wal"} {
		src := path + suffix
		if suffix != "" {
			if _, err := os.Stat(src); os.IsNotExist(err) {
				continue
			} else if err != nil {
				return err
			}
		}
		if err := filecopy.CopyFile(src, target+suffix); err != nil {
			return err
		}
	}
	return nil
}

func (s Snapshot) Close() error {
	if s.root == "" {
		return nil
	}
	return os.RemoveAll(s.root)
}
