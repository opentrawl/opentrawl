package notes

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const staleAfter = 24 * time.Hour

type Crawler struct {
	syncStorePath string
	syncLabel     string
	storeLabel    string
	atTimeRaw     string
}

var (
	_ trawlkit.Crawler  = (*Crawler)(nil)
	_ trawlkit.Syncer   = (*Crawler)(nil)
	_ trawlkit.Searcher = (*Crawler)(nil)
	_ trawlkit.Opener   = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          archive.AppID,
		Surface:     archive.AppID,
		DisplayName: archive.DisplayName,
		Description: "Apple Notes archive",
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"apple-notes", "sqlite", "note-body-versions", "local-search"},
		},
	}
}

func (c *Crawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		{Name: "sync", Flags: c.syncFlags},
		{
			Name:  "list",
			Help:  "List notes newest first, or one folder",
			Args:  []string{"[FOLDER]"},
			Store: trawlkit.StoreRequired,
			Run: func(ctx context.Context, req *trawlkit.Request) error {
				return c.runList(ctx, req)
			},
		},
		{
			Name:    "sync-store",
			Help:    "Sync one copied or mounted NoteStore.sqlite",
			Args:    []string{"PATH"},
			Flags:   c.syncStoreFlags,
			Mutates: true,
			Run: func(ctx context.Context, req *trawlkit.Request) error {
				return c.runSyncStore(ctx, req)
			},
		},
		{
			Name:      "versions",
			Help:      "List recovered versions for one note",
			Args:      []string{"NOTE"},
			Secondary: true,
			Store:     trawlkit.StoreRequired,
			Run: func(ctx context.Context, req *trawlkit.Request) error {
				return c.runVersions(ctx, req)
			},
		},
		{
			Name:      "at-time",
			Help:      "Open the recovered version at or before a time",
			Args:      []string{"NOTE"},
			Secondary: true,
			Flags:     c.atTimeFlags,
			Store:     trawlkit.StoreRequired,
			Run: func(ctx context.Context, req *trawlkit.Request) error {
				return c.runAtTime(ctx, req)
			},
		},
	}
}

func (c *Crawler) syncFlags(fs *flag.FlagSet) {
	c.syncStorePath = ""
	c.syncLabel = ""
	fs.StringVar(&c.syncStorePath, "store", "", "copied NoteStore.sqlite path")
	fs.StringVar(&c.syncLabel, "label", "", "source label")
}

func (c *Crawler) syncStoreFlags(fs *flag.FlagSet) {
	c.storeLabel = ""
	fs.StringVar(&c.storeLabel, "label", "", "source label")
}

func (c *Crawler) atTimeFlags(fs *flag.FlagSet) {
	c.atTimeRaw = ""
	fs.StringVar(&c.atTimeRaw, "time", "", "RFC3339 time")
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(archive.AppID, "Not synced yet.")
	status.State = "missing"
	status.DatabasePath = req.Paths.Archive
	status.ConfigPath = req.Paths.Config
	if req.Store == nil {
		return &status, nil
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be inspected."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.ArchivePath
	status.DatabaseBytes = archiveStatus.ArchiveBytes
	status.LastSyncAt = archiveStatus.LastSyncAt
	status.Counts = []control.Count{
		control.NewCount("notes", "notes", archiveStatus.Notes),
		control.NewCount("versions", "versions", archiveStatus.Versions),
		control.NewCount("decoded_versions", "decoded versions", archiveStatus.DecodedVersions),
	}
	status.Databases = []control.Database{control.SQLiteDatabase("archive", "Notes archive", "archive", archiveStatus.ArchivePath, true, status.Counts)}
	status.State, status.Summary = statusState(archiveStatus)
	return &status, nil
}

func (c *Crawler) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	sourcePath, sourceErr := notesdb.DefaultStorePath()
	return &trawlkit.Doctor{Checks: []trawlkit.Check{
		checkSourceStore(sourcePath, sourceErr),
		checkArchive(ctx, req),
	}}, nil
}

func statusState(status archive.Status) (string, string) {
	if status.Versions == 0 {
		return "empty", "Archive has no notes yet."
	}
	lastSync, err := time.Parse(time.RFC3339Nano, status.LastSyncAt)
	if err != nil {
		return "error", "Archive freshness timestamp cannot be read."
	}
	if time.Since(lastSync) > staleAfter {
		return "stale", "Archive is stale."
	}
	return "ok", "Archive is fresh."
}

func checkSourceStore(path string, pathErr error) trawlkit.Check {
	if pathErr != nil {
		return trawlkit.Check{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot locate the Apple Notes database",
			Remedy:  "set HOME, then run trawl notes sync; or run trawl notes sync --store PATH",
		}
	}
	if _, err := os.Stat(path); err != nil {
		return trawlkit.Check{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot read the Apple Notes database",
			Remedy:  "grant Full Disk Access, then run trawl notes sync; or run trawl notes sync --store PATH",
		}
	}
	return trawlkit.Check{ID: "source_store", State: "ok"}
}

func checkArchive(ctx context.Context, req *trawlkit.Request) trawlkit.Check {
	if req.Store == nil {
		return trawlkit.Check{ID: "archive", State: "fail", Message: "archive has not been synced", Remedy: "run trawl notes sync"}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.Check{ID: "archive", State: "fail", Message: "archive database cannot be read", Remedy: "run trawl notes sync"}
	}
	if _, err := st.Status(ctx); err != nil {
		return trawlkit.Check{ID: "archive", State: "fail", Message: "archive status cannot be read", Remedy: "run trawl notes sync"}
	}
	return trawlkit.Check{ID: "archive", State: "ok"}
}

func commandErr(code, message, remedy string, err error) error {
	return crawlerError{code: code, message: message, remedy: remedy, err: err}
}

type crawlerError struct {
	code    string
	message string
	remedy  string
	err     error
}

func (e crawlerError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return e.message
}

func (e crawlerError) Unwrap() error {
	return e.err
}

func (e crawlerError) ErrorBody() output.ErrorBody {
	return output.ErrorBody{Code: e.code, Message: e.message, Remedy: e.remedy}
}

func usageError(message string) error {
	return output.UsageError{Err: fmt.Errorf("%s", message)}
}
