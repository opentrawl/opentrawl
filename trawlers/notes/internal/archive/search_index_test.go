package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "notes.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `
insert into notes(note_id, title, folder, created_at, modified_at, last_seen_at)
values ('note-1', 'Synthetic launch', 'Fixtures', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
insert into note_versions(
  note_id, zdata_sha256, zdata, zdata_bytes, text, text_status,
  source_modified_at, first_observed_at, latest_observed_at
) values (
  'note-1', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa', x'00', 1,
  'meet at 鹤鸣茶社 for launch planning', 'decoded',
  '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'
);
delete from schema_migrations;
insert into schema_migrations(version) values (1);
drop table notes_fts;
create virtual table notes_fts using fts5(note_id unindexed, zdata_sha256 unindexed, title, body);
insert into notes_fts(note_id, zdata_sha256, title, body)
select v.note_id, v.zdata_sha256, n.title, v.text from note_versions v join notes n on n.note_id = v.note_id;
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
	if version != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, SchemaVersion)
	}
	assertNotesSearch(t, st, "茶社")
	assertNotesSearch(t, st, "鸣茶社")
	assertNotesSearch(t, st, "鹤鸣茶社")
	assertNotesSearch(t, st, "launch")
}

func assertNotesSearch(t *testing.T, st *Store, query string) {
	t.Helper()
	results, total, err := st.Search(context.Background(), query, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 {
		t.Fatalf("query %q returned total=%d rows=%d, want 1", query, total, len(results))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d ref=%s title=%q", query, total, results[0].Ref, results[0].Title)
	}
}
