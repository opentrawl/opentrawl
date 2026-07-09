package calendarstore

import (
	"context"
	"os"
	"path/filepath"

	"github.com/opentrawl/opentrawl/trawlkit/cache"
)

type Snapshot struct {
	SourcePath string
	Path       string
	root       string
}

func SnapshotPath(ctx context.Context, path string) (Snapshot, error) {
	if path == "" {
		path = DefaultPath()
	}
	root, err := os.MkdirTemp("", "calcrawl-snapshot-*")
	if err != nil {
		return Snapshot{}, err
	}
	result, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     path,
		DestinationDir: root,
		Name:           filepath.Base(path),
	})
	if err != nil {
		_ = os.RemoveAll(root)
		return Snapshot{}, err
	}
	return Snapshot{SourcePath: path, Path: result.Path, root: root}, nil
}

func (s Snapshot) Close() error {
	if s.root == "" {
		return nil
	}
	return os.RemoveAll(s.root)
}
