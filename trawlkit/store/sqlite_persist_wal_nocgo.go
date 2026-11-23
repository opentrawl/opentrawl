//go:build !cgo

package store

import (
	"database/sql"

	sqlite3 "github.com/mattn/go-sqlite3"
)

func init() {
	sql.Register(sqlitePersistWALDriverName, &sqlite3.SQLiteDriver{})
}
