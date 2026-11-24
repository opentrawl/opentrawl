package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strconv"
)

// FingerprintQueryRows hashes every value the queries return, in order. An
// index cursor carries it so that a change to rows the cursor does not count,
// such as a contact name, still triggers a full re-export.
func FingerprintQueryRows(ctx context.Context, db *sql.DB, queries ...string) (string, error) {
	hash := sha256.New()
	for _, query := range queries {
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			return "", err
		}
		columns, err := rows.Columns()
		if err != nil {
			_ = rows.Close()
			return "", err
		}
		values := make([]sql.NullString, len(columns))
		destinations := make([]any, len(columns))
		for i := range values {
			destinations[i] = &values[i]
		}
		for rows.Next() {
			if err := rows.Scan(destinations...); err != nil {
				_ = rows.Close()
				return "", err
			}
			for _, value := range values {
				valueLength := strconv.Itoa(len(value.String))
				if !value.Valid {
					valueLength = "null"
				}
				hash.Write([]byte(valueLength + ":" + value.String))
			}
		}
		if err := rows.Close(); err != nil {
			return "", err
		}
		if err := rows.Err(); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:8]), nil
}
