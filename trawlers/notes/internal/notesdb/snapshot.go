package notesdb

import (
	"context"
	"os"
	"path/filepath"

	"github.com/opentrawl/opentrawl/trawlkit/cache"
)

func SnapshotPath(ctx context.Context, path string) (Snapshot, error) {
	if path == "" {
		var err error
		path, err = DefaultStorePath()
		if err != nil {
			return Snapshot{}, err
		}
	}
	root, err := os.MkdirTemp("", "opentrawl-notes-store-*")
	if err != nil {
		return Snapshot{}, err
	}
	result, err := cache.SnapshotSQLite(ctx, cache.SQLiteSnapshotOptions{
		SourcePath:     path,
		DestinationDir: root,
		Name:           filepath.Base(path),
		PreserveWAL:    true,
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
