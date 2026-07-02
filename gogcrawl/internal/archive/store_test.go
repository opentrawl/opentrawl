package archive

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSearchOpenStatus(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 7, 2, 14, 3, 11, 0, time.UTC)
	result, err := st.InsertMessages(ctx, []Message{
		{ID: "m1", ThreadID: "t1", Time: now, FromName: "Alice", FromAddress: "alice@example.com", Subject: "Project sync", Body: "The project sync is Friday.", Labels: []string{"INBOX"}},
		{ID: "m2", ThreadID: "t2", Time: now.Add(-time.Hour), FromName: "Bob", FromAddress: "bob@example.com", Subject: "Lunch", Body: "Lunch moved later.", Labels: []string{"SENT"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Inserted != 2 {
		t.Fatalf("inserted = %d, want 2", result.Inserted)
	}
	if err := st.MarkSyncStarted(ctx, now); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkSyncCompleted(ctx, now); err != nil {
		t.Fatal(err)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Messages != 2 || status.Senders != 2 || status.Since != 2026 {
		t.Fatalf("status = %#v", status)
	}
	search, err := st.Search(ctx, SearchOptions{Query: "project", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if search.TotalMatches != 1 || search.Truncated || len(search.Results) != 1 {
		t.Fatalf("search = %#v", search)
	}
	if search.Results[0].Ref != RefPrefix+"m1" || search.Results[0].Who != "Alice" {
		t.Fatalf("hit = %#v", search.Results[0])
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"m1")
	if err != nil {
		t.Fatal(err)
	}
	if open.Headers.ToAddress != "" {
		t.Fatalf("to address = %q, want empty", open.Headers.ToAddress)
	}
	if open.Body != "The project sync is Friday." {
		t.Fatalf("body = %q", open.Body)
	}
}

func TestInsertMessagesIgnoresExistingIDs(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	msg := Message{ID: "m1", ThreadID: "t1", Time: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC), FromAddress: "alice@example.com", Subject: "Hello", Body: "Hello"}
	first, err := st.InsertMessages(ctx, []Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.InsertMessages(ctx, []Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	if first.Inserted != 1 || second.Inserted != 0 {
		t.Fatalf("insert results = %#v %#v", first, second)
	}
}
