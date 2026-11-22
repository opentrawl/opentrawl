package store

import (
	"database/sql"
	"fmt"
	"time"
)

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func unix(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UTC().Unix()
}

func fromUnix(v int64) time.Time {
	if !validUnixTimestamp(v) {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
}

func validUnixTimestamp(v int64) bool {
	return v > 0 && v <= maxJSONUnixSecond
}

func validUnixPredicate(column string) string {
	return fmt.Sprintf("%s > 0 and %s <= %d", column, column, maxJSONUnixSecond)
}

func invalidUnixPredicate(column string) string {
	return fmt.Sprintf("(%s <= 0 or %s > %d)", column, column, maxJSONUnixSecond)
}

func nullString(v string) sql.NullString {
	return sql.NullString{String: v, Valid: true}
}

func nullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

func rollback(tx *sql.Tx) {
	_ = tx.Rollback()
}
