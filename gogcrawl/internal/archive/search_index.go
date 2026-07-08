package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ckstore "github.com/openclaw/crawlkit/store"
)

func ensureSearchIndex(ctx context.Context, st *ckstore.Store) error {
	current, err := st.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("read search index version: %w", err)
	}
	if current >= schemaVersion {
		return nil
	}
	return st.WithTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`drop table if exists messages_fts`,
			messagesFTSSchema,
			`insert into messages_fts(id, subject, body)
			 select id, coalesce(subject, ''), coalesce(body, '') from messages`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("rebuild messages_fts: %w", err)
			}
		}
		return nil
	})
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
