package notes

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// TestLiveSyncDecodesRealTable copies this machine's live NoteStore.sqlite
// (read-only source) into a scratch dir, runs a real sync into a scratch
// archive, and proves that at least one embedded table decodes to a real
// markdown pipe table from real captured bytes — not a synthetic fixture and
// not the "not captured" fallback. It skips cleanly when the store is absent.
func TestLiveSyncDecodesRealTable(t *testing.T) {
	src := liveStorePath(t)
	storeCopy := copyLiveStore(t, src)

	archivePath := filepath.Join(t.TempDir(), "archive", "notes.db")
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o700); err != nil {
		t.Fatal(err)
	}

	c := New()
	c.syncStorePath = storeCopy
	req := testRequest(t, archivePath, output.JSON, nil, true)
	if _, err := c.Sync(context.Background(), req); err != nil {
		closeStore(t, req)
		t.Fatal(err)
	}
	closeStore(t, req)

	readReq := testRequest(t, archivePath, output.JSON, nil, false)
	defer closeStore(t, readReq)
	db := readReq.Store.DB()

	var captured int
	if err := db.QueryRowContext(context.Background(),
		`select count(*) from note_table_data`).Scan(&captured); err != nil {
		t.Fatal(err)
	}
	t.Logf("captured table CRDT blobs: %d", captured)
	if captured == 0 {
		t.Fatal("no table CRDT blobs captured from a store known to contain tables")
	}

	rows, err := db.QueryContext(context.Background(), `
select note_id, text
from note_versions
where text_status = 'decoded' and text like '%| --- |%'
order by note_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()

	found := false
	for rows.Next() {
		var noteID, text string
		if err := rows.Scan(&noteID, &text); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(text, tableNotCapturedMarker) {
			continue
		}
		if !found {
			t.Logf("\n===== real decoded table in note %s =====\n%s\n", noteID, sampleTable(text))
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("no real pipe table decoded from the live store")
	}
}

// tableNotCapturedMarker mirrors projection.tableNotCaptured (unexported); this
// test only needs the literal to assert it is absent from freshly captured
// tables.
const tableNotCapturedMarker = "[table: not yet captured"

func sampleTable(text string) string {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "| --- |") {
			start = i - 1
			break
		}
	}
	if start < 0 {
		return text
	}
	end := start + 8
	if end > len(lines) {
		end = len(lines)
	}
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:end], "\n")
}

func liveStorePath(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	path := filepath.Join(home, "Library", "Group Containers", "group.com.apple.notes", "NoteStore.sqlite")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("live NoteStore.sqlite not present at %s", path)
	}
	return path
}

// copyLiveStore copies NoteStore.sqlite and its -wal/-shm siblings into a
// scratch dir so the sync never opens the real store for writing.
func copyLiveStore(t *testing.T, src string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "NoteStore.sqlite")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := copyFileIfPresent(src+suffix, dst+suffix); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func copyFileIfPresent(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
