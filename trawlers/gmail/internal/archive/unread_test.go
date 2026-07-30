package archive

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"testing"
	"time"
)

// TestUnreadStateIsQueryableAndFlipsOnReingest protects a load-bearing case:
// UNREAD must be a queryable fact (Status, OpenMessage, Search all
// agree), and a message that drops the UNREAD label on a later ingest must
// flip to read — not stay stuck at whatever it was first archived as.
func TestUnreadStateIsQueryableAndFlipsOnReingest(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gmail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)

	if _, err := st.InsertMessages(ctx, []Message{
		{ID: "u1", ThreadID: "t1", Time: now, FromName: "Alice", FromAddress: "alice@example.com", Subject: "Unread one", Body: "Body needle one.", Labels: []string{"INBOX", "UNREAD"}},
		{ID: "r1", ThreadID: "t2", Time: now.Add(-time.Minute), FromName: "Bob", FromAddress: "bob@example.com", Subject: "Already read", Body: "Body needle two.", Labels: []string{"INBOX"}},
	}); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Unread != 1 {
		t.Fatalf("status.Unread = %d, want 1", status.Unread)
	}

	openUnread, err := st.OpenMessage(ctx, RefPrefix+"u1")
	if err != nil {
		t.Fatal(err)
	}
	if !openUnread.Unread {
		t.Fatalf("u1 open.Unread = false, want true")
	}
	openRead, err := st.OpenMessage(ctx, RefPrefix+"r1")
	if err != nil {
		t.Fatal(err)
	}
	if openRead.Unread {
		t.Fatalf("r1 open.Unread = true, want false")
	}

	search, err := st.Search(ctx, SearchOptions{Query: "needle", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	unreadByRef := map[string]bool{}
	for _, hit := range search.Results {
		unreadByRef[hit.Ref] = hit.Unread
	}
	if !unreadByRef[RefPrefix+"u1"] || unreadByRef[RefPrefix+"r1"] {
		t.Fatalf("search unread flags = %#v", unreadByRef)
	}

	// The read transition: u1 loses the UNREAD label upstream. A re-ingest
	// (same message ID, fresh label set) must flip the archive's flag too —
	// this is the transition most likely to be silently skipped.
	if _, err := st.InsertMessages(ctx, []Message{
		{ID: "u1", ThreadID: "t1", Time: now, FromName: "Alice", FromAddress: "alice@example.com", Subject: "Unread one", Body: "Body needle one.", Labels: []string{"INBOX"}},
	}); err != nil {
		t.Fatal(err)
	}
	afterRead, err := st.OpenMessage(ctx, RefPrefix+"u1")
	if err != nil {
		t.Fatal(err)
	}
	if afterRead.Unread {
		t.Fatalf("u1 stayed unread after re-ingest dropped the UNREAD label")
	}
	statusAfter, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if statusAfter.Unread != 0 {
		t.Fatalf("status.Unread after transition = %d, want 0", statusAfter.Unread)
	}

	// And the reverse: a read message can be marked unread again.
	if _, err := st.InsertMessages(ctx, []Message{
		{ID: "r1", ThreadID: "t2", Time: now.Add(-time.Minute), FromName: "Bob", FromAddress: "bob@example.com", Subject: "Already read", Body: "Body needle two.", Labels: []string{"INBOX", "UNREAD"}},
	}); err != nil {
		t.Fatal(err)
	}
	afterUnread, err := st.OpenMessage(ctx, RefPrefix+"r1")
	if err != nil {
		t.Fatal(err)
	}
	if !afterUnread.Unread {
		t.Fatalf("r1 did not flip back to unread after re-ingest added the UNREAD label")
	}
}

// TestIngestBackupMessageShardSetsUnreadFromLabelIDs covers the real ingest
// path (a raw backup shard row with Gmail's labelIds), not just the
// InsertMessages API a test can call directly.
func TestIngestBackupMessageShardSetsUnreadFromLabelIDs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gmail.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	raw := "From: Alice Example <alice@example.com>\r\nTo: Bob Example <bob@example.com>\r\nSubject: Shard unread\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHello.\r\n"
	row := `{"id":"munread","threadId":"tunread","historyId":"h1","internalDate":1783000000123,"labelIds":["INBOX","UNREAD"],"sizeEstimate":100,"raw":"` +
		base64.RawURLEncoding.EncodeToString([]byte(raw)) + "\"}\n"
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	if _, err := st.IngestBackupShard(ctx, shard, []byte(row)); err != nil {
		t.Fatal(err)
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"munread")
	if err != nil {
		t.Fatal(err)
	}
	if !open.Unread {
		t.Fatalf("message with labelIds containing UNREAD archived as read")
	}
}
