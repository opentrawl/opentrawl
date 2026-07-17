package trawlkit

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestRunChatsUsesSharedLifecycleAndCompletesResult(t *testing.T) {
	ctx := context.Background()
	stateRoot := t.TempDir()
	archive := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	store, err := ckstore.Open(ctx, ckstore.Options{Path: archive})
	if err != nil {
		t.Fatal(err)
	}
	const ref = "telegram:chat/42"
	req := &Request{Store: store}
	if _, err := req.AssignShortRefs(ctx, []ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	aliases, err := req.ShortRefAliases(ctx, []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	var got ChatQuery
	crawler := &searchLifecycleChatCrawler{
		testChatCrawler: &testChatCrawler{chatsFn: func(_ context.Context, _ *Request, query ChatQuery) ([]Chat, error) {
			got = query
			return []Chat{
				{ID: "42", Ref: ref, Title: "Alex-Lee", Unread: int64Ptr(2)},
				{ID: "43", Ref: "telegram:chat/43", Title: "Someone Else", Unread: int64Ptr(1)},
			}, nil
		}},
	}
	result, err := RunChats(ctx, crawler, ChatQuery{With: "alex lee", Unread: true, Limit: 1}, ChatsRunOptions{
		StateRoot: stateRoot,
		Timeout:   time.Second,
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if crawler.prepareCalls != 1 {
		t.Fatalf("read preparation calls = %d, want 1", crawler.prepareCalls)
	}
	if !got.All || got.Limit != 0 || got.With != "" || !got.Unread {
		t.Fatalf("source acquisition query = %#v", got)
	}
	if len(result.Chats) != 1 || result.Chats[0].Ref != ref || result.ShortRefs[ref] != aliases[ref] || result.Truncated {
		t.Fatalf("result = %#v, want one matched chat with alias %q", result, aliases[ref])
	}
}

func TestExecuteChatsUsesResolvedPersonAliasesAsExactEvidence(t *testing.T) {
	var got ChatQuery
	result, err := executeChats(context.Background(), &testChatCrawler{chatsFn: func(_ context.Context, _ *Request, query ChatQuery) ([]Chat, error) {
		got = query
		return []Chat{
			{ID: "telegram", Title: "Anya Telegram"},
			{ID: "messages", Title: "Anna Example"},
			{ID: "unrelated", Title: "Sally Example"},
		}, nil
	}}, nil, ChatQuery{With: "Al", WithAliases: []string{"Anna Example", "Anya Telegram"}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if !got.All || got.Limit != 0 || got.With != "" || len(got.WithAliases) != 0 {
		t.Fatalf("source acquisition query = %#v; identity filters must stay in the shared executor", got)
	}
	if len(result.Chats) != 2 || result.Chats[0].ID != "telegram" || result.Chats[1].ID != "messages" {
		t.Fatalf("resolved aliases did not select the same person across sources: %#v", result.Chats)
	}
}

type searchLifecycleChatCrawler struct {
	*testChatCrawler
	prepareCalls int
}

func (c *searchLifecycleChatCrawler) PrepareReadArchive(context.Context, string) error {
	c.prepareCalls++
	return nil
}

func TestExecuteChatsOwnsUnreadFilteringAndTruncation(t *testing.T) {
	result, err := executeChats(context.Background(), &testChatCrawler{chatsFn: func(_ context.Context, _ *Request, query ChatQuery) ([]Chat, error) {
		if !query.Unread || query.Limit != 3 {
			t.Fatalf("source query = %#v, want unread and one-row probe", query)
		}
		return []Chat{
			{ID: "1", Unread: int64Ptr(2)},
			{ID: "2", Unread: nil},
			{ID: "3", Unread: int64Ptr(1)},
		}, nil
	}}, nil, ChatQuery{Unread: true, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chats) != 2 || result.Truncated {
		t.Fatalf("result = %#v; nil read state must be filtered before truncation", result)
	}
}
