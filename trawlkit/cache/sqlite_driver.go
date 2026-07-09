package cache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func prepareOwnedSQLiteSnapshot(ctx context.Context, source, target string) error {
	sourceDB, err := sql.Open("sqlite3", sqliteSnapshotDSN(source,
		sqliteParam{"mode", "rw"},
		sqliteParam{"_busy_timeout", "5000"},
	))
	if err != nil {
		return fmt.Errorf("open sqlite snapshot source copy: %w", err)
	}
	defer func() { _ = sourceDB.Close() }()
	sourceDB.SetMaxOpenConns(1)
	sourceDB.SetMaxIdleConns(1)
	if err := checkpointSQLite(ctx, sourceDB); err != nil {
		return err
	}
	if err := checkSQLiteIntegrity(ctx, sourceDB); err != nil {
		return err
	}

	targetDB, err := sql.Open("sqlite3", sqliteSnapshotDSN(target,
		sqliteParam{"mode", "rw"},
		sqliteParam{"_busy_timeout", "5000"},
	))
	if err != nil {
		return fmt.Errorf("open sqlite snapshot target: %w", err)
	}
	defer func() { _ = targetDB.Close() }()
	targetDB.SetMaxOpenConns(1)
	targetDB.SetMaxIdleConns(1)
	if err := backupSQLiteDatabase(ctx, targetDB, sourceDB); err != nil {
		return fmt.Errorf("backup sqlite snapshot: %w", err)
	}
	if err := useRollbackJournal(ctx, targetDB); err != nil {
		return err
	}
	if err := checkSQLiteIntegrity(ctx, targetDB); err != nil {
		return err
	}
	return nil
}

func prepareOwnedSQLiteFamily(ctx context.Context, source string) error {
	sourceDB, err := sql.Open("sqlite3", sqliteSnapshotDSN(source,
		sqliteParam{"mode", "ro"},
		sqliteParam{"_query_only", "1"},
		sqliteParam{"_busy_timeout", "5000"},
	))
	if err != nil {
		return fmt.Errorf("open sqlite snapshot source copy: %w", err)
	}
	defer func() { _ = sourceDB.Close() }()
	sourceDB.SetMaxOpenConns(1)
	sourceDB.SetMaxIdleConns(1)
	if err := checkSQLiteIntegrity(ctx, sourceDB); err != nil {
		return err
	}

	probe, err := os.CreateTemp(filepath.Dir(source), "."+filepath.Base(source)+".backup-")
	if err != nil {
		return fmt.Errorf("create sqlite snapshot probe: %w", err)
	}
	probePath := probe.Name()
	if err := probe.Chmod(0o600); err != nil {
		_ = probe.Close()
		_ = removeSQLiteSnapshotFiles(probePath)
		return fmt.Errorf("chmod sqlite snapshot probe: %w", err)
	}
	if err := probe.Close(); err != nil {
		_ = removeSQLiteSnapshotFiles(probePath)
		return fmt.Errorf("close sqlite snapshot probe: %w", err)
	}
	defer func() { _ = removeSQLiteSnapshotFiles(probePath) }()

	targetDB, err := sql.Open("sqlite3", sqliteSnapshotDSN(probePath,
		sqliteParam{"mode", "rw"},
		sqliteParam{"_busy_timeout", "5000"},
	))
	if err != nil {
		return fmt.Errorf("open sqlite snapshot probe: %w", err)
	}
	defer func() { _ = targetDB.Close() }()
	targetDB.SetMaxOpenConns(1)
	targetDB.SetMaxIdleConns(1)
	if err := backupSQLiteDatabase(ctx, targetDB, sourceDB); err != nil {
		return fmt.Errorf("backup sqlite snapshot probe: %w", err)
	}
	if err := useRollbackJournal(ctx, targetDB); err != nil {
		return err
	}
	if err := checkSQLiteIntegrity(ctx, targetDB); err != nil {
		return err
	}
	return nil
}

func commitSQLiteFamily(ctx context.Context, source, workPath, target string, suffixes []string, maxBytes int64) (SQLiteSnapshot, error) {
	if err := removeSQLiteSnapshotFiles(target); err != nil {
		return SQLiteSnapshot{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = removeSQLiteSnapshotFiles(target)
		}
	}()
	result := SQLiteSnapshot{SourcePath: source, Path: target}
	for _, suffix := range suffixes {
		size, copied, err := copySQLiteFile(ctx, workPath+suffix, target+suffix, maxBytes, true)
		if err != nil {
			return SQLiteSnapshot{}, err
		}
		if copied {
			result.Files = append(result.Files, target+suffix)
			result.SizeBytes += size
		}
	}
	committed = true
	return result, nil
}

func checkpointSQLite(ctx context.Context, db *sql.DB) error {
	var busy, logFrames, checkpointed int
	if err := db.QueryRowContext(ctx, "pragma wal_checkpoint(TRUNCATE)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("checkpoint sqlite snapshot source copy: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint sqlite snapshot source copy: busy")
	}
	return nil
}

func backupSQLiteDatabase(ctx context.Context, targetDB, sourceDB *sql.DB) error {
	sourceConn, err := sourceDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = sourceConn.Close() }()
	targetConn, err := targetDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = targetConn.Close() }()
	return targetConn.Raw(func(target any) error {
		targetSQLite, ok := target.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("sqlite target connection has type %T", target)
		}
		return sourceConn.Raw(func(source any) error {
			sourceSQLite, ok := source.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("sqlite source connection has type %T", source)
			}
			backup, err := targetSQLite.Backup("main", sourceSQLite, "main")
			if err != nil {
				return err
			}
			closed := false
			defer func() {
				if !closed {
					_ = backup.Close()
				}
			}()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				remaining := backup.Remaining()
				done, err := backup.Step(1024)
				if err != nil {
					return err
				}
				if done {
					closed = true
					return backup.Close()
				}
				if backup.Remaining() == remaining {
					if err := waitForBackupProgress(ctx); err != nil {
						return err
					}
				}
			}
		})
	})
}

func waitForBackupProgress(ctx context.Context) error {
	timer := time.NewTimer(10 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func useRollbackJournal(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "pragma journal_mode=DELETE").Scan(&mode); err != nil {
		return fmt.Errorf("set sqlite snapshot journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "delete") {
		return fmt.Errorf("set sqlite snapshot journal mode: got %s", mode)
	}
	return nil
}

func checkSQLiteIntegrity(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "pragma integrity_check")
	if err != nil {
		return fmt.Errorf("check sqlite snapshot integrity: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var failures []string
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("read sqlite snapshot integrity check: %w", err)
		}
		if result != "ok" {
			failures = append(failures, result)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read sqlite snapshot integrity check: %w", err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("sqlite snapshot integrity_check failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

type sqliteParam struct {
	key   string
	value string
}

func sqliteSnapshotDSN(path string, params ...sqliteParam) string {
	if runtime.GOOS == "windows" {
		path = filepath.ToSlash(path)
		if filepath.VolumeName(path) != "" && !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
	}
	u := url.URL{Scheme: "file", Path: path}
	query := url.Values{}
	for _, param := range params {
		query.Set(param.key, param.value)
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func syncFile(path string) error {
	file, err := os.Open(path) // #nosec G304 -- caller owns the just-created snapshot path.
	if err != nil {
		return fmt.Errorf("open sqlite snapshot for sync: %w", err)
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync sqlite snapshot: %w", err)
	}
	return nil
}

func removeSQLiteSnapshotFiles(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return removeSQLiteSidecars(path)
}

func removeSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
