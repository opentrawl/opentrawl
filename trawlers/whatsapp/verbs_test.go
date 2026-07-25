package whatsapp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func TestMessageListItemsPreferShortRefs(t *testing.T) {
	message := store.Message{SourcePK: 42, ChatJID: "15550001001@s.whatsapp.net", MessageID: "example"}
	full := messageRef(message)

	items := messageListItems([]store.Message{message}, map[string]string{full: "abc12"})
	if len(items) != 1 || items[0].Ref != "abc12" {
		t.Fatalf("items = %#v", items)
	}
}

func TestPublicMessageListIsStableAndDoesNotExposeArchiveRows(t *testing.T) {
	message := store.Message{
		SourcePK:    42,
		ChatJID:     "fixture@g.us",
		ChatName:    "Launch group",
		MessageID:   "message-42",
		SenderJID:   "alice@example.com",
		SenderName:  "Alice Example",
		Timestamp:   time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC),
		Text:        "Synthetic launch note",
		RawType:     99,
		MessageType: "text",
		MediaPath:   "/synthetic/private/archive.jpg",
	}
	ref := messageRef(message)
	value := publicMessageList([]store.Message{message}, map[string]string{ref: "abc12"}, 3)
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var got trawlkit.MessageList
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Ref != "whatsapp:msg/message-42" || got.Messages[0].ShortRef != "abc12" || got.Total != 3 || !got.Truncated {
		t.Fatalf("public message contract = %#v", got)
	}
	for _, privateField := range []string{"source_pk", "chat_jid", "message_id", "sender_jid", "raw_type", "message_type", "media_path"} {
		if strings.Contains(string(data), `"`+privateField+`"`) {
			t.Fatalf("public JSON exposed archive field %q: %s", privateField, data)
		}
	}
}
