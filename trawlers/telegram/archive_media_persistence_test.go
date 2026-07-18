package telecrawl

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
)

func TestCancelledArchiveMediaBackfillPersistsCompletedDownloadsForResume(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telegram.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	})

	now := time.Date(2026, 7, 18, 10, 0, 0, 0, time.UTC)
	chat := store.Chat{JID: "100", Kind: "user", Name: "Alice Example", LastMessageAt: now, MessageCount: 1}
	message := store.Message{
		SourcePK: 1, ChatJID: chat.JID, ChatName: chat.Name, MessageID: "0:1",
		Timestamp: now, Text: "synthetic message", MediaType: "photo",
	}
	if _, err := st.ReplaceAll(ctx, store.ImportStats{SourcePath: "/synthetic/telegram", FinishedAt: now}, nil, []store.Chat{chat}, nil, nil, nil, nil, []store.Message{message}); err != nil {
		t.Fatal(err)
	}

	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	archivePath := filepath.Join(t.TempDir(), "media", "attachment.bin")
	result := telegramdesktop.ArchiveMediaResult{Updates: []telegramdesktop.ArchiveMediaUpdate{{
		SourcePK: message.SourcePK, MediaPath: archivePath, MediaSize: 123,
	}}}
	updated, _, err := persistArchivedTelegramMedia(cancelledCtx, st, result, context.Canceled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation after persistence", err)
	}
	if updated != 1 {
		t.Fatalf("updated = %d, want 1", updated)
	}

	remaining, err := archivedTelegramMediaCandidates(ctx, st, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("resume candidates = %#v, want completed attachment skipped", remaining)
	}
	stored, err := st.Messages(ctx, store.MessageFilter{ChatJID: chat.JID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].MediaPath != archivePath || stored[0].MediaSize != 123 {
		t.Fatalf("stored message = %#v, want persisted attachment", stored)
	}
}
