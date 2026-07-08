package archive

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/store"
	"github.com/openclaw/photoscrawl/internal/photos"
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
	defer func() { _ = db.Close() }()

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

// A card row indexed as a deduped term list is rebuilt into raw card prose,
// the index version is stamped, and later calls leave the index alone.
func TestEnsureSearchIndexRebuildsCardBodiesToRawProse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	var assetID string
	if err := db.DB().QueryRowContext(ctx, `select id from asset limit 1`).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, row := range []struct{ id, observationType, text string }{
			{"card-sum", modelObservationCardSummary, "Grilled chicken on the new grill"},
			{"card-desc", modelObservationCardDescription, "A whole chicken on a charcoal grill, grill tongs beside it."},
			{"card-ocr", modelObservationCardOCR, ""},
		} {
			if row.text == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
insert into model_observation(id, asset_id, observation_type, value_text, value_json, confidence, source, model_id, prompt_version, evidence_id)
values (?, ?, ?, ?, '{}', 1.0, 'model_multimodal', 'fixture-model', 'v1', '')`,
				row.id, assetID, row.observationType, row.text); err != nil {
				return err
			}
		}
		// Old-style FTS row: deduped normalized terms, term frequency destroyed.
		_, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body)
values ('card-sum', ?, '', 'grilled chicken new grill whole charcoal tongs beside')`, assetID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := ensureSearchIndex(ctx, db, classifyLogger{}); err != nil {
		t.Fatal(err)
	}

	var body string
	if err := db.DB().QueryRowContext(ctx, `select body from observation_fts where id = 'card-sum'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	want := "Grilled chicken on the new grill\nA whole chicken on a charcoal grill, grill tongs beside it."
	if body != want {
		t.Fatalf("card fts body = %q, want raw prose %q", body, want)
	}
	var version int
	if err := db.DB().QueryRowContext(ctx, `select max(version) from search_index_state`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != searchIndexVersion {
		t.Fatalf("search index version = %d, want %d", version, searchIndexVersion)
	}

	// A current-version index must not be rebuilt: tamper with the row and
	// prove the tampering survives a second ensure call.
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from observation_fts where id = 'card-sum'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSearchIndex(ctx, db, classifyLogger{}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := db.DB().QueryRowContext(ctx, `select count(*) from observation_fts where id = 'card-sum'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("second ensure call rebuilt a current-version index")
	}
}

// bm25 over raw prose: the asset whose card is about grilling outranks the
// asset that mentions a grill once.
func TestSearchRanksByTermFrequency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedTwoAssetLibrary(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	var oftenAssetID string
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		var ids []string
		rows, err := tx.QueryContext(ctx, `select id from asset order by local_identifier`)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) != 2 {
			t.Fatalf("expected 2 assets, got %d", len(ids))
		}
		// The high-frequency asset is the OLDER one (ids[0]). The query
		// tiebreak is `creation_date desc`, so if bm25 collapsed the newer
		// low-frequency asset (ids[1]) would win: only real term-frequency
		// ranking can float the older asset to the top.
		oftenAssetID = ids[0]
		for _, row := range []struct{ id, assetID, body string }{
			{"card-often", ids[0], "Grilling on the grill: grilled chicken over charcoal, grill tongs turning grilled corn."},
			{"card-once", ids[1], "A kitchen counter with a pan, a grill pan hangs on the wall among many other utensils and appliances."},
		} {
			if _, err := tx.ExecContext(ctx, `
insert into observation_fts(id, asset_id, title, body) values (?, ?, '', ?)`,
				row.id, row.assetID, row.body); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := Search(ctx, paths, SearchOptions{Query: "grill", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 {
		t.Fatalf("results = %#v", result.Results)
	}
	if result.Results[0].ID != oftenAssetID {
		t.Fatalf("frequency ranking: first result = %s, want the grilling asset %s", result.Results[0].ID, oftenAssetID)
	}
}

func seedTwoAssetLibrary(t *testing.T, paths Paths) {
	t.Helper()
	ctx := context.Background()
	libraryPath := filepath.Join(t.TempDir(), "Fixture Photos Library.photoslibrary")
	if err := mkdirLibrary(libraryPath); err != nil {
		t.Fatal(err)
	}
	provider := fakeProvider{snapshot: photos.LibrarySnapshot{
		Provider:            "fake",
		PhotosVersion:       "fixture",
		AuthorizationStatus: "authorized",
		Assets: []photos.Asset{
			{LocalIdentifier: "fixture-asset-a", MediaType: "image", MediaSubtypes: "0", CreationDate: "2026-05-27T12:00:00Z", Width: 100, Height: 80},
			{LocalIdentifier: "fixture-asset-b", MediaType: "image", MediaSubtypes: "0", CreationDate: "2026-05-27T13:00:00Z", Width: 100, Height: 80},
		},
	}}
	if _, err := Sync(ctx, paths, SyncOptions{
		LibraryPath: libraryPath,
		Provider:    provider,
		Now:         fixedClock("2026-05-28T10:00:00Z"),
	}); err != nil {
		t.Fatal(err)
	}
}

// Unselected POI candidates are selection provenance, not searchable claims:
// neither the write path nor the index rebuild may put them in FTS.
func TestSearchExcludesUnselectedPOICandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	paths := testPaths(t)
	seedSyntheticPlaceAsset(t, paths)
	db, err := store.Open(ctx, store.Options{Path: paths.Database, Schema: Schema, SchemaVersion: SchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	var assetID string
	if err := db.DB().QueryRowContext(ctx, `select id from asset limit 1`).Scan(&assetID); err != nil {
		t.Fatal(err)
	}
	// Stamp the index version so the rebuild-path check below controls when
	// the rebuild fires.
	if err := ensureSearchIndex(ctx, db, classifyLogger{}); err != nil {
		t.Fatal(err)
	}

	// Write path: candidate gets no FTS row, selected venue and address do.
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		for _, row := range []struct{ kind, text string }{
			{"poi_candidate", "Meadow Grill Taqueria"},
			{"venue", "UMI Sushi"},
			{"address", "Simeonstrasse 1 Trier"},
		} {
			if _, err := insertPlaceObservation(ctx, tx, assetID, "fixture-generation", row.kind, row.text, map[string]any{}, "fixture", "hit", "poi", 10); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var indexed int
	if err := db.DB().QueryRowContext(ctx,
		`select count(*) from observation_fts where asset_id = ?`, assetID,
	).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if indexed != 2 {
		t.Fatalf("write path indexed %d place rows, want 2 (venue+address, no candidate)", indexed)
	}

	// Rebuild path: force a rebuild and prove the candidate stays out.
	if err := db.WithTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `delete from search_index_state`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := ensureSearchIndex(ctx, db, classifyLogger{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if hits, err := Search(ctx, paths, SearchOptions{Query: "taqueria", Limit: 5}); err != nil || len(hits.Results) != 0 {
		t.Fatalf("candidate is searchable after rebuild: hits=%v err=%v", hits.Results, err)
	}
	if hits, err := Search(ctx, paths, SearchOptions{Query: "sushi", Limit: 5}); err != nil || len(hits.Results) != 1 {
		t.Fatalf("selected venue not searchable: hits=%v err=%v", hits.Results, err)
	}
	if hits, err := Search(ctx, paths, SearchOptions{Query: "simeonstrasse", Limit: 5}); err != nil || len(hits.Results) != 1 {
		t.Fatalf("address not searchable: hits=%v err=%v", hits.Results, err)
	}
}
