package messages

import (
	"fmt"
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
		path = DefaultChatDBPath()
	}
	info, err := os.Stat(path)
	if err != nil {
		return Snapshot{}, err
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, fmt.Errorf("messages database is not a regular file: %s", path)
	}
	root, err := os.MkdirTemp("", "imsgcrawl-snapshot-*")
	if err != nil {
		return Snapshot{}, err
	}
	target := filepath.Join(root, filepath.Base(path))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFileIfExists(path+suffix, target+suffix); err != nil {
			_ = os.RemoveAll(root)
			return Snapshot{}, err
		}
	}
	return Snapshot{SourcePath: path, Path: target, root: root}, nil
}

func (s Snapshot) Close() error {
	if s.root == "" {
		return nil
	}
	return os.RemoveAll(s.root)
}

func copyFileIfExists(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- source path is the local Messages database selected for read-only snapshotting.
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = in.Close() }()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- destination is inside a mktemp snapshot dir.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
