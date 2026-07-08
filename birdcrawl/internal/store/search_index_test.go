package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "twitter.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := st.db.ExecContext(ctx, `
insert into tweets(id, created_at, author_handle, author_name, text, first_source)
values ('tweet-1', ?, 'alice', 'Alice Example', 'meet at 鹤鸣茶社 for launch planning', 'archive');
drop table tweets_fts;
create virtual table tweets_fts using fts5(
	text,
	author_handle,
	author_name,
	content='tweets',
	content_rowid='rowid'
);
insert into tweets_fts(rowid, text, author_handle, author_name)
select rowid, text, author_handle, author_name from tweets;
pragma user_version = 2;
`, formatUTC(now)); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	version, err := userVersion(ctx, st.db)
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	assertTwitterSearch(t, st, "茶社")
	assertTwitterSearch(t, st, "鸣茶社")
	assertTwitterSearch(t, st, "鹤鸣茶社")
	assertTwitterSearch(t, st, "launch")
}

func assertTwitterSearch(t *testing.T, st *Store, query string) {
	t.Helper()
	results, total, err := st.Search(context.Background(), SearchFilter{Query: query, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 {
		t.Fatalf("query %q returned total=%d rows=%d, want 1", query, total, len(results))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d tweet_id=%s text=%q", query, total, results[0].ID, results[0].Text)
	}
}
