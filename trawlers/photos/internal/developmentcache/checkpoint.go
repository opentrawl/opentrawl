package developmentcache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const checkpointSchema = `
create table if not exists completed_original (
  source_library_id text not null,
  snapshot_id text not null,
  cache_key text not null,
  asset_id text not null,
  size_bytes integer not null,
  sha256 text not null,
  completed_at text not null,
  primary key (source_library_id, snapshot_id, cache_key)
);
`

type Completion struct {
	AssetID   string
	SizeBytes int64
	SHA256    string
}

type Checkpoint struct {
	db  *sql.DB
	now func() time.Time
}

func OpenCheckpoint(ctx context.Context, paths StoragePaths) (*Checkpoint, error) {
	paths, err := ValidateStoragePaths(paths)
	if err != nil {
		return nil, err
	}
	path, err := prepareCheckpointPath(paths.StatePath)
	if err != nil {
		return nil, err
	}
	query := url.Values{
		"_busy_timeout": {"5000"},
		"_foreign_keys": {"on"},
		"_journal_mode": {"WAL"},
		"_synchronous":  {"FULL"},
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open development cache checkpoint: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, checkpointSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialise development cache checkpoint: %w", err)
	}
	return &Checkpoint{db: db, now: func() time.Time { return time.Now().UTC() }}, nil
}

func prepareCheckpointPath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve development cache checkpoint path: %w", err)
	}
	parent := filepath.Dir(absPath)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return "", fmt.Errorf("development cache checkpoint directory must already exist: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() || parentInfo.Mode().Perm() != 0o700 || !ownedByCurrentUser(parentInfo) {
		return "", errors.New("development cache checkpoint directory must be an owner-only 0700 directory, not a symlink")
	}
	info, err := os.Lstat(absPath)
	switch {
	case os.IsNotExist(err):
		file, createErr := os.OpenFile(absPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if createErr != nil {
			return "", fmt.Errorf("create development cache checkpoint: %w", createErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return "", fmt.Errorf("close development cache checkpoint: %w", closeErr)
		}
	case err != nil:
		return "", fmt.Errorf("inspect development cache checkpoint: %w", err)
	case info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ownedByCurrentUser(info):
		return "", errors.New("development cache checkpoint must be an owner-only 0600 regular file, not a symlink")
	}
	return absPath, nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Geteuid()
}

func (c *Checkpoint) Close() error {
	if c == nil || c.db == nil {
		return nil
	}
	return c.db.Close()
}

func (c *Checkpoint) Completed(ctx context.Context, sourceLibraryID, snapshotID string) (map[string]Completion, error) {
	if c == nil || c.db == nil {
		return nil, errors.New("development cache checkpoint is not open")
	}
	rows, err := c.db.QueryContext(ctx, `
select cache_key, asset_id, size_bytes, sha256
from completed_original
where source_library_id = ? and snapshot_id = ?
order by cache_key
`, sourceLibraryID, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("read development cache checkpoint: %w", err)
	}
	defer func() { _ = rows.Close() }()
	completed := map[string]Completion{}
	for rows.Next() {
		var key string
		var completion Completion
		if err := rows.Scan(&key, &completion.AssetID, &completion.SizeBytes, &completion.SHA256); err != nil {
			return nil, fmt.Errorf("scan development cache checkpoint: %w", err)
		}
		completed[key] = completion
	}
	return completed, rows.Err()
}

func (c *Checkpoint) Record(ctx context.Context, sourceLibraryID, snapshotID, cacheKey string, completion Completion) error {
	if c == nil || c.db == nil {
		return errors.New("development cache checkpoint is not open")
	}
	if strings.TrimSpace(sourceLibraryID) == "" || strings.TrimSpace(snapshotID) == "" || strings.TrimSpace(cacheKey) == "" || strings.TrimSpace(completion.AssetID) == "" || completion.SizeBytes <= 0 || strings.TrimSpace(completion.SHA256) == "" {
		return errors.New("checked development cache completion is incomplete")
	}
	now := c.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	_, err := c.db.ExecContext(ctx, `
insert into completed_original(source_library_id, snapshot_id, cache_key, asset_id, size_bytes, sha256, completed_at)
values (?, ?, ?, ?, ?, ?, ?)
on conflict(source_library_id, snapshot_id, cache_key) do update set
  asset_id = excluded.asset_id,
  size_bytes = excluded.size_bytes,
  sha256 = excluded.sha256,
  completed_at = excluded.completed_at
`, sourceLibraryID, snapshotID, cacheKey, completion.AssetID, completion.SizeBytes, completion.SHA256, now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("record checked development cache completion: %w", err)
	}
	return nil
}
