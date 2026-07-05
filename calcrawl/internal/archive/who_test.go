package archive

import "testing"

// TRAWL-111: calendar-invite@lu.ma fronts several distinct organizers. A
// shared mailbox must not union them into one silently-picked entity; each
// name stays its own candidate and the mailbox appears on all of them.
func TestBuildWhoCandidatesSharedMailboxDoesNotMerge(t *testing.T) {
	records := []whoRecord{
		{displayName: "Frontier Tower SF", email: "calendar-invite@lu.ma", lastSeen: "2026-02-05T02:00:00Z", eventUID: "event-frontier"},
		{displayName: "Frontier Tower SF", email: "calendar-invite@lu.ma", address: "MAILTO:calendar-invite@lu.ma", lastSeen: "2026-02-05T02:00:00Z", eventUID: "event-frontier"},
		{displayName: "ClawCon", email: "calendar-invite@lu.ma", lastSeen: "2026-05-14T10:00:00Z", eventUID: "event-clawcon"},
		{displayName: "ClawCon", email: "calendar-invite@lu.ma", address: "MAILTO:calendar-invite@lu.ma", lastSeen: "2026-05-14T10:00:00Z", eventUID: "event-clawcon"},
	}
	candidates := buildWhoCandidates(records)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want ClawCon and Frontier Tower SF separate", candidates)
	}
	for i, want := range []struct {
		who    string
		events int64
	}{{"ClawCon", 1}, {"Frontier Tower SF", 1}} {
		got := candidates[i]
		if got.Who != want.who || got.Messages != want.events {
			t.Fatalf("candidates[%d] = %#v, want who %q with %d event", i, got, want.who, want.events)
		}
		if len(got.Identifiers) != 2 || got.Identifiers[0] != "calendar-invite@lu.ma" || got.Identifiers[1] != "MAILTO:calendar-invite@lu.ma" {
			t.Fatalf("candidates[%d].Identifiers = %#v, want the shared mailbox listed", i, got.Identifiers)
		}
		// The shared mailbox must not reach the event filter either:
		// filtering by it would pull the other entity's events in under
		// this name. With no owned identifiers, the filter is name-only.
		if filter := got.Filter(); len(filter.Identifiers) != 0 || filter.Who != want.who {
			t.Fatalf("candidates[%d].Filter() = %#v, want name-only filter", i, filter)
		}
	}
}

// Rows with no name evidence on a shared mailbox cannot be attributed to
// any of the names it fronts: they cluster with each other under the
// mailbox itself, never silently under one of the names.
func TestBuildWhoCandidatesSharedMailboxNamelessRowsClusterUnderMailbox(t *testing.T) {
	records := []whoRecord{
		{displayName: "Frontier Tower SF", email: "calendar-invite@lu.ma", lastSeen: "2026-02-05T02:00:00Z", eventUID: "event-frontier"},
		{displayName: "ClawCon", email: "calendar-invite@lu.ma", lastSeen: "2026-05-14T10:00:00Z", eventUID: "event-clawcon"},
		{email: "calendar-invite@lu.ma", lastSeen: "2026-06-01T10:00:00Z", eventUID: "event-bare-1"},
		{email: "calendar-invite@lu.ma", lastSeen: "2026-06-02T10:00:00Z", eventUID: "event-bare-2"},
	}
	candidates := buildWhoCandidates(records)
	if len(candidates) != 3 {
		t.Fatalf("candidates = %#v, want two names plus the mailbox cluster", candidates)
	}
	for i, want := range []struct {
		who    string
		events int64
	}{{"calendar-invite@lu.ma", 2}, {"ClawCon", 1}, {"Frontier Tower SF", 1}} {
		if candidates[i].Who != want.who || candidates[i].Messages != want.events {
			t.Fatalf("candidates[%d] = %#v, want who %q with %d events", i, candidates[i], want.who, want.events)
		}
	}
}

// One person whose name arrives in both "Last, First" and "First Last"
// order on the same email is one name, not a shared mailbox: the word set
// matches, so the email keeps merging.
func TestBuildWhoCandidatesNameWordOrderVariantsStillMerge(t *testing.T) {
	records := []whoRecord{
		{displayName: "Moore, Matthew", email: "mmoore@example.com", lastSeen: "2026-01-01T10:00:00Z", eventUID: "event-1"},
		{displayName: "Matthew Moore", email: "mmoore@example.com", lastSeen: "2026-01-02T10:00:00Z", eventUID: "event-2"},
	}
	candidates := buildWhoCandidates(records)
	if len(candidates) != 1 || candidates[0].Messages != 2 {
		t.Fatalf("candidates = %#v, want one person with 2 events", candidates)
	}
	if filter := candidates[0].Filter(); len(filter.Identifiers) != 1 || filter.Identifiers[0] != "mmoore@example.com" {
		t.Fatalf("Filter() = %#v, want the owned email kept for filtering", filter)
	}
}

// A display name that merely echoes the record's own email — verbatim or
// buried in "Name <email>" cruft — is identifier data, not evidence of a
// second identity: the email keeps merging.
func TestBuildWhoCandidatesIdentifierEchoNameStillMerges(t *testing.T) {
	records := []whoRecord{
		{displayName: "Josh Palmer", email: "josh@example.com", lastSeen: "2026-01-01T10:00:00Z", eventUID: "event-1"},
		{displayName: "josh@example.com", email: "josh@example.com", lastSeen: "2026-01-02T10:00:00Z", eventUID: "event-2"},
		{displayName: "Josh Palmer <josh@example.com>", email: "josh@example.com", lastSeen: "2026-01-03T10:00:00Z", eventUID: "event-3"},
	}
	candidates := buildWhoCandidates(records)
	if len(candidates) != 1 || candidates[0].Who != "Josh Palmer" || candidates[0].Messages != 3 {
		t.Fatalf("candidates = %#v, want one Josh Palmer with 3 events", candidates)
	}
}
