package archive_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
)

func TestAtTimeExcludesFractionalVersionAfterRequestedTime(t *testing.T) {
	ctx := context.Background()
	st := openArchive(t)
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	note := archive.Note{ID: "note-alpha", Title: "Alpha"}
	requested := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	body := bodyInsert("note-alpha", "after requested time", notesdb.AppleDateFloat(0.123))
	applyBatch(t, st, note, []archive.BodyInsert{body})

	result, err := st.AtTime(ctx, note, requested)
	if err != nil {
		t.Fatal(err)
	}
	if result.Match != "none_before_time" {
		t.Fatalf("match = %q, want none_before_time", result.Match)
	}
	if result.Version != nil {
		t.Fatalf("version = %#v, want none", result.Version)
	}
}

func TestVersionsOrderFractionalTimesNumerically(t *testing.T) {
	ctx := context.Background()
	st := openArchive(t)
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	note := archive.Note{ID: "note-alpha", Title: "Alpha"}
	early := bodyInsert("note-alpha", "version at 20.10", notesdb.AppleDateFloat(20.10))
	late := bodyInsert("note-alpha", "version at 20.15", notesdb.AppleDateFloat(20.15))
	applyBatch(t, st, note, []archive.BodyInsert{early, late})

	versions, err := st.Versions(ctx, note.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}
	if versions[0].SHA256 != late.ZDataSHA256 {
		t.Fatalf("first version sha = %s, want %s", versions[0].SHA256, late.ZDataSHA256)
	}

	requested := time.Date(2001, 1, 1, 0, 0, 20, 120_000_000, time.UTC)
	result, err := st.AtTime(ctx, note, requested)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version == nil {
		t.Fatal("at-time returned no version")
	}
	if result.Version.SHA256 != early.ZDataSHA256 {
		t.Fatalf("at-time sha = %s, want %s", result.Version.SHA256, early.ZDataSHA256)
	}
}

func openArchive(t *testing.T) *archive.Store {
	t.Helper()
	st, err := archive.Open(context.Background(), filepath.Join(t.TempDir(), "notes.db"))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func applyBatch(t *testing.T, st *archive.Store, note archive.Note, bodies []archive.BodyInsert) {
	t.Helper()
	_, err := st.ApplySync(context.Background(), archive.SyncBatch{
		Notes:        []archive.Note{note},
		Bodies:       bodies,
		LastSeenAt:   notestime.Format(time.Date(2001, 1, 1, 0, 1, 0, 0, time.UTC)),
		ReplaceNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func bodyInsert(noteID, text, modified string) archive.BodyInsert {
	data := []byte(text)
	return archive.BodyInsert{
		NoteID:           noteID,
		ZDataSHA256:      archive.SHA256(data),
		ZData:            data,
		Source:           "fixture",
		SourceDetail:     "test",
		SourceModifiedAt: modified,
		ObservedAt:       notestime.Format(time.Date(2001, 1, 1, 0, 1, 0, 0, time.UTC)),
		Title:            "Alpha",
	}
}
