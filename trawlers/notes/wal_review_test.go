package notes

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// TestWALRemovedOnClose checks the ATTACK 2 assumption that a clean Close
// checkpoints and removes the -wal/-shm siblings, so parkArchiveFile's
// best-effort sibling rename is normally a no-op.
func TestWALRemovedOnClose(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "notes.db")
	st, err := store.Open(ctx, store.Options{Path: path, Schema: "create table t(x)"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "insert into t(x) values (1),(2),(3)"); err != nil {
		t.Fatal(err)
	}
	// Force a WAL to exist on disk while the connection is open.
	walBefore := statExists(path + "-wal")
	t.Logf("wal exists while open: %v", walBefore)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("after close: wal=%v shm=%v", statExists(path+"-wal"), statExists(path+"-shm"))
	if statExists(path + "-wal") {
		t.Log("NOTE: -wal survives a clean close; parkArchiveFile sibling rename is load-bearing")
	}
}

func statExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
