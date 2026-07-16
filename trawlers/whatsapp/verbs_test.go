package wacrawl

import (
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
)

func TestMessageListItemsPreferShortRefs(t *testing.T) {
	message := store.Message{SourcePK: 42, ChatJID: "15550001001@s.whatsapp.net", MessageID: "example"}
	full := messageRef(message)

	items := messageListItems([]store.Message{message}, map[string]string{full: "abc12"})
	if len(items) != 1 || items[0].Ref != "abc12" {
		t.Fatalf("items = %#v", items)
	}
}
