//go:build !cgo

package cache

import (
	"context"
	"database/sql"
	"errors"
)

func backupSQLiteDatabase(context.Context, *sql.DB, *sql.DB) error {
	return errors.New("sqlite online backup requires cgo")
}
