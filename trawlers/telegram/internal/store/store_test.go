package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T, path string) *Store {
	t.Helper()
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return st
}

func TestMessagesToleratesNullableOptionalFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := openTestStore(t, filepath.Join(t.TempDir(), "nullable-messages.db"))
	if _, err := st.db.ExecContext(ctx, `insert into messages(source_pk,chat_jid,msg_id,ts,from_me,raw_type,starred) values(?,?,?,?,?,?,?)`, 1, "42", "1", unix(time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)), 0, 0, 0); err != nil {
		t.Fatal(err)
	}

	messages, err := st.Messages(ctx, MessageFilter{ChatJID: "42", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].EditTime.IsZero() == false {
		t.Fatalf("edit time = %v, want zero", messages[0].EditTime)
	}
	if messages[0].ChatName != "" || messages[0].TopicID != "" || messages[0].ForwardJSON != "" {
		t.Fatalf("nullable fields not normalized: %#v", messages[0])
	}
}
