package notes

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

// seedStaleArchive builds a standalone archive at path, current in every way
// except its recorded schema_migrations version, which is pinned back to
// version. It carries one distinguishing note ("note-stale") that must never
// appear in a rebuilt archive -- if it does, the rebuild merged old data
// instead of doing the required full fresh import. Returns the
// seeded file's exact bytes so a caller can prove the parked copy is
// untouched.
func seedStaleArchive(t *testing.T, path string, version int) []byte {
	t.Helper()
	ctx := context.Background()
	st, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	db := st.DB()
	if _, err := db.Exec(`insert into notes(note_id, title) values ('note-stale', 'Stale seed')`); err != nil {
		t.Fatal(err)
	}
	body := []byte("stale seed body")
	if _, err := db.Exec(`insert into note_versions
      (note_id, zdata_sha256, zdata, zdata_bytes, text, text_status, unsupported_reason, first_observed_at, latest_observed_at)
      values ('note-stale', 'sha-stale', ?, ?, 'stale seed body', 'decoded', '', 't', 't')`, body, len(body)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`delete from schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`insert into schema_migrations(version) values (?)`, version); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestSyncRefusesNewerSchemaArchive covers an archive whose recorded version
// is newer than this build's SchemaVersion: an old binary must never demote
// a newer archive, so sync must fail and leave the file exactly as it found
// it -- nothing parked, nothing rewritten.
//
// In production this refusal happens in trawlkit's write-open path, which
// calls Crawler.PrepareArchive (== archive.PrepareArchive) before it ever
// opens req.Store -- Sync itself never runs. This test calls
// archive.PrepareArchive directly for the same reason: it is the real
// harness-lifecycle entry point for the check, not a crawler-in-isolation
// shortcut through Sync.
func TestSyncRefusesNewerSchemaArchive(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	seeded := seedStaleArchive(t, archivePath, archive.SchemaVersion+1)

	err := archive.PrepareArchive(context.Background(), archivePath)
	if err == nil {
		t.Fatal("prepare over a newer-schema archive succeeded, want it refused")
	}
	if !errors.Is(err, archive.ErrSchemaNewer) {
		t.Fatalf("prepare error = %v, want it to wrap archive.ErrSchemaNewer", err)
	}

	after, readErr := os.ReadFile(archivePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, seeded) {
		t.Fatal("newer-schema archive bytes changed after a refused prepare, want it untouched")
	}
	parkedPath := fmt.Sprintf("%s.v%d", archivePath, archive.SchemaVersion+1)
	if _, statErr := os.Stat(parkedPath); statErr == nil {
		t.Fatalf("prepare parked a newer-schema archive at %s, want nothing ever parked for a newer archive", parkedPath)
	}
}
