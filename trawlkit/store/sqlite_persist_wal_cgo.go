//go:build cgo

package store

import (
	"database/sql"

	// C SQLite via cgo. The pure-Go translation (modernc.org/sqlite) ran
	// hot paths 10-100x slower and caused three production incidents in one
	// day (unindexed-scan write stalls, 69s searches, pathological reads
	// against a large WAL). Requires -tags sqlite_fts5; the monorepo devenv
	// sets it via GOFLAGS.
	sqlite3 "github.com/mattn/go-sqlite3"
)

func init() {
	sql.Register(sqlitePersistWALDriverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			return conn.SetFileControlInt("main", sqlite3.SQLITE_FCNTL_PERSIST_WAL, 1)
		},
	})
}
