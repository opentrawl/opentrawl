package archive

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/openclaw/imsgcrawl/internal/messages"
)

func TestSearchUsesLegacyShortRefRows(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	syncedAt := time.Date(2026, 7, 2, 14, 3, 0, 0, time.UTC)
	data := messages.ArchiveData{
		SourcePath:       "synthetic-chat.db",
		SourceModifiedAt: syncedAt,
		ExtractedAt:      syncedAt,
		Handles: []messages.Handle{{
			SourceRowID: 1,
			ID:          "+15550100001",
			Service:     "iMessage",
			DisplayName: "Alice Example",
		}},
		Chats: []messages.Chat{{
			SourceRowID:    1,
			GUID:           "chat-one",
			ChatIdentifier: "+15550100001",
			ServiceName:    "iMessage",
			DisplayName:    "Alice Example",
		}},
		Participants: []messages.Participant{{
			ChatRowID:   1,
			HandleRowID: 1,
		}},
		ChatMessages: []messages.ChatMessage{{
			ChatRowID:    1,
			MessageRowID: 1,
		}},
		Messages: []messages.Message{{
			SourceRowID: 1,
			GUID:        "message-one",
			HandleRowID: 1,
			Date:        1,
			Service:     "iMessage",
			Text:        "needle",
		}},
	}
	if err := st.ReplaceAll(ctx, data, nil, nil, syncedAt); err != nil {
		t.Fatal(err)
	}

	var alias string
	if err := st.store.DB().QueryRowContext(ctx, `select alias from short_refs where full_ref = ? order by length(alias), alias limit 1`, MessageRef("1")).Scan(&alias); err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `update short_refs set full_ref = ? where full_ref = ?`, LegacyMessageRefPrefix+"1", MessageRef("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := st.store.DB().ExecContext(ctx, `update sync_state set source_name = ? where source_name = ?`, legacySyncSource, syncSource); err != nil {
		t.Fatal(err)
	}

	page, err := st.SearchPage(ctx, "needle", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %#v, want one result", page.Items)
	}
	if page.Items[0].ShortRef != alias {
		t.Fatalf("short ref = %q, want legacy alias %q", page.Items[0].ShortRef, alias)
	}
}
