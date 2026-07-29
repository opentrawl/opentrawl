package whatsapp

import (
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
)

func TestMessageWhereUsesKnownParticipantBeforeOpaqueChatID(t *testing.T) {
	message := store.Message{
		ChatJID:    "118390991671363@lid",
		ChatName:   "118390991671363@lid",
		SenderName: "Avery Example",
	}
	if got := messageWhere(message); got != "Avery Example" {
		t.Fatalf("human conversation label = %q", got)
	}
}

func TestResolvedParticipantNamesDropOpaqueValues(t *testing.T) {
	participants := []string{
		"118390991671363@lid",
		"Alice Example",
		"228390991671363@lid",
		"Bob Example",
	}

	resolved := resolvedParticipantNames(participants)
	if len(resolved) != 2 || resolved[0] != "Alice Example" || resolved[1] != "Bob Example" {
		t.Fatalf("resolved participants = %#v", resolved)
	}
}
