package archive

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/openclaw/crawlkit/store"
)

func ensureSearchIndex(ctx context.Context, st *store.Store) error {
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
			`insert into messages_fts(source_rowid, text)
			 select source_rowid, coalesce(text, '') from messages`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("rebuild messages_fts: %w", err)
			}
		}
		return nil
	})
}
