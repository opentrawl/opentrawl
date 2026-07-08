package store

import (
	"context"
	"database/sql"
	"fmt"
)

func migrate(ctx context.Context, db *sql.DB) error {
	adds := map[string]map[string]string{
		"chats": {
			"folder_id": "text",
			"forum":     "integer not null default 0",
		},
		"messages": {
			"topic_id":          "text",
			"reply_to_msg_id":   "text",
			"reply_to_chat_jid": "text",
			"thread_id":         "text",
			"edit_ts":           "integer",
			"forward_json":      "text",
			"reactions_json":    "text",
			"views":             "integer not null default 0",
			"forwards":          "integer not null default 0",
			"replies_count":     "integer not null default 0",
			"pinned":            "integer not null default 0",
			"metadata_type":     "text",
			"metadata_title":    "text",
			"metadata_url":      "text",
			"metadata_json":     "text",
		},
		"contacts": {
			"peer_type":   "text",
			"avatar_path": "text",
		},
	}
	for table, defs := range adds {
		existing, err := columns(ctx, db, table)
		if err != nil {
			return err
		}
		for name, def := range defs {
			if existing[name] {
				continue
			}
			if _, err := db.ExecContext(ctx, fmt.Sprintf("alter table %s add column %s %s", table, name, def)); err != nil {
				return err
			}
		}
	}
	return nil
}

// legacySyncState reports whether the archive still carries the pre-canonical
// key/value sync_state table (no source_name column). A fresh or migrated
// archive returns false, so the canonical crawlkit state table is never
// dropped once it exists.
func legacySyncState(ctx context.Context, db *sql.DB) (bool, error) {
	cols, err := columns(ctx, db, "sync_state")
	if err != nil {
		return false, err
	}
	if len(cols) == 0 {
		return false, nil
	}
	return !cols["source_name"], nil
}

// legacySyncStateValues reads every key/value pair out of the pre-canonical
// sync_state table, so Open can carry them into the canonical crawlkit state
// table before dropping the legacy one.
func legacySyncStateValues(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `select key, value from sync_state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func columns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, "pragma table_info("+table+")")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}
