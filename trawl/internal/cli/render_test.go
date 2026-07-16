package cli

import (
	"bytes"
	"testing"
	"time"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

func TestRenderStatusTable(t *testing.T) {
	t.Setenv("COLUMNS", "120")
	now := time.Date(2026, 7, 2, 14, 5, 0, 0, time.UTC)
	results := []StatusResult{
		{
			Source: Source{ID: "imessage", DisplayName: "iMessage"},
			Status: &federationv1.SourceStatus{
				AppId: "imessage", State: "ok", LastSyncRfc3339: "2026-07-02T14:03:00Z",
				Counts: []*federationv1.Count{
					{Id: "messages", Label: "messages", Value: 12345},
					{Id: "chats", Label: "chats", Value: 87},
					{Id: "since", Label: "since", Value: 2014},
				},
			},
		},
		{
			Source: Source{ID: "telegram", DisplayName: "Telegram"},
			Status: &federationv1.SourceStatus{
				AppId: "telegram", State: "stale", LastSyncRfc3339: "2026-06-29T14:05:00Z",
				Counts: []*federationv1.Count{
					{Id: "messages", Label: "messages", Value: 23456},
				},
			},
		},
		{
			Source: Source{ID: "gmail", DisplayName: "Gmail"},
			Status: &federationv1.SourceStatus{
				AppId: "gmail", State: "error", Summary: "auth expired",
			},
		},
	}
	var out bytes.Buffer
	if err := renderStatusTable(&out, results, now); err != nil {
		t.Fatal(err)
	}
	want := "source    state  recently synced  headline\n" +
		"iMessage  ok     2m ago           12,345 messages · 87 chats · since 2014\n" +
		"Telegram  stale  3d ago           23,456 messages\n" +
		"Gmail     error  not synced yet   auth expired\n"
	if out.String() != want {
		t.Fatalf("status table:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestStatusHeadlineDropsZeroSinceAndYearCounts(t *testing.T) {
	headline := statusHeadline(&federationv1.SourceStatus{State: "ok", Counts: []*federationv1.Count{
		{Id: "messages", Label: "messages", Value: 0},
		{Id: "since", Label: "since", Value: 0},
		{Id: "oldest_year", Label: "oldest year", Value: 0},
		{Id: "senders", Label: "senders", Value: 2},
	}})

	want := "0 messages · 2 senders"
	if headline != want {
		t.Fatalf("headline = %q, want %q", headline, want)
	}
}

func TestStatusHeadlineUsesFailedSummaryBeforeCounts(t *testing.T) {
	headline := statusHeadline(&federationv1.SourceStatus{
		State:   "missing",
		Summary: "Not synced yet.",
		Counts: []*federationv1.Count{
			{Id: "messages", Label: "messages", Value: 0},
			{Id: "senders", Label: "senders", Value: 0},
		},
	})
	if headline != "Not synced yet." {
		t.Fatalf("headline = %q, want normalised failed summary", headline)
	}
}

func TestRenderStatusDetailKeepsCanonicalWarningsErrorsAndSetup(t *testing.T) {
	status := &federationv1.SourceStatus{
		State: "error", Summary: "Archive could not be read.",
		Warnings: []string{"The archive may be incomplete."},
		Errors:   []string{"SQLite reported corruption."},
		SetupRequirements: []*federationv1.SetupRequirement{{
			State: federationv1.SetupState_SETUP_STATE_NEEDS_ACTION, Explanation: "OpenTrawl is waiting for source access.",
			Action: federationv1.SetupActionKind_SETUP_ACTION_KIND_RUN_COMMAND, Command: []string{"trawl", "telegram", "pair"},
		}},
	}
	var out bytes.Buffer
	if err := renderStatusDetail(&out, StatusResult{Source: Source{ID: "notes", DisplayName: "Notes"}, Status: status}, time.Time{}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"summary: Archive could not be read.", "The archive may be incomplete.", "SQLite reported corruption.", "OpenTrawl is waiting for source access.", "next: trawl telegram pair"} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Fatalf("status detail omitted %q:\n%s", want, out.String())
		}
	}
}

func TestNormalizeSelfKeepsKnownIdentity(t *testing.T) {
	if got := normalizeSelf("ME (@avery_example)"); got != "me (@avery_example)" {
		t.Fatalf("normalizeSelf = %q", got)
	}
	if got := normalizeSelf(" me () "); got != "me" {
		t.Fatalf("normalizeSelf empty identity = %q", got)
	}
}
