package notes

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/wal"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestSyncSearchOpenAndAtTime(t *testing.T) {
	f := newFixture(t, true)
	defer f.close()
	updateBody(t, f.db, "first synthetic edit", 20)
	updateBody(t, f.db, "second synthetic edit", 30)
	updateBody(t, f.db, "third synthetic edit", 40)
	offsets, _, err := wal.CommitOffsetsFile(f.path() + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	if len(offsets) < 2 {
		t.Fatalf("wal commits = %d, want at least 2", len(offsets))
	}
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	report, err := c.Sync(context.Background(), req)
	closeStore(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Added != 4 {
		t.Fatalf("versions added = %d, want 4", report.Added)
	}
	if report.Updated != 4 {
		t.Fatalf("updated = %d, want 4", report.Updated)
	}

	readReq := testRequest(t, archivePath, output.JSON, nil, false)
	search, err := c.Search(context.Background(), readReq, trawlkit.Query{Text: "second", Limit: 20})
	closeStore(t, readReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("search results = %d, want 1", len(search.Results))
	}
	if !strings.Contains(search.Results[0].Snippet, "second synthetic edit") {
		t.Fatalf("snippet = %q", search.Results[0].Snippet)
	}

	var openBuf bytes.Buffer
	openReq := testRequest(t, archivePath, output.JSON, &openBuf, false)
	if err := c.Open(context.Background(), openReq, search.Results[0].Ref); err != nil {
		t.Fatal(err)
	}
	closeStore(t, openReq)
	var opened openOutput
	if err := json.Unmarshal(openBuf.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	// Search returns a note-level ref, so opening it reads the note's current
	// body. The snippet matched a historical edit ("second"); the open card
	// shows what the note says now ("third"). History stays in versions/at-time.
	if opened.Text != "third synthetic edit" {
		t.Fatalf("open text = %q", opened.Text)
	}

	var versionsBuf bytes.Buffer
	versionsReq := testRequest(t, archivePath, output.JSON, &versionsBuf, false)
	if err := c.runVersions(context.Background(), versionsReq); err == nil {
		t.Fatal("versions without args succeeded")
	}
	closeStore(t, versionsReq)

	versionsBuf.Reset()
	versionsReq = testRequest(t, archivePath, output.JSON, &versionsBuf, false)
	versionsReq.Args = []string{"note-alpha"}
	if err := c.runVersions(context.Background(), versionsReq); err != nil {
		t.Fatal(err)
	}
	closeStore(t, versionsReq)
	var versions versionListOutput
	if err := json.Unmarshal(versionsBuf.Bytes(), &versions); err != nil {
		t.Fatal(err)
	}
	if len(versions.Versions) != 4 {
		t.Fatalf("versions = %d, want 4", len(versions.Versions))
	}

	atCases := []struct {
		name      string
		requested string
		match     string
		text      string
	}{
		{
			name:      "before first version",
			requested: "2001-01-01T00:00:09Z",
			match:     "none_before_time",
		},
		{
			name:      "exact modified time",
			requested: "2001-01-01T00:00:20Z",
			match:     "exact_modified_time",
			text:      "first synthetic edit",
		},
		{
			name:      "between versions",
			requested: "2001-01-01T00:00:25Z",
			match:     "latest_modified_before",
			text:      "first synthetic edit",
		},
		{
			name:      "after last version",
			requested: "2001-01-01T00:00:41Z",
			match:     "latest_modified_before",
			text:      "third synthetic edit",
		},
	}
	for _, tc := range atCases {
		t.Run("at-time "+tc.name, func(t *testing.T) {
			at := runAtTimeCase(t, c, archivePath, "note-alpha", tc.requested)
			if at.Match != tc.match {
				t.Fatalf("match = %q, want %q", at.Match, tc.match)
			}
			if tc.text == "" {
				if at.Version != nil {
					t.Fatalf("at-time version = %#v, want none", at.Version)
				}
				return
			}
			if at.Version == nil || at.Version.Text != tc.text {
				t.Fatalf("at-time version = %#v, want text %q", at.Version, tc.text)
			}
		})
	}
}

func TestSyncBuildsShortRefsAndOpenResolvesThem(t *testing.T) {
	f := newFixture(t, false)
	defer f.close()
	updateBody(t, f.db, "short ref synthetic edit", 20)
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()

	req := testRequest(t, archivePath, output.JSON, nil, true)
	if _, err := c.Sync(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	records, err := c.ShortRefRecords(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 {
		t.Fatal("short ref records = 0, want at least 1")
	}
	if _, err := req.RebuildShortRefs(context.Background(), records); err != nil {
		t.Fatal(err)
	}
	closeStore(t, req)

	searchReq := testRequest(t, archivePath, output.JSON, nil, false)
	search, err := c.Search(context.Background(), searchReq, trawlkit.Query{Text: "synthetic", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	aliases, err := searchReq.ShortRefAliases(context.Background(), []string{search.Results[0].Ref})
	if err != nil {
		t.Fatal(err)
	}
	shortRef := aliases[search.Results[0].Ref]
	closeStore(t, searchReq)
	if !trawlkit.ValidShortRef(shortRef) {
		t.Fatalf("search short ref = %q, want a valid short ref", shortRef)
	}

	var openBuf bytes.Buffer
	openReq := testRequest(t, archivePath, output.JSON, &openBuf, false)
	if err := c.Open(context.Background(), openReq, shortRef); err != nil {
		t.Fatalf("open by short ref %q: %v", shortRef, err)
	}
	closeStore(t, openReq)
	var opened openOutput
	if err := json.Unmarshal(openBuf.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Text != "short ref synthetic edit" {
		t.Fatalf("open text = %q", opened.Text)
	}
}

func TestSyncCountsOneObservationForUnchangedFinalBody(t *testing.T) {
	f := newFixture(t, false)
	defer f.close()
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	report, err := c.Sync(context.Background(), req)
	closeStore(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Updated != 1 {
		t.Fatalf("updated = %d, want 1", report.Updated)
	}

	readReq := testRequest(t, archivePath, output.JSON, nil, false)
	st, err := archive.UseExisting(context.Background(), readReq.Store, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	status, err := st.Status(context.Background())
	closeStore(t, readReq)
	if err != nil {
		t.Fatal(err)
	}
	if status.Observations != 1 {
		t.Fatalf("observations = %d, want 1", status.Observations)
	}
}

func TestSyncBackfillsCurrentTitleForHistoricalVersionSearch(t *testing.T) {
	f := newFixture(t, true)
	defer f.close()
	setTitle(t, f.db, "Needle title")
	updateBody(t, f.db, "first synthetic edit", 20)
	updateBody(t, f.db, "second synthetic edit", 30)
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	report, err := c.Sync(context.Background(), req)
	closeStore(t, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Added < 2 {
		t.Fatalf("versions added = %d, want at least 2", report.Added)
	}

	searchReq := testRequest(t, archivePath, output.JSON, nil, false)
	search, err := c.Search(context.Background(), searchReq, trawlkit.Query{Text: "needle", Limit: 20})
	closeStore(t, searchReq)
	if err != nil {
		t.Fatal(err)
	}
	// The current title is backfilled onto every recovered version's FTS row, so
	// a title search matches historical versions too. Note-level dedup then
	// collapses those matches to one hit for the note.
	if len(search.Results) != 1 {
		t.Fatalf("title search results = %d, want 1", len(search.Results))
	}
	if search.Results[0].Ref != archive.RefForNote("note-alpha") {
		t.Fatalf("title search ref = %q, want note ref", search.Results[0].Ref)
	}
}

func TestSyncStoreDoesNotReplaceCurrentMetadata(t *testing.T) {
	live := newFixture(t, false)
	setTitle(t, live.db, "Current title")
	live.close()
	old := newFixture(t, false)
	setTitle(t, old.db, "Old title")
	updateBody(t, old.db, "old synthetic body", 5)
	old.close()
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = live.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	if _, err := c.Sync(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	closeStore(t, req)

	req = testRequest(t, archivePath, output.JSON, nil, true)
	req.Args = []string{old.path()}
	c.storeLabel = "old-copy"
	if err := c.runSyncStore(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	closeStore(t, req)

	readReq := testRequest(t, archivePath, output.JSON, nil, false)
	st, err := archive.UseExisting(context.Background(), readReq.Store, archivePath)
	if err != nil {
		t.Fatal(err)
	}
	note, err := st.ResolveNote(context.Background(), "note-alpha")
	if err != nil {
		t.Fatal(err)
	}
	closeStore(t, readReq)
	if note.Title != "Current title" {
		t.Fatalf("title = %q, want Current title", note.Title)
	}
}

type fixture struct {
	dir string
	db  *sql.DB
}

func newFixture(t *testing.T, walMode bool) fixture {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", filepath.Join(dir, "NoteStore.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if walMode {
		if _, err := db.Exec("pragma journal_mode=wal"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("pragma wal_autocheckpoint=0"); err != nil {
			t.Fatal(err)
		}
	}
	createSchema(t, db)
	insertInitialRows(t, db)
	if walMode {
		if _, err := db.Exec("pragma wal_checkpoint(truncate)"); err != nil {
			t.Fatal(err)
		}
	}
	return fixture{dir: dir, db: db}
}

func (f fixture) path() string {
	return filepath.Join(f.dir, "NoteStore.sqlite")
}

func (f fixture) close() {
	if f.db != nil {
		_ = f.db.Close()
	}
}

func createSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	// Apple declares the Core Data date columns TIMESTAMP, matching the real
	// NoteStore.sqlite. go-sqlite3 converts any whole-second value stored as
	// INTEGER in a TIMESTAMP column to time.Time, so this schema (not a plain
	// REAL one) is what exercises the date-read path the crawler ships against.
	_, err := db.Exec(`
create table ZICNOTEDATA (Z_PK integer primary key, ZDATA blob);
create table ZICCLOUDSYNCINGOBJECT (
  Z_PK integer primary key,
  ZIDENTIFIER text,
  ZNOTEDATA integer,
  ZTITLE1 text,
  ZTITLE2 text,
  ZFOLDER integer,
  ZISPASSWORDPROTECTED integer,
  ZNEEDSINITIALFETCHFROMCLOUD integer,
  ZCREATIONDATE1 timestamp,
  ZCREATIONDATE3 timestamp,
  ZMODIFICATIONDATE1 timestamp,
  ZTYPEUTI text,
  ZMEDIA integer,
  ZNOTE integer,
  ZFILENAME text
);`)
	if err != nil {
		t.Fatal(err)
	}
}

func insertInitialRows(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec("insert into ZICNOTEDATA (Z_PK, ZDATA) values (100, ?)", noteBody(t, "initial synthetic text")); err != nil {
		t.Fatal(err)
	}
	// ZCREATIONDATE1 and ZMODIFICATIONDATE1 are whole-second values stored as
	// INTEGER, the storage class that triggers go-sqlite3's TIMESTAMP-to-time
	// conversion on read.
	_, err := db.Exec(`
insert into ZICCLOUDSYNCINGOBJECT
  (Z_PK, ZIDENTIFIER, ZNOTEDATA, ZTITLE1, ZTITLE2, ZFOLDER, ZCREATIONDATE1, ZCREATIONDATE3, ZMODIFICATIONDATE1)
values
  (1, null, null, null, 'Fixture folder', null, null, null, null),
  (10, 'note-alpha', 100, 'Alpha', null, 1, 1, null, 10)`)
	if err != nil {
		t.Fatal(err)
	}
}

func updateBody(t *testing.T, db *sql.DB, text string, modified float64) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("update ZICNOTEDATA set ZDATA = ? where Z_PK = 100", noteBody(t, text)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.Exec("update ZICCLOUDSYNCINGOBJECT set ZMODIFICATIONDATE1 = ? where ZIDENTIFIER = 'note-alpha'", modified); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func setTitle(t *testing.T, db *sql.DB, title string) {
	t.Helper()
	if _, err := db.Exec("update ZICCLOUDSYNCINGOBJECT set ZTITLE1 = ? where ZIDENTIFIER = 'note-alpha'", title); err != nil {
		t.Fatal(err)
	}
}

func noteBody(t *testing.T, text string) []byte {
	t.Helper()
	note := protoField(2, []byte(text))
	document := protoField(3, note)
	return gzipBytes(t, protoField(2, document))
}

func protoField(field int, data []byte) []byte {
	var buf [10]byte
	keyLen := binary.PutUvarint(buf[:], uint64(field<<3|2))
	out := append([]byte{}, buf[:keyLen]...)
	lenLen := binary.PutUvarint(buf[:], uint64(len(data)))
	out = append(out, buf[:lenLen]...)
	return append(out, data...)
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func testRequest(t *testing.T, path string, format output.Format, out *bytes.Buffer, write bool) *trawlkit.Request {
	t.Helper()
	var st *ckstore.Store
	var err error
	if write {
		st, err = ckstore.Open(context.Background(), ckstore.Options{Path: path})
	} else {
		st, err = ckstore.OpenReadOnly(context.Background(), path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		out = &bytes.Buffer{}
	}
	return &trawlkit.Request{
		Store:  st,
		Paths:  trawlkit.Paths{Archive: path, Config: filepath.Join(filepath.Dir(path), "config.toml"), Logs: filepath.Join(filepath.Dir(path), "logs")},
		Format: format,
		Out:    out,
	}
}

func closeStore(t *testing.T, req *trawlkit.Request) {
	t.Helper()
	if req != nil && req.Store != nil {
		if err := req.Store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func runAtTimeCase(t *testing.T, c *Crawler, archivePath, noteID, requested string) archive.AtTimeResult {
	t.Helper()
	var atBuf bytes.Buffer
	atReq := testRequest(t, archivePath, output.JSON, &atBuf, false)
	atReq.Args = []string{noteID}
	c.atTimeRaw = requested
	if err := c.runAtTime(context.Background(), atReq); err != nil {
		closeStore(t, atReq)
		t.Fatal(err)
	}
	closeStore(t, atReq)
	var at archive.AtTimeResult
	if err := json.Unmarshal(atBuf.Bytes(), &at); err != nil {
		t.Fatal(err)
	}
	return at
}
