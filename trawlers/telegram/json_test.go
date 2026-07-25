package telegram

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func TestMessageJSONUsesSharedPublicContract(t *testing.T) {
	message := store.Message{
		SourcePK:   42,
		ChatJID:    "fixture-peer",
		ChatName:   "Launch group",
		MessageID:  "provider-message-42",
		SenderJID:  "provider-sender",
		SenderName: "Alice Example",
		Timestamp:  time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC),
		Text:       "Synthetic launch note",
		RawType:    99,
		MediaPath:  "/synthetic/private/archive.jpg",
	}
	ref := messageRef(message.SourcePK)
	value := messageJSONEnvelope([]store.Message{message}, 3, map[string]string{ref: "abc12"})
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Messages) != 1 || value.Messages[0].Ref != ref || value.Messages[0].ShortRef != "abc12" || value.Total != 3 || !value.Truncated {
		t.Fatalf("public message contract = %#v", value)
	}
	for _, privateField := range []string{"source_pk", "chat_jid", "message_id", "sender_jid", "raw_type", "media_path"} {
		if strings.Contains(string(data), `"`+privateField+`"`) {
			t.Fatalf("public JSON exposed archive field %q: %s", privateField, data)
		}
	}
}

func TestSourceListsDoNotExposeStoredPathsOrRawProviderBlobs(t *testing.T) {
	contacts, err := json.Marshal(contactJSONRows([]store.Contact{{
		JID: "fixture-peer", FullName: "Avery Example", LID: "privacy-provider-id", AvatarPath: "/private/archive/avatar.jpg",
	}}))
	if err != nil {
		t.Fatal(err)
	}
	folders, err := json.Marshal(folderJSONRows([]store.Folder{{ID: "1", Title: "Launch", FlagsJSON: `{"raw":true}`}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"privacy-provider-id", "/private/archive", "flags_json", `\"raw\"`} {
		if strings.Contains(string(contacts), forbidden) || strings.Contains(string(folders), forbidden) {
			t.Fatalf("source JSON exposed internal value %q: contacts=%s folders=%s", forbidden, contacts, folders)
		}
	}
	if !strings.Contains(string(contacts), "Avery Example") || !strings.Contains(string(folders), "Launch") {
		t.Fatalf("source JSON lost useful fields: contacts=%s folders=%s", contacts, folders)
	}
}
