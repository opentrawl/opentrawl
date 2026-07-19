package telegram

import (
	"reflect"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func TestCloudContactPreservesRicherLocalFields(t *testing.T) {
	t.Parallel()
	updated := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	local := store.Contact{
		JID: "42", PeerType: "user", Phone: "+15550100001", FullName: "Alice Example",
		BusinessName: "Example Studio", Username: "old-alice", LID: "local-id",
		AboutText: "Local profile", AvatarPath: "/synthetic/avatar.jpg", UpdatedAt: updated,
	}
	cloud := store.Contact{JID: "42", PeerType: "user", Username: "alice"}
	got := preserveLocalContactFields(cloud, local)
	if got.Username != "alice" {
		t.Fatalf("username = %q, want current cloud username", got.Username)
	}
	if got.Phone != local.Phone || got.FullName != local.FullName || got.BusinessName != local.BusinessName ||
		got.LID != local.LID || got.AboutText != local.AboutText || got.AvatarPath != local.AvatarPath || !got.UpdatedAt.Equal(updated) {
		t.Fatalf("merged contact = %#v, want richer local-only fields preserved", got)
	}
}

func TestCompletedHistoryRetainsPerDialogState(t *testing.T) {
	t.Parallel()
	state := telegramHistoryState{
		Complete:         true,
		CompletedDialogs: []string{"account:known"},
		DialogOffsets:    map[string]int{"account:new": 100},
	}
	completed := state.completedSet()

	recordTelegramHistoryBatch(&state, completed, "account:new", 50, false)
	if got := state.DialogOffsets["account:new"]; got != 50 {
		t.Fatalf("partial offset = %d, want 50", got)
	}
	recordTelegramHistoryBatch(&state, completed, "account:new", 1, true)

	if !state.Complete {
		t.Fatal("account-wide completion was lost")
	}
	if _, exists := state.DialogOffsets["account:new"]; exists {
		t.Fatalf("completed dialog retained an offset: %#v", state.DialogOffsets)
	}
	if want := []string{"account:known", "account:new"}; !reflect.DeepEqual(state.CompletedDialogs, want) {
		t.Fatalf("completed dialogs = %#v, want %#v", state.CompletedDialogs, want)
	}
}

func TestPartialHistoryActivatesProjectionPreservation(t *testing.T) {
	t.Parallel()
	for _, state := range []telegramHistoryState{
		{CompletedDialogs: []string{"account:complete"}},
		{DialogOffsets: map[string]int{"account:partial": 100}},
		{Complete: true},
	} {
		if !telegramHistoryStarted(state) {
			t.Fatalf("history state was not recognised as started: %#v", state)
		}
	}
	if telegramHistoryStarted(telegramHistoryState{}) {
		t.Fatal("empty history state was treated as started")
	}
}
