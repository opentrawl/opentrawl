package archive

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/store"
)

func TestFTSQueryJoinsWithOR(t *testing.T) {
	t.Parallel()
	if got := ftsQuery("castellbell house street"); got != `"castellbell" OR "house" OR "street"` {
		t.Fatalf("ftsQuery = %q", got)
	}
}

// "grill" must match a card that says "grilled" (porter stemming), and a
// multi-word query must not require every word to be present.
func TestSearchStemsAndRanksInsteadOfRequiringEveryTerm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var assetID string
		if err := tx.QueryRowContext(ctx, `select id from asset limit 1`).Scan(&assetID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('obs-grill', ?, '', 'whole chicken grilled charred garden')`, assetID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	stemmed, err := Search(ctx, paths, SearchOptions{Query: "grill", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(stemmed.Results) != 1 {
		t.Fatalf("stemmed search results = %#v", stemmed.Results)
	}
	partial, err := Search(ctx, paths, SearchOptions{Query: "grilled restaurant rooftop", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(partial.Results) != 1 {
		t.Fatalf("partial-term search results = %#v", partial.Results)
	}
}

// An archive built with the old default tokenizer is rebuilt in place from
// the source tables the first time the write path opens it.
func TestEnsureSearchIndexRebuildsOldTokenizer(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Regress the archive to the pre-porter shape, with legacy rows.
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range []string{
			`drop table asset_fts`,
			`drop table observation_fts`,
			`create virtual table asset_fts using fts5(id unindexed, title, body)`,
			`create virtual table observation_fts using fts5(id unindexed, asset_id unindexed, title, body)`,
			`insert into metadata_observation(id, asset_id, observation_type, label, source, classifier_id, evidence_id)
			 values ('meta-1', (select id from asset limit 1), 'time_of_day', 'daytime photo', 'fixture', '', '')`,
		} {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := ensureSearchIndex(ctx, db, classifyLogger{}); err != nil {
		t.Fatal(err)
	}

	var ddl string
	if err := db.DB().QueryRowContext(ctx, `select sql from sqlite_master where name = 'asset_fts'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ddl, "porter") {
		t.Fatalf("asset_fts not rebuilt with porter: %s", ddl)
	}
	var assetRows, metaRows int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from asset_fts`).Scan(&assetRows); err != nil {
		t.Fatal(err)
	}
	if err := db.DB().QueryRowContext(ctx, `select count(*) from observation_fts where id = 'meta-1'`).Scan(&metaRows); err != nil {
		t.Fatal(err)
	}
	if assetRows == 0 || metaRows != 1 {
		t.Fatalf("rebuild rows: asset=%d meta=%d", assetRows, metaRows)
	}

	// Second call is a no-op (porter already present).
	if err := ensureSearchIndex(ctx, db, classifyLogger{}); err != nil {
		t.Fatal(err)
	}
}
