package cli

import (
	"testing"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/whomatch"
)

func TestStatusEnvelopeFromControlUsesTypedCrawlkitStatus(t *testing.T) {
	status := control.NewStatus("imessage", "Archive is fresh.")
	status.State = "ok"
	status.LastSyncAt = "2026-07-02T14:03:00Z"
	status.Counts = []control.Count{
		control.NewCount("messages", "messages", 42),
	}

	got, err := statusEnvelopeFromControl(Source{ID: "imessage"}, &status)
	if err != nil {
		t.Fatal(err)
	}
	if got.AppID != "imessage" || got.State != "ok" || got.Summary != "Archive is fresh." {
		t.Fatalf("status = %#v", got)
	}
	if got.LastSyncAt != "2026-07-02T14:03:00Z" || len(got.Counts) != 1 {
		t.Fatalf("typed fields were not preserved: %#v", got)
	}
	if got.Counts[0].Value.text("messages", "messages") != "42" {
		t.Fatalf("count value = %#v", got.Counts[0].Value)
	}
}

func TestWhoCandidatesFromMatchesConvertsTypedMatches(t *testing.T) {
	lastSeen := time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)
	got := whoCandidatesFromMatches([]whomatch.Candidate{{
		Who:         "Alex Example",
		Identifiers: []string{"alex@example.com"},
		LastSeen:    lastSeen,
		Messages:    7,
	}}, "imessage", "alex@example.com")

	if len(got) != 1 {
		t.Fatalf("candidates = %#v", got)
	}
	candidate := got[0]
	if candidate.Who != "Alex Example" || candidate.MatchQuality != "exact" || candidate.LastSeen != "2026-07-01T08:00:00Z" || candidate.Messages != 7 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if len(candidate.Sources) != 1 || candidate.Sources[0] != "imessage" {
		t.Fatalf("sources = %#v", candidate.Sources)
	}
}
