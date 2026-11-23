//go:build cgo

package cache

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"
)

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
