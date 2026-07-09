package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SQLiteSnapshotOptions struct {
	SourcePath     string
	DestinationDir string
	Name           string
	MaxFileBytes   int64
	// PreserveWAL keeps copied WAL frames beside the snapshot. It is only for
	// callers whose data contract reads WAL frames as source data, such as the
	// Notes version extractor. Other callers use the default checkpointed
	// single-file snapshot.
	PreserveWAL bool
}

type SQLiteSnapshot struct {
	SourcePath string   `json:"source_path"`
	Path       string   `json:"path"`
	Files      []string `json:"files"`
	SizeBytes  int64    `json:"size_bytes"`
}

// SnapshotSQLite writes one SQLite snapshot and returns a consistent
// point-in-time view. Under a concurrently writing source it may be a slightly
// earlier consistent view.
// The caller owns DestinationDir and decides whether snapshots are temporary
// or retained.
func SnapshotSQLite(ctx context.Context, opts SQLiteSnapshotOptions) (SQLiteSnapshot, error) {
	return snapshotSQLite(ctx, opts, nil)
}

func snapshotSQLite(ctx context.Context, opts SQLiteSnapshotOptions, afterCopy func(attempt int, path string)) (SQLiteSnapshot, error) {
	source := strings.TrimSpace(opts.SourcePath)
	if source == "" {
		return SQLiteSnapshot{}, errors.New("sqlite source path is required")
	}
	destination := strings.TrimSpace(opts.DestinationDir)
	if destination == "" {
		return SQLiteSnapshot{}, errors.New("sqlite destination dir is required")
	}
	info, err := os.Stat(source)
	if err != nil {
		return SQLiteSnapshot{}, fmt.Errorf("stat sqlite source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return SQLiteSnapshot{}, fmt.Errorf("sqlite source is not a regular file: %s", source)
	}
	if opts.MaxFileBytes > 0 && info.Size() > opts.MaxFileBytes {
		return SQLiteSnapshot{}, fmt.Errorf("sqlite source file %s is %d bytes, exceeds limit %d", source, info.Size(), opts.MaxFileBytes)
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = filepath.Base(source)
	}
	if filepath.Base(name) != name || name == "." || name == ".." {
		return SQLiteSnapshot{}, fmt.Errorf("sqlite snapshot name must be a file name: %q", name)
	}
	opts.SourcePath = source
	opts.DestinationDir = destination
	opts.Name = name
	if err := ctx.Err(); err != nil {
		return SQLiteSnapshot{}, err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return SQLiteSnapshot{}, fmt.Errorf("create sqlite snapshot dir: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= sqliteSnapshotAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return SQLiteSnapshot{}, err
		}
		snapshot, err := snapshotSQLiteAttempt(ctx, opts, attempt, afterCopy)
		if err == nil {
			return snapshot, nil
		}
		lastErr = err
	}
	return SQLiteSnapshot{}, fmt.Errorf("snapshot sqlite after %d attempts: %w", sqliteSnapshotAttempts, lastErr)
}

const sqliteSnapshotAttempts = 3

var errSQLiteSourceChanged = errors.New("sqlite source changed during copy")

var sqliteCopyChunkBytes int64 = 1024 * 1024

type sqliteFamilyFingerprint map[string]sqliteFileFingerprint

type sqliteFileFingerprint struct {
	size    int64
	modTime time.Time
}

func snapshotSQLiteAttempt(ctx context.Context, opts SQLiteSnapshotOptions, attempt int, afterCopy func(attempt int, path string)) (SQLiteSnapshot, error) {
	source := opts.SourcePath
	destination := opts.DestinationDir
	name := opts.Name
	target := filepath.Join(destination, name)
	maxBytes := opts.MaxFileBytes

	workDir, err := os.MkdirTemp(destination, "."+name+".work-")
	if err != nil {
		return SQLiteSnapshot{}, fmt.Errorf("create sqlite snapshot work dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	workPath := filepath.Join(workDir, name)
	before, err := fingerprintSQLiteFamily(source)
	if err != nil {
		return SQLiteSnapshot{}, err
	}
	suffixes, _, err := copySQLiteFamily(ctx, source, workPath, maxBytes)
	if err != nil {
		return SQLiteSnapshot{}, err
	}
	after, err := fingerprintSQLiteFamily(source)
	if err != nil {
		return SQLiteSnapshot{}, err
	}
	sourceChanged := !before.Equal(after)
	if afterCopy != nil {
		afterCopy(attempt, workPath)
	}
	if sourceChanged && attempt < sqliteSnapshotAttempts {
		return SQLiteSnapshot{}, errSQLiteSourceChanged
	}
	if opts.PreserveWAL {
		if err := prepareOwnedSQLiteFamily(ctx, workPath); err != nil {
			return SQLiteSnapshot{}, err
		}
		return commitSQLiteFamily(ctx, source, workPath, target, suffixes, maxBytes)
	}

	tmp, err := os.CreateTemp(destination, "."+name+".tmp-")
	if err != nil {
		return SQLiteSnapshot{}, fmt.Errorf("create sqlite snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = removeSQLiteSnapshotFiles(tmpPath)
		return SQLiteSnapshot{}, fmt.Errorf("chmod sqlite snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = removeSQLiteSnapshotFiles(tmpPath)
		return SQLiteSnapshot{}, fmt.Errorf("close sqlite snapshot: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = removeSQLiteSnapshotFiles(tmpPath)
		}
	}()

	if err := prepareOwnedSQLiteSnapshot(ctx, workPath, tmpPath); err != nil {
		return SQLiteSnapshot{}, err
	}
	info, err := os.Stat(tmpPath)
	if err != nil {
		return SQLiteSnapshot{}, fmt.Errorf("stat sqlite snapshot: %w", err)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return SQLiteSnapshot{}, fmt.Errorf("sqlite snapshot file %s is %d bytes, exceeds limit %d", source, info.Size(), maxBytes)
	}
	if err := syncFile(tmpPath); err != nil {
		return SQLiteSnapshot{}, err
	}
	if err := removeSQLiteSidecars(target); err != nil {
		return SQLiteSnapshot{}, err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return SQLiteSnapshot{}, fmt.Errorf("commit sqlite snapshot: %w", err)
	}
	committed = true
	return SQLiteSnapshot{SourcePath: source, Path: target, Files: []string{target}, SizeBytes: info.Size()}, nil
}

func copySQLiteFamily(ctx context.Context, source, target string, maxBytes int64) ([]string, int64, error) {
	var total int64
	var suffixes []string
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		required := suffix == ""
		size, copied, err := copySQLiteFile(ctx, source+suffix, target+suffix, maxBytes, required)
		if err != nil {
			return nil, 0, err
		}
		if copied {
			suffixes = append(suffixes, suffix)
			total += size
		}
	}
	return suffixes, total, nil
}

func fingerprintSQLiteFamily(source string) (sqliteFamilyFingerprint, error) {
	fingerprint := sqliteFamilyFingerprint{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := source + suffix
		info, err := os.Stat(path)
		if err != nil {
			if suffix != "" && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("stat sqlite snapshot source: %w", err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("sqlite snapshot source is not a regular file: %s", path)
		}
		fingerprint[suffix] = sqliteFileFingerprint{
			size:    info.Size(),
			modTime: info.ModTime(),
		}
	}
	return fingerprint, nil
}

func (f sqliteFamilyFingerprint) Equal(other sqliteFamilyFingerprint) bool {
	if len(f) != len(other) {
		return false
	}
	for suffix, want := range f {
		got, ok := other[suffix]
		if !ok {
			return false
		}
		if got.size != want.size || !got.modTime.Equal(want.modTime) {
			return false
		}
	}
	return true
}

func SQLiteModifiedAfter(path string, cutoff time.Time) bool {
	path = strings.TrimSpace(path)
	if path == "" || cutoff.IsZero() {
		return false
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if info, err := os.Stat(path + suffix); err == nil && info.ModTime().After(cutoff) {
			return true
		}
	}
	return false
}

func copySQLiteFile(ctx context.Context, source, target string, maxBytes int64, required bool) (int64, bool, error) {
	in, err := os.Open(source) // #nosec G304 -- caller explicitly selects the local SQLite source.
	if err != nil {
		if !required && errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("open sqlite snapshot source: %w", err)
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return 0, false, fmt.Errorf("stat sqlite snapshot source: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, false, fmt.Errorf("sqlite snapshot source is not a regular file: %s", source)
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return 0, false, fmt.Errorf("sqlite snapshot file %s is %d bytes, exceeds limit %d", source, info.Size(), maxBytes)
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-")
	if err != nil {
		return 0, false, fmt.Errorf("create sqlite snapshot: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return 0, false, err
	}
	copied, err := copySQLiteBytes(ctx, tmp, in, source, maxBytes)
	if err != nil {
		_ = tmp.Close()
		return 0, false, err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return 0, false, err
	}
	if err := tmp.Close(); err != nil {
		return 0, false, err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return 0, false, fmt.Errorf("commit sqlite snapshot: %w", err)
	}
	committed = true
	return copied, true, nil
}

func copySQLiteBytes(ctx context.Context, dst io.Writer, src io.Reader, source string, maxBytes int64) (int64, error) {
	chunkBytes := sqliteCopyChunkBytes
	if chunkBytes <= 0 {
		chunkBytes = 1024 * 1024
	}
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return copied, err
		}
		limit := chunkBytes
		if maxBytes > 0 {
			remaining := maxBytes + 1 - copied
			if remaining <= 0 {
				return copied, fmt.Errorf("sqlite snapshot file %s exceeds limit %d", source, maxBytes)
			}
			if remaining < limit {
				limit = remaining
			}
		}
		n, err := io.CopyN(dst, src, limit)
		copied += n
		if maxBytes > 0 && copied > maxBytes {
			return copied, fmt.Errorf("sqlite snapshot file %s exceeds limit %d", source, maxBytes)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return copied, nil
		}
		return copied, err
	}
}
