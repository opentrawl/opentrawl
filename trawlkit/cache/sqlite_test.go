package cache

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type sqliteSourceState map[string]sqliteFileState

type sqliteFileState struct {
	mode    os.FileMode
	size    int64
	modTime time.Time
	sum     [32]byte
}

func TestSnapshotSQLiteLeavesQuiescentWALSourceByteIdentical(t *testing.T) {
	source := quiescentWALSource(t)
	before := readSQLiteSourceState(t, filepath.Dir(source))

	destination := filepath.Join(t.TempDir(), "snapshot")
	snap, err := SnapshotSQLite(context.Background(), SQLiteSnapshotOptions{
		SourcePath:     source,
		DestinationDir: destination,
		Name:           "archive.sqlite",
	})
	if err != nil {
		t.Fatal(err)
	}
	after := readSQLiteSourceState(t, filepath.Dir(source))
	if err := diffSQLiteSourceState(before, after); err != nil {
		t.Fatal(err)
	}
	assertNoSQLiteSnapshotSidecars(t, source)
	assertNoSQLiteSnapshotSidecars(t, snap.Path)
	if got := readThingCount(t, snap.Path); got != 1 {
		t.Fatalf("snapshot thing count = %d, want 1", got)
	}
	if snap.Path != filepath.Join(destination, "archive.sqlite") || len(snap.Files) != 1 || snap.SizeBytes <= 0 {
		t.Fatalf("snapshot = %+v", snap)
	}
	cutoff := time.Now().Add(-time.Hour)
	if !SQLiteModifiedAfter(source, cutoff) {
		t.Fatal("sqlite source should be newer than cutoff")
	}
	t.Log("snapshot_source_unchanged=true wal_exists=false shm_exists=false")
}

func TestSQLiteSourceStateCheckDetectsModeROSidecars(t *testing.T) {
	source := quiescentWALSource(t)
	before := readSQLiteSourceState(t, filepath.Dir(source))

	db, err := sql.Open("sqlite3", sqliteSnapshotDSN(source,
		sqliteParam{"mode", "ro"},
		sqliteParam{"_query_only", "1"},
		sqliteParam{"_busy_timeout", "5000"},
	))
	if err != nil {
		t.Fatal(err)
	}
	var tables int
	if err := db.QueryRow("select count(*) from sqlite_master").Scan(&tables); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	after := readSQLiteSourceState(t, filepath.Dir(source))
	if err := diffSQLiteSourceState(before, after); err == nil {
		t.Fatal("source state check did not detect mode=ro sidecar creation")
	} else {
		t.Logf("positive_control_detected=true err=%v", err)
	}
}

func TestSnapshotSQLITERetriesTornCopy(t *testing.T) {
	source := quiescentWALSource(t)
	attempts := 0
	snap, err := snapshotSQLite(context.Background(), SQLiteSnapshotOptions{
		SourcePath:     source,
		DestinationDir: filepath.Join(t.TempDir(), "snapshot"),
		Name:           "archive.sqlite",
	}, func(attempt int, path string) {
		attempts = attempt
		if attempt != 1 {
			return
		}
		file, err := os.OpenFile(path, os.O_WRONLY, 0) // #nosec G304 -- test owns the copied database.
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteAt([]byte("not a sqlite db"), 0); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := readThingCount(t, snap.Path); got != 1 {
		t.Fatalf("snapshot thing count = %d, want 1", got)
	}
}

func TestSnapshotSQLITERetriesSourceMutationAndAcceptsFinalValidCopy(t *testing.T) {
	source := paddedQuiescentWALSource(t, 512*1024)
	withSQLiteCopyChunkBytes(t, 1)

	destination := filepath.Join(t.TempDir(), "snapshot")
	watchCtx, stopWatching := context.WithCancel(context.Background())
	touches := touchSQLiteSourceDuringCopies(t, watchCtx, destination, filepath.Base(source), source, sqliteSnapshotAttempts)
	attempts := 0
	snap, err := snapshotSQLite(context.Background(), SQLiteSnapshotOptions{
		SourcePath:     source,
		DestinationDir: destination,
		Name:           filepath.Base(source),
	}, func(attempt int, path string) {
		attempts = attempt
	})
	stopWatching()
	touched := <-touches
	if err != nil {
		t.Fatal(err)
	}
	if touched.err != nil {
		t.Fatal(touched.err)
	}
	if touched.count != sqliteSnapshotAttempts {
		t.Fatalf("source touches = %d, want %d", touched.count, sqliteSnapshotAttempts)
	}
	if attempts != sqliteSnapshotAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, sqliteSnapshotAttempts)
	}
	if got := readThingCount(t, snap.Path); got != 1 {
		t.Fatalf("snapshot thing count = %d, want 1", got)
	}
}

func TestSnapshotSQLiteHonorsContextDeadline(t *testing.T) {
	source := quiescentWALSource(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	_, err := SnapshotSQLite(ctx, SQLiteSnapshotOptions{
		SourcePath:     source,
		DestinationDir: filepath.Join(t.TempDir(), "snapshot"),
		Name:           "archive.sqlite",
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
}

func TestCopySQLiteFileHonorsContextDuringCopy(t *testing.T) {
	withSQLiteCopyChunkBytes(t, 1)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.sqlite")
	target := filepath.Join(dir, "target.sqlite")
	writeLargeFixture(t, source, 8*1024*1024)

	ctx, cancel := context.WithCancel(context.Background())
	cancelled := cancelWhenCopyStarts(t, ctx, dir, filepath.Base(target), cancel)
	start := time.Now()
	_, _, err := copySQLiteFile(ctx, source, target, 0, true)
	elapsed := time.Since(start)
	cancel()
	<-cancelled

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
	if elapsed > time.Second {
		t.Fatalf("copy returned after %s, want prompt cancellation", elapsed)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target stat err = %v, want not exist", err)
	}
}

func quiescentWALSource(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.sqlite")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma journal_mode=WAL`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma wal_autocheckpoint=0`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table things(id text primary key, value text not null); insert into things(id, value) values('one', 'synthetic');`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma wal_checkpoint(TRUNCATE)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	removeSQLiteSnapshotSidecars(t, path)
	assertNoSQLiteSnapshotSidecars(t, path)
	return path
}

func paddedQuiescentWALSource(t *testing.T, paddingBytes int) string {
	t.Helper()
	path := quiescentWALSource(t)
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma journal_mode=WAL`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma wal_autocheckpoint=0`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`create table padding(data blob not null)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into padding(data) values(zeroblob(?))`, paddingBytes); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`pragma wal_checkpoint(TRUNCATE)`); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	removeSQLiteSnapshotSidecars(t, path)
	assertNoSQLiteSnapshotSidecars(t, path)
	return path
}

func readThingCount(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite3", sqliteSnapshotDSN(path,
		sqliteParam{"mode", "ro"},
		sqliteParam{"_query_only", "1"},
		sqliteParam{"_busy_timeout", "5000"},
	))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRow("select count(*) from things").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func readSQLiteSourceState(t *testing.T, dir string) sqliteSourceState {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := sqliteSourceState{}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected directory in sqlite source: %s", entry.Name())
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path) // #nosec G304 -- test reads its own temp fixture.
		if err != nil {
			t.Fatal(err)
		}
		state[entry.Name()] = sqliteFileState{
			mode:    info.Mode().Perm(),
			size:    info.Size(),
			modTime: info.ModTime(),
			sum:     sha256.Sum256(data),
		}
	}
	return state
}

func diffSQLiteSourceState(before, after sqliteSourceState) error {
	for name, want := range before {
		got, ok := after[name]
		if !ok {
			return fmt.Errorf("missing source file %s", name)
		}
		if got.mode != want.mode || got.size != want.size || !got.modTime.Equal(want.modTime) || got.sum != want.sum {
			return fmt.Errorf("source file changed: %s", name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			return fmt.Errorf("new source file: %s", name)
		}
	}
	return nil
}

func assertNoSQLiteSnapshotSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sidecar %s stat err = %v, want not exist", path+suffix, err)
		}
	}
}

func withSQLiteCopyChunkBytes(t *testing.T, chunkBytes int64) {
	t.Helper()
	old := sqliteCopyChunkBytes
	sqliteCopyChunkBytes = chunkBytes
	t.Cleanup(func() {
		sqliteCopyChunkBytes = old
	})
}

type sqliteSourceTouchResult struct {
	count int
	err   error
}

func touchSQLiteSourceDuringCopies(t *testing.T, ctx context.Context, destination, name, source string, want int) <-chan sqliteSourceTouchResult {
	t.Helper()
	result := make(chan sqliteSourceTouchResult, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()
		seen := map[string]bool{}
		count := 0
		for count < want {
			select {
			case <-ctx.Done():
				result <- sqliteSourceTouchResult{count: count}
				return
			case <-ticker.C:
				matches, err := filepath.Glob(filepath.Join(destination, "."+name+".work-*", "."+name+".tmp-*"))
				if err != nil {
					result <- sqliteSourceTouchResult{count: count, err: err}
					return
				}
				for _, path := range matches {
					if seen[path] {
						continue
					}
					info, err := os.Stat(path)
					if err != nil || info.Size() == 0 {
						continue
					}
					mtime := time.Now().Add(time.Duration(count+1) * time.Second)
					if err := os.Chtimes(source, mtime, mtime); err != nil {
						result <- sqliteSourceTouchResult{count: count, err: err}
						return
					}
					seen[path] = true
					count++
					break
				}
			}
		}
		result <- sqliteSourceTouchResult{count: count}
	}()
	return result
}

func writeLargeFixture(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.Create(path) // #nosec G304 -- test owns the temp fixture.
	if err != nil {
		t.Fatal(err)
	}
	block := make([]byte, 64*1024)
	var written int64
	for written < size {
		n := int64(len(block))
		if remaining := size - written; remaining < n {
			n = remaining
		}
		if _, err := file.Write(block[:n]); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		written += n
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func cancelWhenCopyStarts(t *testing.T, ctx context.Context, dir, targetName string, cancel context.CancelFunc) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				matches, err := filepath.Glob(filepath.Join(dir, "."+targetName+".tmp-*"))
				if err != nil {
					cancel()
					return
				}
				for _, path := range matches {
					info, err := os.Stat(path)
					if err == nil && info.Size() > 0 {
						cancel()
						return
					}
				}
			}
		}
	}()
	return done
}

func removeSQLiteSnapshotSidecars(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
}
