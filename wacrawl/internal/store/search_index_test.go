package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSearchIndexRebuildsTrigramCJK(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "whatsapp.db")
	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `
insert into messages(
  source_pk, chat_jid, chat_name, msg_id, sender_jid, sender_name, ts, from_me,
  text, raw_type, message_type, media_type, media_title, media_path, media_url,
  media_size, starred
) values (
  1, 'chat-1', 'Synthetic chat', 'msg-1', 'alice@example.com', 'Alice Example', 1, 0,
  'meet at 鹤鸣茶社 for launch planning', 1, '', '', '', '', '', 0, 0
);
drop table messages_fts;
create virtual table messages_fts using fts5(text, chat, sender, media);
insert into messages_fts(rowid, text, chat, sender, media)
select rowid, text, chat_name, sender_name, media_type from messages;
pragma user_version = 1;
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
	version, err := userVersion(ctx, st.db)
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	assertWhatsAppSearch(t, st, "茶社")
	assertWhatsAppSearch(t, st, "鸣茶社")
	assertWhatsAppSearch(t, st, "鹤鸣茶社")
	assertWhatsAppSearch(t, st, "launch")
}

func assertWhatsAppSearch(t *testing.T, st *Store, query string) {
	t.Helper()
	results, err := st.Search(context.Background(), MessageFilter{Query: query, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	total, err := st.SearchCount(context.Background(), MessageFilter{Query: query, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 {
		t.Fatalf("query %q returned total=%d rows=%d, want 1", query, total, len(results))
	}
	if query == "茶社" || query == "鸣茶社" {
		t.Logf("query=%q total=%d source_pk=%d text=%q", query, total, results[0].SourcePK, results[0].Text)
	}
}
