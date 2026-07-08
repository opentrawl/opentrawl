package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ckstore "github.com/openclaw/crawlkit/store"
)

func rebuildSearchIndex(ctx context.Context, tx *sql.Tx) error {
	for _, stmt := range []string{
		`drop table if exists tweets_fts`,
		tweetsFTSSchema,
		`insert into tweets_fts(rowid, text, author_handle, author_name)
		 select rowid, coalesce(text, ''), coalesce(author_handle, ''), coalesce(author_name, '') from tweets`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("rebuild tweets_fts: %w", err)
		}
	}
	return nil
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
