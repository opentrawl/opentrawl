package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "imessage.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `
insert into messages(source_rowid, guid, date, text)
values (1, 'synthetic-message-1', 1, 'meet at 鹤鸣茶社 for launch planning');
insert into messages_fts(source_rowid, text)
values (1, 'meet at 鹤鸣茶社 for launch planning');
delete from schema_migrations;
insert into schema_migrations(version) values (6);
drop table messages_fts;
create virtual table messages_fts using fts5(source_rowid unindexed, text);
insert into messages_fts(source_rowid, text)
values (1, 'meet at 鹤鸣茶社 for launch planning');
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
	var version int
	if err := st.store.DB().QueryRowContext(ctx, `select max(version) from schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	assertImessageSearch(t, st, "茶社")
	assertImessageSearch(t, st, "鸣茶社")
	assertImessageSearch(t, st, "鹤鸣茶社")
	assertImessageSearch(t, st, "launch")
}

func assertImessageSearch(t *testing.T, st *Store, query string) {
	t.Helper()
	page, err := st.SearchPage(context.Background(), query, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 {
		t.Fatalf("query %q returned total=%d rows=%d, want 1", query, page.Total, len(page.Items))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d message_id=%s text=%q", query, page.Total, page.Items[0].MessageID, page.Items[0].Text)
	}
}
