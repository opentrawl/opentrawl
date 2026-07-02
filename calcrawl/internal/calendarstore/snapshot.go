package calendarstore

import (
	"errors"
	"io"
	"os"
	"path/filepath"
)

type Snapshot struct {
	SourcePath string
	Path       string
	root       string
}

func SnapshotPath(path string) (Snapshot, error) {
	if path == "" {
		path = DefaultPath()
	}
	root, err := os.MkdirTemp("", "calcrawl-snapshot-*")
	if err != nil {
		return Snapshot{}, err
	}
	dest := filepath.Join(root, filepath.Base(path))
	if err := copySQLiteBundle(path, dest); err != nil {
		_ = os.RemoveAll(root)
		return Snapshot{}, err
	}
	return Snapshot{SourcePath: path, Path: dest, root: root}, nil
}

func (s Snapshot) Close() error {
	if s.root == "" {
		return nil
	}
	return os.RemoveAll(s.root)
}

func copySQLiteBundle(source, dest string) error {
	if err := copyFile(source, dest); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		err := copyFile(source+suffix, dest+suffix)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			continue
		}
		return err
	}
	return nil
}

func copyFile(source, dest string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
