package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ckstore "github.com/openclaw/crawlkit/store"
)

func ensureSearchIndex(ctx context.Context, db *sql.DB, current int) error {
	if current >= schemaVersion {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)
	for _, stmt := range []string{
		`drop table if exists messages_fts`,
		messagesFTSSchema,
		`insert into messages_fts(rowid, text, chat, sender, media)
		 select rowid,
		        trim(coalesce(text, '') || ' ' || coalesce(media_title, '') || ' ' || coalesce(metadata_title, '') || ' ' || coalesce(metadata_url, '')),
		        coalesce(chat_name, ''),
		        coalesce(sender_name, ''),
		        coalesce(media_type, '')
		 from messages`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rebuild messages_fts: %w", err)
		}
	}
	return tx.Commit()
}

func ftsSearchClause(tableName string, columns []string, plan ckstore.FTS5QueryPlan) (string, []any) {
	if plan.Mode == ckstore.FTS5QueryMatch {
		return tableName + " match ?", []any{plan.Match}
	}
	var parts []string
	var args []any
	for _, pattern := range plan.LikePatterns {
		var termParts []string
		for _, column := range columns {
			termParts = append(termParts, column+" like ? escape '\\'")
			args = append(args, pattern)
		}
		parts = append(parts, "("+strings.Join(termParts, " or ")+")")
	}
	return strings.Join(parts, " and "), args
}
