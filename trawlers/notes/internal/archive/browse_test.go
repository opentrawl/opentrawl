package archive_test

import (
	"context"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
)

// TestStatusNotesExcludesRecentlyDeleted pins the fix for the 566-vs-569 count
// disagreement: Status must count the same population list and search browse
// (real notes, Recently Deleted left out), not every row in the notes table.
func TestStatusNotesExcludesRecentlyDeleted(t *testing.T) {
	ctx := context.Background()
	st := openArchive(t)
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	kept := archive.Note{ID: "note-kept", Title: "Kept", Folder: "Notes"}
	trashed := archive.Note{ID: "note-trashed", Title: "Trashed", Folder: "Recently Deleted"}
	_, err := st.ApplySync(ctx, archive.SyncBatch{
		Notes: []archive.Note{kept, trashed},
		Bodies: []archive.BodyInsert{
			bodyInsert("note-kept", "kept body", notestime.Format(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
			bodyInsert("note-trashed", "trashed body", notestime.Format(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
		},
		LastSeenAt:   notestime.Format(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)),
		ReplaceNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Notes != 1 {
		t.Fatalf("status.Notes = %d, want 1 (Recently Deleted excluded)", status.Notes)
	}
	items, err := st.ListNotes(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != int(status.Notes) {
		t.Fatalf("list returned %d notes, status.Notes = %d, want the same population", len(items), status.Notes)
	}
}

// TestKnownFoldersExcludesRecentlyDeletedAndIncludesBodylessNote pins the
// unknown-folder-vs-empty-folder distinction: a folder is known if any note
// (even one with no recovered body yet) names it, but Recently Deleted never
// counts as a folder a reader can list.
func TestKnownFoldersExcludesRecentlyDeletedAndIncludesBodylessNote(t *testing.T) {
	ctx := context.Background()
	st := openArchive(t)
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	real := archive.Note{ID: "note-real", Title: "Real", Folder: "Notes"}
	placeholder := archive.Note{ID: "note-placeholder", Title: "Placeholder", Folder: "Unfetched"}
	trashed := archive.Note{ID: "note-trashed", Title: "Trashed", Folder: "Recently Deleted"}
	_, err := st.ApplySync(ctx, archive.SyncBatch{
		Notes: []archive.Note{real, placeholder, trashed},
		Bodies: []archive.BodyInsert{
			bodyInsert("note-real", "real body", notestime.Format(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
			// placeholder gets no body: an unfetched iCloud note.
			bodyInsert("note-trashed", "trashed body", notestime.Format(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))),
		},
		LastSeenAt:   notestime.Format(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)),
		ReplaceNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	known, err := st.KnownFolders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"Notes": true, "Unfetched": true}
	got := map[string]bool{}
	for _, folder := range known {
		got[folder] = true
	}
	for name := range want {
		if !got[name] {
			t.Fatalf("known folders = %v, want %q present", known, name)
		}
	}
	if got["Recently Deleted"] {
		t.Fatalf("known folders = %v, want Recently Deleted excluded", known)
	}
	// Unfetched is known, but its only note has no recovered body, so it
	// browses empty rather than erroring.
	items, err := st.ListNotes(ctx, "Unfetched")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("list Unfetched = %d notes, want 0 (known folder, no browsable notes)", len(items))
	}
}
