package telecrawl

import (
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
