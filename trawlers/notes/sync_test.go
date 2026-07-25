package notes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/wal"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
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

func TestStateSpecsEndAtLastCommittedWALState(t *testing.T) {
	specs := stateSpecs("live", "current", []int64{128, 256})
	if len(specs) != 3 {
		t.Fatalf("stateSpecs returned %d states, want base plus two committed states", len(specs))
	}
	if got := specs[len(specs)-1].offset; got != 256 {
		t.Fatalf("final state offset = %d, want final committed offset 256", got)
	}
}

func TestStateSpecsIgnoreTornWALTailAfterLastCommit(t *testing.T) {
	f := newFixture(t, true)
	defer f.close()
	updateBody(t, f.db, "committed before torn tail", 20)

	commits, data, err := wal.CommitOffsetsFile(f.path() + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) == 0 {
		t.Fatal("fixture WAL has no committed state")
	}
	lastCommit := commits[len(commits)-1]
	torn := append(append([]byte(nil), data...), make([]byte, 17)...)
	parsed, err := wal.CommitOffsets(torn)
	if err != nil {
		t.Fatal(err)
	}

	specs := stateSpecs("live", "current", parsed)
	if got := specs[len(specs)-1].offset; got != lastCommit {
		t.Fatalf("final state offset = %d, want last committed offset %d", got, lastCommit)
	}
	if got := specs[len(specs)-1].offset; got >= int64(len(torn)) {
		t.Fatalf("final state offset = %d, want it before torn WAL length %d", got, len(torn))
	}
}

func TestMalformedHistoricalStateCanBeSkipped(t *testing.T) {
	if !shouldSkipHistoricalState(notesdb.ErrMalformed, 0, 2) {
		t.Fatal("malformed historical state should be skipped")
	}
	if shouldSkipHistoricalState(notesdb.ErrMalformed, 1, 2) {
		t.Fatal("malformed final state must fail")
	}
	if shouldSkipHistoricalState(errors.New("permission denied"), 0, 2) {
		t.Fatal("non-malformed historical failure must fail")
	}
}

func TestSyncSkipsMalformedBaseWhenCommittedWALIsValid(t *testing.T) {
	f := newFixture(t, true)
	defer f.close()

	var pageSize, pageNumber int64
	if err := f.db.QueryRow("pragma page_size").Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow(
		"select rootpage from sqlite_master where name = 'ZICNOTEDATA'",
	).Scan(&pageNumber); err != nil {
		t.Fatal(err)
	}
	var warmed []byte
	if err := f.db.QueryRow("select ZDATA from ZICNOTEDATA where Z_PK = 100").Scan(&warmed); err != nil {
		t.Fatal(err)
	}

	base, err := os.OpenFile(f.path(), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	corruption := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	if _, err := base.WriteAt(corruption, (pageNumber-1)*pageSize); err != nil {
		_ = base.Close()
		t.Fatal(err)
	}
	if err := base.Close(); err != nil {
		t.Fatal(err)
	}

	updateBody(t, f.db, "recovered from committed WAL", 20)

	combined, err := notesdb.Open(context.Background(), f.path())
	if err != nil {
		t.Fatalf("base plus committed WAL should be valid: %v", err)
	}
	if err := combined.Close(); err != nil {
		t.Fatal(err)
	}

	before, err := filepath.Glob(filepath.Join(os.TempDir(), "opentrawl-notes-wal-state-*"))
	if err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	crawler := New()
	crawler.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	report, err := crawler.Sync(context.Background(), req)
	if err != nil {
		closeStore(t, req)
		t.Fatal(err)
	}
	if report.Added == 0 {
		closeStore(t, req)
		t.Fatal("sync added no note versions")
	}
	closeStore(t, req)

	readReq := testRequest(t, archivePath, output.JSON, nil, false)
	result, err := crawler.Search(
		context.Background(), readReq, trawlkit.Query{Text: "recovered", Limit: 10},
	)
	closeStore(t, readReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("search returned %d results, want the final WAL-backed note", len(result.Results))
	}

	after, err := filepath.Glob(filepath.Join(os.TempDir(), "opentrawl-notes-wal-state-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("temporary WAL states before=%d after=%d; sync leaked temporary state", len(before), len(after))
	}
}
