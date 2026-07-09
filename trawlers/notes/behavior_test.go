package notes

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// buildArchive writes an archive directly through the archive package (no WAL
// fixture needed) so list/search/status/versions tests can set up folders and
// bodies precisely.
func buildArchive(t *testing.T, notesToInsert []archive.Note, bodies []archive.BodyInsert) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notes.db")
	st, err := archive.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.ApplySync(context.Background(), archive.SyncBatch{
		Notes:        notesToInsert,
		Bodies:       bodies,
		LastSeenAt:   notestime.Format(time.Date(2026, 1, 1, 0, 1, 0, 0, time.UTC)),
		ReplaceNotes: true,
		SyncState:    map[string]string{"last_sync_at": notestime.Format(time.Now())},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// testBody builds a version whose ZData decodes to text, using the same
// gzip-protobuf shape noteBody (crawler_test.go) builds for the WAL fixture
// path, so it decodes and lands in the FTS index like a real synced body.
func testBody(t *testing.T, noteID, text string, modified time.Time) archive.BodyInsert {
	t.Helper()
	data := noteBody(t, text)
	return archive.BodyInsert{
		NoteID:           noteID,
		ZDataSHA256:      archive.SHA256(data),
		ZData:            data,
		Source:           "fixture",
		SourceDetail:     "test",
		SourceModifiedAt: notestime.Format(modified),
		ObservedAt:       notestime.Format(modified),
	}
}

// TestListUnknownFolderIsADataError pins the refuse-and-name-candidates rule:
// a folder name that matches nothing in the archive is a data error (exit
// 1), never a calm empty list indistinguishable from a real empty folder.
func TestListUnknownFolderIsADataError(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "A", Folder: "Notes"}},
		[]archive.BodyInsert{testBody(t, "note-a", "body a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	c := New()
	req := testRequest(t, path, output.Text, nil, false)
	req.Args = []string{"NoSuchFolder"}
	err := c.runList(context.Background(), req)
	closeStore(t, req)
	if err == nil {
		t.Fatal("list of unknown folder succeeded, want a data error")
	}
	body := output.ErrorBodyFor(err)
	if body.Code != "unknown_folder" {
		t.Fatalf("error code = %q, want unknown_folder", body.Code)
	}
	if !strings.Contains(body.Remedy, "Notes") {
		t.Fatalf("remedy = %q, want it to name the known folder %q", body.Remedy, "Notes")
	}
}

// TestListKnownFolderWithNoNotesStaysCalm pins the other half of the same
// rule: a folder that is real (a note names it) but has no browsable notes
// right now (its only note has no recovered body) still exits 0 with the
// plain empty message, not an error.
func TestListKnownFolderWithNoNotesStaysCalm(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{
			{ID: "note-a", Title: "A", Folder: "Notes"},
			{ID: "note-b", Title: "B", Folder: "Unfetched"},
		},
		[]archive.BodyInsert{testBody(t, "note-a", "body a", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	c := New()
	var buf bytes.Buffer
	req := testRequest(t, path, output.Text, &buf, false)
	req.Args = []string{"Unfetched"}
	if err := c.runList(context.Background(), req); err != nil {
		t.Fatalf("list of known, empty folder failed: %v", err)
	}
	closeStore(t, req)
	if got := strings.TrimSpace(buf.String()); got != "No notes in Unfetched." {
		t.Fatalf("output = %q, want the calm empty message", got)
	}
}

// TestSearchHitsOmitWho pins the who-column fix: Apple Notes has no per-note
// author, so a hit never carries the constant "me" — the shared list
// renderer drops a column with no varying value on its own.
func TestSearchHitsOmitWho(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "A", Folder: "Notes"}},
		[]archive.BodyInsert{testBody(t, "note-a", "findable needle text", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	c := New()
	req := testRequest(t, path, output.JSON, nil, false)
	result, err := c.Search(context.Background(), req, trawlkit.Query{Text: "needle", Limit: 20})
	closeStore(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 {
		t.Fatalf("results = %d, want 1", len(result.Results))
	}
	if result.Results[0].Who != "" {
		t.Fatalf("who = %q, want empty (no constant \"me\")", result.Results[0].Who)
	}
}

// TestOpenJSONVersionOmitsDuplicateText pins the JSON dedupe: the note body
// shows up once, at the top level, not a second time inside "version".
func TestOpenJSONVersionOmitsDuplicateText(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "A", Folder: "Notes"}},
		[]archive.BodyInsert{testBody(t, "note-a", "unique body text", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	c := New()
	var buf bytes.Buffer
	req := testRequest(t, path, output.JSON, &buf, false)
	if err := c.Open(context.Background(), req, "note-a"); err != nil {
		t.Fatal(err)
	}
	closeStore(t, req)
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["text"] != "unique body text" {
		t.Fatalf("top-level text = %v, want the body", decoded["text"])
	}
	version, ok := decoded["version"].(map[string]any)
	if !ok {
		t.Fatalf("version = %#v, want an object", decoded["version"])
	}
	if _, has := version["text"]; has {
		t.Fatalf("version object still carries text: %#v", version)
	}
}

// TestVersionsHeaderNamesNoteAndCount pins the versions table header and
// column order: a heading sentence in list/search's voice, then a table led
// by the typeable ref, never the raw content hash.
func TestVersionsHeaderNamesNoteAndCount(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "Alpha", Folder: "Notes"}},
		[]archive.BodyInsert{
			testBody(t, "note-a", "first body", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
			testBody(t, "note-a", "second body", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)),
		})
	c := New()
	var buf bytes.Buffer
	req := testRequest(t, path, output.Text, &buf, false)
	req.Args = []string{"note-a"}
	if err := c.runVersions(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	closeStore(t, req)
	lines := strings.Split(buf.String(), "\n")
	if len(lines) < 2 {
		t.Fatalf("output = %q, want a heading line and a table", buf.String())
	}
	if lines[0] != "2 recovered versions of Alpha, newest first." {
		t.Fatalf("heading = %q, want the note name and count", lines[0])
	}
	header := lines[2]
	fields := strings.Fields(header)
	if len(fields) == 0 || fields[0] != "ref" {
		t.Fatalf("table header = %q, want ref to lead", header)
	}
	for _, field := range fields {
		if field == "version" {
			t.Fatalf("table header = %q, want no raw-hash \"version\" column", header)
		}
	}
}

// TestOpenCardVersionRefIsTypeable pins the fix for the non-typeable short
// SHA on the open card: the card must show a ref for the exact version
// displayed that Open() itself accepts, and the raw hash earns no row of its
// own once a real handle exists.
func TestOpenCardVersionRefIsTypeable(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "Alpha", Folder: "Notes"}},
		[]archive.BodyInsert{testBody(t, "note-a", "card body", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	c := New()

	buildReq := testRequest(t, path, output.JSON, nil, true)
	records, err := c.ShortRefRecords(context.Background(), buildReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildReq.AssignShortRefs(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	closeStore(t, buildReq)

	var buf bytes.Buffer
	openReq := testRequest(t, path, output.Text, &buf, false)
	if err := c.Open(context.Background(), openReq, "note-a"); err != nil {
		t.Fatal(err)
	}
	closeStore(t, openReq)

	var refLine, versionLine string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.HasPrefix(line, "Ref: ") {
			refLine = strings.TrimPrefix(line, "Ref: ")
		}
		if strings.HasPrefix(line, "Version: ") {
			versionLine = strings.TrimPrefix(line, "Version: ")
		}
	}
	if refLine == "" {
		t.Fatalf("no Ref line in output: %q", buf.String())
	}
	if versionLine == "" {
		t.Fatalf("no Version line in output: %q", buf.String())
	}
	if !trawlkit.ValidShortRef(versionLine) {
		t.Fatalf("Version = %q, want a typeable short ref", versionLine)
	}
	if versionLine == refLine {
		t.Fatalf("Version %q duplicates Ref %q, want the note ref and the version ref distinct when opened by note ref", versionLine, refLine)
	}
	// The whole point: what is printed on screen must open cleanly.
	reopenReq := testRequest(t, path, output.JSON, nil, false)
	if err := c.Open(context.Background(), reopenReq, versionLine); err != nil {
		t.Fatalf("open %q (the printed Version ref) failed: %v", versionLine, err)
	}
	closeStore(t, reopenReq)
}

// TestOpenCardOmitsVersionRowWhenSameAsRef pins the dedupe half of the same
// fix: opening a version ref directly already shows that ref once, so a
// second "Version" row repeating it would be dead weight.
func TestOpenCardOmitsVersionRowWhenSameAsRef(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "Alpha", Folder: "Notes"}},
		[]archive.BodyInsert{testBody(t, "note-a", "card body", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	c := New()

	buildReq := testRequest(t, path, output.JSON, nil, true)
	records, err := c.ShortRefRecords(context.Background(), buildReq)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildReq.AssignShortRefs(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	closeStore(t, buildReq)

	versionsReq := testRequest(t, path, output.JSON, nil, false)
	versionsReq.Args = []string{"note-a"}
	var versionsBuf bytes.Buffer
	versionsReq.Out = &versionsBuf
	if err := c.runVersions(context.Background(), versionsReq); err != nil {
		t.Fatal(err)
	}
	closeStore(t, versionsReq)
	var versions versionListOutput
	if err := json.Unmarshal(versionsBuf.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions.Versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions.Versions))
	}

	aliasReq := testRequest(t, path, output.JSON, nil, false)
	aliases, err := aliasReq.ShortRefAliases(context.Background(), []string{versions.Versions[0].Ref})
	if err != nil {
		t.Fatal(err)
	}
	closeStore(t, aliasReq)
	versionShortRef := aliases[versions.Versions[0].Ref]
	if versionShortRef == "" {
		t.Fatal("no short ref alias for the version ref")
	}

	var buf bytes.Buffer
	openReq := testRequest(t, path, output.Text, &buf, false)
	if err := c.Open(context.Background(), openReq, versionShortRef); err != nil {
		t.Fatal(err)
	}
	closeStore(t, openReq)
	if strings.Contains(buf.String(), "Version:") {
		t.Fatalf("output = %q, want no Version row when it would repeat Ref", buf.String())
	}
}

// TestStatusOkSummaryHasNoBannedWordAndMatchesText pins the freshness-wording
// fix: the JSON summary for state "ok" must say the same thing as the human
// renderer already prints ("Recently synced."), and neither says "fresh".
func TestStatusOkSummaryHasNoBannedWordAndMatchesText(t *testing.T) {
	path := buildArchive(t,
		[]archive.Note{{ID: "note-a", Title: "Alpha", Folder: "Notes"}},
		[]archive.BodyInsert{testBody(t, "note-a", "status body", time.Now())})
	c := New()
	req := testRequest(t, path, output.JSON, nil, false)
	status, err := c.Status(context.Background(), req)
	closeStore(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "ok" {
		t.Fatalf("state = %q, want ok", status.State)
	}
	if status.Summary != "Recently synced." {
		t.Fatalf("summary = %q, want %q (matching the human renderer)", status.Summary, "Recently synced.")
	}
	if strings.Contains(strings.ToLower(status.Summary), "fresh") {
		t.Fatalf("summary = %q, contains the banned word", status.Summary)
	}
}
