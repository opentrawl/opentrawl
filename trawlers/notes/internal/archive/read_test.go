package archive_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
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

func TestSearchExcludesNotesDroppedFromTheNotesTable(t *testing.T) {
	ctx := context.Background()
	st := openArchive(t)
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// First sync archives a note whose body holds the needle.
	alpha := archive.Note{ID: "note-alpha", Title: "Alpha"}
	applyBatch(t, st, alpha, []archive.BodyInsert{bodyInsert("note-alpha", "alpha needle body", notesdb.AppleDateFloat(20))})
	// A later sync no longer lists that note (deleted at source): ReplaceNotes
	// rewrites the notes table without it, but its recovered versions and FTS
	// rows stay behind. Search must not surface that orphaned, blank-title note.
	beta := archive.Note{ID: "note-beta", Title: "Beta"}
	applyBatch(t, st, beta, []archive.BodyInsert{bodyInsert("note-beta", "beta filler body", notesdb.AppleDateFloat(30))})

	results, total, err := st.Search(ctx, "needle", archive.SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 || total != 0 {
		t.Fatalf("search for orphaned note: results=%d total=%d, want 0/0", len(results), total)
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
	data := fixtureZData(text)
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

// fixtureZData wraps text in the minimal gzip protobuf shape DecodeText
// accepts, so fixture bodies project to text and land in the FTS index the
// way real synced bodies do.
func fixtureZData(text string) []byte {
	note := fixtureProtoField(2, []byte(text))
	document := fixtureProtoField(3, note)
	body := fixtureProtoField(2, document)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		panic(err)
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

func fixtureProtoField(field int, data []byte) []byte {
	var scratch [10]byte
	keyLen := binary.PutUvarint(scratch[:], uint64(field<<3|2))
	out := append([]byte{}, scratch[:keyLen]...)
	lenLen := binary.PutUvarint(scratch[:], uint64(len(data)))
	out = append(out, scratch[:lenLen]...)
	return append(out, data...)
}

// The search header promises newest first. Rank only picks which version
// represents a note; it must never decide which note comes first.
func TestSearchOrdersNotesNewestFirstAcrossRank(t *testing.T) {
	ctx := context.Background()
	st := openArchive(t)
	defer func() {
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// The older note matches the term four times, so FTS ranks it far above
	// the newer note's single match.
	oldBody := bodyInsert("note-old", "needle needle needle needle", notestime.Format(time.Date(2015, 3, 1, 12, 0, 0, 0, time.UTC)))
	newBody := bodyInsert("note-new", "one needle hiding in a long meandering body of unrelated words", notestime.Format(time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)))
	_, err := st.ApplySync(ctx, archive.SyncBatch{
		Notes: []archive.Note{
			{ID: "note-old", Title: "Old"},
			{ID: "note-new", Title: "New"},
		},
		Bodies:       []archive.BodyInsert{oldBody, newBody},
		LastSeenAt:   notestime.Format(time.Date(2025, 1, 1, 0, 1, 0, 0, time.UTC)),
		ReplaceNotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	results, _, err := st.Search(ctx, "needle", archive.SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if results[0].NoteID != "note-new" || results[1].NoteID != "note-old" {
		t.Fatalf("order = [%s, %s], want newest first [note-new, note-old]", results[0].NoteID, results[1].NoteID)
	}
}
