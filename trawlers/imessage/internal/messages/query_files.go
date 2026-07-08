package messages

import (
	"context"
	"database/sql"
	_ "embed"
	"strings"
)

//go:embed queries/common/table_exists.sql
var tableExistsSQL string

//go:embed queries/sync/handles.sql
var extractHandlesSQL string

//go:embed queries/sync/chats.sql
var extractChatsSQL string

//go:embed queries/sync/participants.sql
var extractParticipantsSQL string

//go:embed queries/sync/chat_messages.sql
var extractChatMessagesSQL string

//go:embed queries/sync/messages.sql
var extractMessagesSQL string

//go:embed queries/status/handle_ids.sql
var handleIDsSQL string

//go:embed queries/contacts/phone_handles.sql
var phoneHandleRowsSQL string

func extractMessagesQuery(ctx context.Context, db *sql.DB) (string, error) {
	hasAccount, err := tableHasColumn(ctx, db, "message", "account")
	if err != nil {
		return "", err
	}
	accountExpr := "''"
	if hasAccount {
		accountExpr = "coalesce(m.account, '')"
	}
	return strings.Replace(extractMessagesSQL, "{{ACCOUNT_EXPR}}", accountExpr, 1), nil
}

func tableHasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `pragma table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
