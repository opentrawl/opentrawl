package archive

import (
	"context"
	"path/filepath"
	"testing"
)

func TestApplySyncRollsBackObservedUpdatesWhenBatchFails(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	if _, err := st.ApplySync(ctx, SyncBatch{
		Notes:               []Note{{ID: "note-alpha", Title: "Alpha"}},
		LastSeenAt:          "2026-01-01T00:00:00Z",
		RefreshNoteMetadata: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `
create trigger fail_sync_state
before insert on sync_state
begin
  select raise(abort, 'synthetic interruption');
end`); err != nil {
		t.Fatal(err)
	}

	_, err = st.ApplySync(ctx, SyncBatch{
		Notes:               []Note{{ID: "note-alpha", Title: "Changed before interruption"}},
		SyncState:           map[string]string{"last_sync_at": "2026-01-02T00:00:00Z"},
		LastSeenAt:          "2026-01-02T00:00:00Z",
		RefreshNoteMetadata: true,
	})
	if err == nil {
		t.Fatal("interrupted sync succeeded")
	}
	note, err := st.ResolveNote(ctx, "note-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if note.Title != "Alpha" || note.LastSeenAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("note after rollback = %#v, want original metadata", note)
	}
}
