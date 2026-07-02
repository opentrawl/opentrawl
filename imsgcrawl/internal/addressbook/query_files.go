package addressbook

import (
	"context"
	"database/sql"
	_ "embed"
	"strings"

	"github.com/openclaw/crawlkit/store"
)

//go:embed queries/email_addresses.sql
var emailAddressesSQL string

//go:embed queries/phone_numbers.sql
var phoneNumbersSQL string

//go:embed queries/table_exists.sql
var tableExistsSQL string

var meCardColumns = []string{"ZISME", "ZISMYCARD", "ZISMECARD", "ZME"}

func contactHandleQuery(ctx context.Context, db *sql.DB, query string) (string, error) {
	expr, err := meCardExpr(ctx, db)
	if err != nil {
		return "", err
	}
	return strings.Replace(query, "{{IS_ME_EXPR}}", expr, 1), nil
}

func meCardExpr(ctx context.Context, db *sql.DB) (string, error) {
	for _, column := range meCardColumns {
		ok, err := tableHasColumn(ctx, db, "ZABCDRECORD", column)
		if err != nil {
			return "", err
		}
		if ok {
			return "coalesce(r." + store.QuoteIdent(column) + ", 0)", nil
		}
	}
	return "0", nil
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
