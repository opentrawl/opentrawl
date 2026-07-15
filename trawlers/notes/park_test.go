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
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
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

// TestSyncParksOlderSchemaArchiveAndRebuilds covers both schema shapes this
// build has ever recorded (1 and 2): sync over an archive stamped at either
// version parks the old file byte-for-byte and rebuilds a fresh one at
// SchemaVersion via a full import, not a merge with the parked data.
func TestSyncParksOlderSchemaArchiveAndRebuilds(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprintf("v%d", version), func(t *testing.T) {
			f := newFixture(t, false)
			defer f.close()
			archivePath := filepath.Join(t.TempDir(), "notes.db")
			seeded := seedStaleArchive(t, archivePath, version)

			c := New()
			c.syncStorePath = f.path()
			req := testRequest(t, archivePath, output.JSON, nil, true)
			report, err := c.Sync(context.Background(), req)
			closeStore(t, req)
			if err != nil {
				t.Fatal(err)
			}
			if report.Added == 0 {
				t.Fatal("sync over stale archive added 0 versions, want the live fixture imported")
			}

			// The parked file exists, at the expected name, with the exact
			// original bytes -- parking is a pure rename, never a rewrite.
			parkedPath := fmt.Sprintf("%s.v%d", archivePath, version)
			parked, err := os.ReadFile(parkedPath)
			if err != nil {
				t.Fatalf("parked file %s: %v", parkedPath, err)
			}
			if !bytes.Equal(parked, seeded) {
				t.Fatalf("parked file bytes changed: %d bytes, want %d bytes matching the original seed", len(parked), len(seeded))
			}

			// The version peek must not have littered WAL sidecars onto the
			// file it was inspecting: parking renames exactly what the old
			// archive left behind, nothing the peek created.
			for _, sibling := range []string{parkedPath + "-wal", parkedPath + "-shm"} {
				if _, err := os.Stat(sibling); !os.IsNotExist(err) {
					t.Fatalf("parked sidecar %s exists, want none: the schema peek must leave no -wal/-shm behind", sibling)
				}
			}

			// The fresh archive at the normal path is at SchemaVersion and
			// holds only the live fixture's data -- the stale seed note is
			// gone, proving this was a full import, not a merge.
			readReq := testRequest(t, archivePath, output.JSON, nil, false)
			fresh, err := archive.UseExisting(context.Background(), readReq.Store, archivePath)
			if err != nil {
				closeStore(t, readReq)
				t.Fatal(err)
			}
			status, err := fresh.Status(context.Background())
			if err != nil {
				closeStore(t, readReq)
				t.Fatal(err)
			}
			if status.SchemaVersion != archive.SchemaVersion {
				t.Fatalf("rebuilt schema version = %d, want %d", status.SchemaVersion, archive.SchemaVersion)
			}
			if _, err := fresh.ResolveNote(context.Background(), "note-stale"); err == nil {
				closeStore(t, readReq)
				t.Fatal("stale seed note resolved in the rebuilt archive, want a full import with no trace of it")
			}
			closeStore(t, readReq)

			// list and search both work against the rebuilt archive.
			listReq := testRequest(t, archivePath, output.JSON, nil, false)
			if err := c.runList(context.Background(), listReq); err != nil {
				closeStore(t, listReq)
				t.Fatalf("list after park+rebuild: %v", err)
			}
			closeStore(t, listReq)

			searchReq := testRequest(t, archivePath, output.JSON, nil, false)
			search, err := c.Search(context.Background(), searchReq, trawlkit.Query{Text: "initial", Limit: 20})
			closeStore(t, searchReq)
			if err != nil {
				t.Fatalf("search after park+rebuild: %v", err)
			}
			if len(search.Results) == 0 {
				t.Fatal("search after park+rebuild found nothing, want the live fixture's note")
			}
		})
	}
}

// TestReadVerbOnOlderSchemaArchiveErrorsWithoutTouchingIt covers a read verb
// (list) against an old-schema archive: it must fail with a clear, truthful
// error naming what sync will do, and must never touch the file -- only sync
// parks and rebuilds.
func TestReadVerbOnOlderSchemaArchiveErrorsWithoutTouchingIt(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	seeded := seedStaleArchive(t, archivePath, 1)

	c := New()
	req := testRequest(t, archivePath, output.JSON, nil, false)
	err := c.runList(context.Background(), req)
	closeStore(t, req)
	if err == nil {
		t.Fatal("list on an old-schema archive succeeded, want a schema-outdated error")
	}
	if !errors.Is(err, archive.ErrSchemaOutdated) {
		t.Fatalf("list error = %v, want it to wrap archive.ErrSchemaOutdated", err)
	}
	if body, ok := err.(interface{ ErrorBody() output.ErrorBody }); ok {
		if got := body.ErrorBody().Message; got == "" {
			t.Fatal("error body message is empty, want a one-line truthful message")
		}
	}
	if !bytes.Contains([]byte(err.Error()), []byte("park")) || !bytes.Contains([]byte(err.Error()), []byte("rebuild")) {
		t.Fatalf("error text = %q, want it to say sync will park and rebuild", err.Error())
	}

	after, readErr := os.ReadFile(archivePath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(after, seeded) {
		t.Fatal("archive bytes changed after a failed read verb, want it untouched")
	}
	if _, statErr := os.Stat(archivePath + ".v1"); statErr == nil {
		t.Fatal("a read verb parked the archive, want only sync to ever park it")
	}
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
