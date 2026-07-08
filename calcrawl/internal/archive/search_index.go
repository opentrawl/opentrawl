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
			`drop table if exists events_fts`,
			eventsFTSSchema,
			`insert into events_fts(event_uid, summary, description, location, participants)
			 select event_uid, coalesce(summary, ''), coalesce(description, ''),
			        trim(coalesce(location_title, '') || ' ' || coalesce(location_address, '')),
			        coalesce(participants_text, '')
			 from events`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("rebuild events_fts: %w", err)
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
