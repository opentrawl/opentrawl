package archive_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

// plainNoteBody builds a gzip'd NoteStoreProto whose note_text is text and
// which has no attribute runs, so it projects to text verbatim.
func plainNoteBody(text string) []byte {
	note := protoBytes(2, []byte(text))
	document := protoBytes(3, note)
	store := protoBytes(2, document)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(store)
	_ = zw.Close()
	return buf.Bytes()
}

func protoBytes(field int, data []byte) []byte {
	var key [10]byte
	n := binary.PutUvarint(key[:], uint64(field<<3|2))
	var length [10]byte
	m := binary.PutUvarint(length[:], uint64(len(data)))
	out := append(append([]byte{}, key[:n]...), length[:m]...)
	return append(out, data...)
}

// seedSchemaV1Archive creates an archive, fills it with deliberately stale
// text/fts, then pins the recorded schema version back to 1 to simulate a
// pre-TRAWL-210 archive that the migration must self-heal on next open.
func seedSchemaV1Archive(t *testing.T, path string) {
	t.Helper()
	ctx := context.Background()
	st, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()
	mustExec(t, db, `insert into notes(note_id, title) values ('note-plain', 'Plain'), ('note-legacy', 'Legacy')`)

	// A decodable body stored with STALE projected text and stale fts.
	body := plainNoteBody("hello world")
	mustExec(t, db, `insert into note_versions
      (note_id, zdata_sha256, zdata, zdata_bytes, text, text_status, unsupported_reason, first_observed_at, latest_observed_at)
      values ('note-plain', 'sha-plain', ?, ?, 'STALE', 'decoded', '', 't', 't')`, body, len(body))
	mustExec(t, db, `insert into notes_fts(note_id, zdata_sha256, title, body) values ('note-plain', 'sha-plain', 'Plain', 'STALE')`)

	// A legacy binary-plist body with the old generic unsupported reason.
	legacy := append([]byte("bplist00"), 0x01, 0x02, 0x03)
	mustExec(t, db, `insert into note_versions
      (note_id, zdata_sha256, zdata, zdata_bytes, text, text_status, unsupported_reason, first_observed_at, latest_observed_at)
      values ('note-legacy', 'sha-legacy', ?, ?, '', 'unsupported', 'note body is not gzip or zlib data', 't', 't')`, legacy, len(legacy))

	// Pin recorded schema version back to 1.
	mustExec(t, db, `delete from schema_migrations`)
	mustExec(t, db, `insert into schema_migrations(version) values (1)`)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationReprojectsStoredBodiesOnce(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "notes.db")
	seedSchemaV1Archive(t, path)

	st, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()

	// Decodable row: STALE text replaced by the real projection.
	var text, status, reason string
	scanRow(t, db, "note-plain", &text, &status, &reason)
	if text != "hello world" || status != "decoded" || reason != "" {
		t.Fatalf("plain row after migration: text=%q status=%q reason=%q", text, status, reason)
	}

	// Legacy row: stays unsupported, but with a precise reason.
	scanRow(t, db, "note-legacy", &text, &status, &reason)
	if status != "unsupported" {
		t.Fatalf("legacy row status=%q, want unsupported", status)
	}
	if !contains(reason, "legacy binary-plist") {
		t.Fatalf("legacy row reason=%q, want mention of legacy binary-plist", reason)
	}

	// FTS consistency: decoded row indexed with new text, unsupported row not.
	if n := ftsCount(t, db, "hello"); n != 1 {
		t.Fatalf("fts matches for 'hello' = %d, want 1", n)
	}
	if n := ftsCount(t, db, "STALE"); n != 0 {
		t.Fatalf("fts still matches stale text 'STALE' = %d, want 0", n)
	}
	if total := ftsTotal(t, db); total != 1 {
		t.Fatalf("fts total rows = %d, want 1 (only the decoded row)", total)
	}

	if version := recordedVersion(t, db); version != archive.SchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, archive.SchemaVersion)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: migration must not run again and data stays put.
	st2, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st2.Close() }()
	scanRow(t, st2.DB(), "note-plain", &text, &status, &reason)
	if text != "hello world" {
		t.Fatalf("plain row after reopen: text=%q", text)
	}
	if total := ftsTotal(t, st2.DB()); total != 1 {
		t.Fatalf("fts total after reopen = %d, want 1", total)
	}
}

func scanRow(t *testing.T, db *sql.DB, noteID string, text, status, reason *string) {
	t.Helper()
	err := db.QueryRowContext(context.Background(),
		`select text, text_status, unsupported_reason from note_versions where note_id = ?`, noteID).
		Scan(text, status, reason)
	if err != nil {
		t.Fatalf("scan %s: %v", noteID, err)
	}
}

func recordedVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	var v int
	if err := db.QueryRowContext(context.Background(),
		`select coalesce(max(version), 0) from schema_migrations`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func ftsCount(t *testing.T, db *sql.DB, term string) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(),
		`select count(*) from notes_fts where notes_fts match ?`, term).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func ftsTotal(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRowContext(context.Background(), `select count(*) from notes_fts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func contains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
