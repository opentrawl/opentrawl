package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gmail.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `
insert into messages(id, thread_id, history_id, time, time_unix, subject, body, labels_json)
values ('msg-1', 'thread-1', 'hist-1', '2026-01-01T00:00:00Z', 1767225600, 'Synthetic launch', 'meet at 鹤鸣茶社 for launch planning', '[]');
delete from schema_migrations;
insert into schema_migrations(version) values (2);
drop table messages_fts;
create virtual table messages_fts using fts5(id unindexed, subject, body);
insert into messages_fts(id, subject, body)
select id, subject, body from messages;
`); err != nil {
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
	version, err := st.store.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	assertGmailSearch(t, st, "茶社")
	assertGmailSearch(t, st, "鸣茶社")
	assertGmailSearch(t, st, "鹤鸣茶社")
	assertGmailSearch(t, st, "launch")
}

func assertGmailSearch(t *testing.T, st *Store, query string) {
	t.Helper()
	result, err := st.Search(context.Background(), SearchOptions{Query: query, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalMatches != 1 || len(result.Results) != 1 {
		t.Fatalf("query %q returned total=%d rows=%d, want 1", query, result.TotalMatches, len(result.Results))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d ref=%s snippet=%q", query, result.TotalMatches, result.Results[0].Ref, result.Results[0].Snippet)
	}
}
