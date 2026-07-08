package archive

import (
	"testing"

	"github.com/openclaw/imsgcrawl/internal/messages"
)

// Regression: Messages accounts carry "E:"/"P:" prefixes. The raw
// string must not win normalization, or the owner set stores
// "e:user@host" and never matches the handles table, leaving the
// owner's other handles rendered by contact name instead of "me".
func TestOwnerHandlesStripAccountPrefixes(t *testing.T) {
	data := &messages.ArchiveData{
		Messages: []messages.Message{
			{IsFromMe: true, Account: "E:owner@example.com"},
			{IsFromMe: true, Account: "P:+14155550100"},
		},
		Handles: []messages.Handle{
			{ID: "owner@example.com", DisplayName: "Owner Name"},
			{ID: "+14155550100", DisplayName: "Owner Name"},
			{ID: "owner@other.com", DisplayName: "Owner Name"},
			{ID: "friend@example.com", DisplayName: "Friend"},
		},
	}
	mappings := []ContactMapping{
		{Kind: "email", NormalizedHandle: "owner@example.com", ContactKey: "c1"},
		{Kind: "email", NormalizedHandle: "owner@other.com", ContactKey: "c1"},
		{Kind: "phone", NormalizedHandle: "14155550100", ContactKey: "c1"},
		{Kind: "email", NormalizedHandle: "friend@example.com", ContactKey: "c2"},
	}
	owners := applyOwnerHandles(data, nil, mappings)
	if len(owners) == 0 {
		t.Fatal("no owner handles derived")
	}
	for _, o := range owners {
		if o.NormalizedHandle == "e:owner@example.com" {
			t.Fatalf("prefix leaked into normalized owner handle: %+v", owners)
		}
	}
	for _, h := range data.Handles {
		want := "me"
		if h.ID == "friend@example.com" {
			want = "Friend"
		}
		if h.DisplayName != want {
			t.Fatalf("handle %s display %q, want %q", h.ID, h.DisplayName, want)
		}
	}
}

func TestOwnerHandleCandidatesOrder(t *testing.T) {
	got := ownerHandleCandidates("E:owner@example.com")
	if len(got) == 0 || got[0] != "owner@example.com" {
		t.Fatalf("stripped candidate must come first, got %v", got)
	}
	if got[len(got)-1] != "E:owner@example.com" {
		t.Fatalf("raw candidate must come last, got %v", got)
	}
}
