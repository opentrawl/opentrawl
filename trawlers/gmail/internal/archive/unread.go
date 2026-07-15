package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// unreadLabelID is the Gmail system label that marks a message unread.
const unreadLabelID = "UNREAD"

// isUnread reports whether labelIDs contains Gmail's UNREAD system label.
func isUnread(labelIDs []string) bool {
	for _, id := range labelIDs {
		if id == unreadLabelID {
			return true
		}
	}
	return false
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// ensureUnreadColumn adds messages.is_unread to archives created before this
// column existed, and backfills it from the labels_json every row already
// carries. Pre-v1: an additive column plus a one-time backfill beats a
// migration framework. It runs on every Open, Use and UseExisting
// call so both the sync path and the read paths (search, open, who, doctor)
// self-heal at the point of use — a separate repair verb would be a design
// bug (rule 16).
func ensureUnreadColumn(ctx context.Context, db *sql.DB) error {
	has, err := hasColumn(ctx, db, "messages", "is_unread")
	if err != nil {
		return fmt.Errorf("inspect messages columns: %w", err)
	}
	if !has {
		if _, err := db.ExecContext(ctx, `alter table messages add column is_unread integer not null default 0`); err != nil {
			if !isDuplicateColumnError(err) {
				return fmt.Errorf("add is_unread column: %w", err)
			}
		} else if _, err := db.ExecContext(ctx, `update messages set is_unread = 1 where labels_json like '%"UNREAD"%'`); err != nil {
			return fmt.Errorf("backfill is_unread: %w", err)
		}
	}
	// The index is created unconditionally (idempotent via IF NOT EXISTS):
	// fresh databases reach here with the column already present from the
	// ALTER above, but never got a chance to declare the index anywhere else.
	if _, err := db.ExecContext(ctx, `create index if not exists idx_messages_unread on messages(is_unread)`); err != nil {
		return fmt.Errorf("index is_unread: %w", err)
	}
	return nil
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func isDuplicateColumnError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "duplicate column name")
}
