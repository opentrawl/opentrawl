package addressbook

import (
	"context"
	"os"

	"github.com/opentrawl/opentrawl/trawlkit/cache"
)

type Snapshot struct {
	SourcePath string
	Path       string
	root       string
}

func SnapshotPath(ctx context.Context, path string) (Snapshot, error) {
	root, err := os.MkdirTemp("", "imsgcrawl-addressbook-*")
	if err != nil {
		return Snapshot{}, err
	}
	result, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{SourcePath: path, DestinationDir: root})
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
