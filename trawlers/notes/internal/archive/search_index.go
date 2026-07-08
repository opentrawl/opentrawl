package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/store"
)

func ensureSearchIndex(ctx context.Context, st *store.Store) error {
	current, err := st.SchemaVersion(ctx)
	if err != nil {
		return fmt.Errorf("read search index version: %w", err)
	}
	if current >= SchemaVersion {
		return nil
	}
	return st.WithTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`drop table if exists notes_fts`,
			notesFTSSchema,
			`insert into notes_fts(note_id, zdata_sha256, title, body)
			 select v.note_id, v.zdata_sha256, coalesce(n.title, ''), coalesce(v.text, '')
			 from note_versions v
			 left join notes n on n.note_id = v.note_id
			 where v.text_status = 'decoded'`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("rebuild notes_fts: %w", err)
			}
		}
		return nil
	})
}

func ftsSearchClause(tableName string, columns []string, plan store.FTS5QueryPlan) (string, []any) {
	if plan.Mode == store.FTS5QueryMatch {
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
