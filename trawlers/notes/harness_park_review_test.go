package notes

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// reviewRebuildShortRefs replicates trawlkit's post-sync harness step
// (execute.go assignSourceShortRefs) exactly: gather the crawler's short-ref
// records from req.Store, then assign aliases into req.Store. The real sync
// verb runs this immediately after Sync returns, against the SAME req.Store.
func reviewRebuildShortRefs(ctx context.Context, c *Crawler, req *trawlkit.Request) error {
	records, err := c.ShortRefRecords(ctx, req)
	if err != nil {
		return err
	}
	_, err = req.AssignShortRefs(ctx, records)
	return err
}

// TestHarnessPostSyncShortRefRebuildAfterPark is the permanent regression
// guard for what was ATTACK 1 in review: over an old-schema archive, the CLI
// harness used to open req.Store, run Sync (which closed that connection and
// swapped in a private replacement while parking the old file), then run the
// short-ref rebuild against the now-closed original req.Store -- "sql:
// database is closed", the one sync that performs the upgrade exiting
// non-zero with no short-ref index.
//
// The fix moved the peek-and-park step (archive.PrepareArchive) before the
// harness ever opens req.Store -- see trawlkit/execute.go runInProcess and
// testRequest in this package, which mirrors that ordering exactly. By the
// time req.Store opens here, either nothing needed parking or the old file
// is already parked and req.Store opens fresh; either way archive.Use only
// ever borrows req.Store and never closes it. This test proves req.Store
// stays open and valid all the way through Sync and the harness's own
// post-sync short-ref rebuild against that same connection.
func TestHarnessPostSyncShortRefRebuildAfterPark(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	defer f.close()
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	seedStaleArchive(t, archivePath, 1)

	c := New()
	c.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	defer closeStore(t, req)

	if _, err := c.Sync(ctx, req); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	// This is exactly what the harness does next, in-process, same req.Store.
	if err := reviewRebuildShortRefs(ctx, c, req); err != nil {
		t.Fatalf("post-sync short-ref rebuild failed (ATTACK 1 confirmed): %v", err)
	}
}
