package telecrawl

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

func TestSyncImportErrorSurfacesTDataSessionRejectedCause(t *testing.T) {
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
	if body.Remedy != telegramdesktop.TDataSessionRejectedRemedy {
		t.Fatalf("remedy = %q, want %q", body.Remedy, telegramdesktop.TDataSessionRejectedRemedy)
	}
	if !strings.Contains(command.Error(), sourceErr.Error()) || !strings.Contains(command.Error(), telegramdesktop.TDataSessionRejectedRemedy) {
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
