package telegram

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
)

func TestSyncImportErrorSurfacesTelegramSessionRejectedCause(t *testing.T) {
	sourceErr := fmt.Errorf("telegram session is not authorized: %w", tgerr.New(401, "AUTH_KEY_UNREGISTERED"))
	err := syncImportError(sourceErr)
	command, ok := err.(commandError)
	if !ok {
		t.Fatalf("error = %T, want commandError", err)
	}
	body := command.ErrorBody()
	if body.Code != "telegram_session" {
		t.Fatalf("code = %q, want telegram_session", body.Code)
	}
	if body.Message != sourceErr.Error() {
		t.Fatalf("message = %q, want original error %q", body.Message, sourceErr.Error())
	}
	if body.Remedy != telegramdesktop.TelegramSessionRejectedRemedy {
		t.Fatalf("remedy = %q, want %q", body.Remedy, telegramdesktop.TelegramSessionRejectedRemedy)
	}
	if !strings.Contains(command.Error(), sourceErr.Error()) || !strings.Contains(command.Error(), telegramdesktop.TelegramSessionRejectedRemedy) {
		t.Fatalf("human error = %q, want cause and remedy", command.Error())
	}
	if !tgerr.Is(command, "AUTH_KEY_UNREGISTERED") {
		t.Fatalf("wrapped error lost AUTH_KEY_UNREGISTERED: %v", command)
	}
	t.Logf("json_error code=%q message=%q remedy=%q", body.Code, body.Message, body.Remedy)
	t.Logf("human_error=%q", command.Error())
}

func TestStoreImportResultMergesBoundedAcquisition(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	chat := store.Chat{JID: "100", Kind: "user", Name: "Alice Example", LastMessageAt: now, MessageCount: 2}
	old := store.Message{SourcePK: 1, ChatJID: "100", ChatName: chat.Name, MessageID: "1", Timestamp: now.Add(-time.Hour), Text: "older archived message"}
	observed := store.Message{SourcePK: 2, ChatJID: "100", ChatName: chat.Name, MessageID: "2", Timestamp: now, Text: "before update"}
	if _, err := st.ReplaceAll(ctx, store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now}, nil, []store.Chat{chat}, nil, nil, nil, nil, []store.Message{old, observed}); err != nil {
		t.Fatal(err)
	}

	observed.Text = "after update"
	result := telegramdesktop.ImportResult{
		Stats:    store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now.Add(time.Minute)},
		Chats:    []store.Chat{chat},
		Messages: []store.Message{observed},
	}
	stats, err := storeImportResult(ctx, st, &result, "")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Updated != 1 || stats.Removed != 0 {
		t.Fatalf("sync stats = %+v, want added=0 updated=1 removed=0", stats)
	}
	messages, err := st.Messages(ctx, store.MessageFilter{ChatJID: "100", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Text != old.Text || messages[1].Text != observed.Text {
		t.Fatalf("messages after bounded sync = %#v, want old record preserved and observed record updated", messages)
	}

	observed.Text = "after filtered update"
	result.Messages = []store.Message{observed}
	stats, err = storeImportResult(ctx, st, &result, "100")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Added != 0 || stats.Updated != 1 || stats.Removed != 0 {
		t.Fatalf("filtered sync stats = %+v, want added=0 updated=1 removed=0", stats)
	}
	messages, err = st.Messages(ctx, store.MessageFilter{ChatJID: "100", Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Text != old.Text || messages[1].Text != observed.Text {
		t.Fatalf("messages after filtered bounded sync = %#v, want old record preserved and observed record updated", messages)
	}
	matches, err := st.Search(ctx, store.MessageFilter{Query: "filtered update", ChatJID: "100", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].SourcePK != observed.SourcePK {
		t.Fatalf("search after observed update = %#v, want the updated record indexed", matches)
	}
}

func TestStoreImportResultInterruptedBeforeWritePreservesArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	chat := store.Chat{JID: "100", Kind: "user", Name: "Alice Example", LastMessageAt: now, MessageCount: 1}
	archived := store.Message{SourcePK: 1, ChatJID: "100", ChatName: chat.Name, MessageID: "1", Timestamp: now, Text: "must survive"}
	if _, err := st.ReplaceAll(ctx, store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now}, nil, []store.Chat{chat}, nil, nil, nil, nil, []store.Message{archived}); err != nil {
		t.Fatal(err)
	}

	interrupted, cancel := context.WithCancel(ctx)
	cancel()
	result := telegramdesktop.ImportResult{Stats: store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now.Add(time.Minute)}}
	if _, err := storeImportResult(interrupted, st, &result, ""); err == nil {
		t.Fatal("interrupted sync succeeded, want error")
	}
	messages, err := st.Messages(ctx, store.MessageFilter{ChatJID: "100", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Text != archived.Text {
		t.Fatalf("archive after interrupted sync = %#v, want original record unchanged", messages)
	}
}

func TestArchivedTelegramMediaCandidatesHonorChatScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	chats := []store.Chat{
		{JID: "100", Kind: "user", Name: "First", LastMessageAt: now, MessageCount: 2},
		{JID: "200", Kind: "user", Name: "Second", LastMessageAt: now, MessageCount: 1},
	}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "100", MessageID: "0:1", Timestamp: now, MediaType: "photo"},
		{SourcePK: 2, ChatJID: "100", MessageID: "0:2", Timestamp: now, MediaType: "photo", MediaPath: "/archive/existing"},
		{SourcePK: 3, ChatJID: "200", MessageID: "0:3", Timestamp: now, MediaType: "photo"},
	}
	if _, err := st.ReplaceAll(ctx, store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now}, nil, chats, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	got, err := archivedTelegramMediaCandidates(ctx, st, "100", map[int64]struct{}{2: {}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].SourcePK != 1 {
		t.Fatalf("chat-scoped candidates = %#v, want only missing attachment in chat 100", got)
	}
}

func TestLocalSyncPreservesEstablishedCloudProjection(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	chat := store.Chat{JID: "100", Kind: "group", Name: "Example", LastMessageAt: now, MessageCount: 1}
	cloud := store.Message{
		SourcePK: 1, ChatJID: chat.JID, MessageID: "0:1", Timestamp: now,
		SenderJID: "sender", SenderName: "Remote sender", FromMe: false,
		MessageType: "service", MediaType: "document", TopicID: "7",
		ReplyToID: "0:2", ForwardJSON: `{"remote":true}`, Views: 42, Pinned: true,
	}
	if _, err := st.ReplaceAll(ctx, store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now}, nil, []store.Chat{chat}, nil, nil, nil, nil, []store.Message{cloud}); err != nil {
		t.Fatal(err)
	}
	local := []store.Message{{
		SourcePK: 1, ChatJID: chat.JID, MessageID: "0:1", Timestamp: now,
		FromMe: true, MessageType: "message", MediaType: "file", MetadataType: "local",
	}}
	if err := preserveArchivedTelegramProjection(ctx, st, local); err != nil {
		t.Fatal(err)
	}
	got := local[0]
	if got.FromMe || got.SenderJID != cloud.SenderJID || got.MessageType != cloud.MessageType || got.MediaType != cloud.MediaType || got.TopicID != cloud.TopicID || got.ForwardJSON != cloud.ForwardJSON || got.Views != cloud.Views || !got.Pinned {
		t.Fatalf("preserved projection = %#v", got)
	}
	if got.MetadataType != "local" {
		t.Fatalf("local metadata was lost: %#v", got)
	}
}
