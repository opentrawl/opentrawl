package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
)

// Tripwire for TRAWL-103: the owner's self-chat (Telegram's Saved Messages)
// used to render its raw numeric id where the chat name belongs. The owner is
// identified structurally as the one distinct from_me sender, never by name.
func seedSavedMessagesArchive(t *testing.T, ownerJIDs ...string) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	chats := []store.Chat{
		{JID: "999", Kind: "user", Name: "999", LastMessageAt: now, MessageCount: 1},
		{JID: "300", Kind: "user", Name: "Recipient Person", LastMessageAt: now.Add(time.Minute), MessageCount: len(ownerJIDs)},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "999", ChatName: "999", MessageID: "0:1", Timestamp: now, Text: "self needle"},
	}
	for i, owner := range ownerJIDs {
		messages = append(messages, store.Message{
			SourcePK:  int64(2 + i),
			ChatJID:   "300",
			ChatName:  "Recipient Person",
			MessageID: "0:" + string(rune('2'+i)),
			SenderJID: owner,
			Timestamp: now.Add(time.Duration(1+i) * time.Minute),
			FromMe:    true,
			Text:      "note to recipient",
		})
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, nil, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSavedMessagesChatRendersItsName(t *testing.T) {
	t.Parallel()
	db := seedSavedMessagesArchive(t, "999")

	chatsOut, _, err := runCLI(t, "--db", db, "chats")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(chatsOut, "Saved Messages") {
		t.Fatalf("chats output missing Saved Messages:\n%s", chatsOut)
	}

	chatsJSONOut, _, err := runCLI(t, "--db", db, "chats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var chatsPayload struct {
		Chats []struct {
			ChatID string `json:"chat_id"`
			Name   string `json:"name"`
		} `json:"chats"`
	}
	if err := json.Unmarshal([]byte(chatsJSONOut), &chatsPayload); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, chat := range chatsPayload.Chats {
		if chat.ChatID == "999" {
			found = true
			if chat.Name != "Saved Messages" {
				t.Fatalf("self chat name = %q, want Saved Messages", chat.Name)
			}
		}
	}
	if !found {
		t.Fatalf("self chat missing from chats --json:\n%s", chatsJSONOut)
	}

	messagesOut, _, err := runCLI(t, "--db", db, "messages", "--chat", "999")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(messagesOut, "Saved Messages") {
		t.Fatalf("messages output missing Saved Messages:\n%s", messagesOut)
	}

	messagesJSONOut, _, err := runCLI(t, "--db", db, "messages", "--chat", "999", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var messagesPayload messagesJSONEnvelope
	if err := json.Unmarshal([]byte(messagesJSONOut), &messagesPayload); err != nil {
		t.Fatal(err)
	}
	if len(messagesPayload.Messages) != 1 || messagesPayload.Messages[0].Who != "Saved Messages" {
		t.Fatalf("messages --json = %#v, want who Saved Messages", messagesPayload.Messages)
	}

	searchJSONOut, _, err := runCLI(t, "--db", db, "search", "self needle", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var searchPayload searchJSON
	if err := json.Unmarshal([]byte(searchJSONOut), &searchPayload); err != nil {
		t.Fatal(err)
	}
	if len(searchPayload.Results) != 1 || searchPayload.Results[0].Who != "Saved Messages" {
		t.Fatalf("search --json = %#v, want who Saved Messages", searchPayload.Results)
	}

	openOut, _, err := runCLI(t, "--db", db, "open", "telecrawl:msg/1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(openOut, "chat: Saved Messages") {
		t.Fatalf("open output missing Saved Messages chat header:\n%s", openOut)
	}

	openJSONOut, _, err := runCLI(t, "--db", db, "open", "telecrawl:msg/1", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var openPayload openJSON
	if err := json.Unmarshal([]byte(openJSONOut), &openPayload); err != nil {
		t.Fatal(err)
	}
	if openPayload.Chat.Name != "Saved Messages" {
		t.Fatalf("open --json chat name = %q, want Saved Messages", openPayload.Chat.Name)
	}
}

func TestSavedMessagesNameNeedsUnambiguousOwner(t *testing.T) {
	t.Parallel()
	db := seedSavedMessagesArchive(t, "999", "998")
	chatsOut, _, err := runCLI(t, "--db", db, "chats")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(chatsOut, "Saved Messages") {
		t.Fatalf("chats output renamed a chat with an ambiguous owner:\n%s", chatsOut)
	}
}
