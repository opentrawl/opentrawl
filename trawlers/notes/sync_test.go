package notes

import (
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
)

func TestSplitBodilessNotesKeepsOnlyNotesWithABody(t *testing.T) {
	notes := []notesdb.Note{
		{ID: "with-body"},
		{ID: "no-body"},
	}
	withBody := map[string]bool{"with-body": true}

	real, skipped := splitBodilessNotes(notes, withBody)
	if len(real) != 1 || real[0].ID != "with-body" {
		t.Fatalf("real = %#v, want only with-body", real)
	}
	if len(skipped) != 1 || skipped[0].ID != "no-body" {
		t.Fatalf("skipped = %#v, want only no-body", skipped)
	}
}

func TestSkipWarningsNamesEachReason(t *testing.T) {
	skipped := []notesdb.Note{
		{ID: "a", NeedsInitialFetch: true},
		{ID: "b", NeedsInitialFetch: true},
		{ID: "c", PasswordProtected: true},
		{ID: "d"}, // no flag: unexplained
	}

	warnings := skipWarnings(skipped)
	joined := strings.Join(warnings, "\n")

	wantFragments := []string{
		"Skipped 2 notes still downloading from iCloud",
		"Skipped 1 password-protected note",
		"Skipped 1 notes with no body and no known reason",
	}
	for _, fragment := range wantFragments {
		if !strings.Contains(joined, fragment) {
			t.Fatalf("warnings missing %q; got:\n%s", fragment, joined)
		}
	}
	if len(warnings) != 3 {
		t.Fatalf("warnings = %d lines, want 3:\n%s", len(warnings), joined)
	}
}

func TestSkipWarningsEmptyWhenNothingSkipped(t *testing.T) {
	if got := skipWarnings(nil); got != nil {
		t.Fatalf("skipWarnings(nil) = %#v, want nil", got)
	}
}

func TestNoteIDsWithBodyCollectsEveryBodyNote(t *testing.T) {
	set := noteIDsWithBody(nil)
	if len(set) != 0 {
		t.Fatalf("empty bodies gave %d ids, want 0", len(set))
	}
}
