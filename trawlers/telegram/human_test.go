package telecrawl

import (
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func TestMessageListItemsPreferShortRefs(t *testing.T) {
	message := store.Message{SourcePK: 42, Text: "hello"}
	full := messageRef(message.SourcePK)

	items := messageListItems([]store.Message{message}, map[string]string{full: "abc12"})
	if len(items) != 1 || items[0].Ref != "abc12" {
		t.Fatalf("items = %#v", items)
	}
}
