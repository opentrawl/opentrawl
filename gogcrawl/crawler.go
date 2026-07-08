package gogcrawl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/whomatch"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

const (
	appID          = "gmail"
	displayName    = "Gmail"
	statusFreshFor = 24 * time.Hour
	minGogVersion  = "0.31.0"
)

type Crawler struct {
	gog            gog.Client
	backupRepoPath string
	syncQuery      string
	syncMax        int
}

var (
	_ crawlkit.Crawler         = (*Crawler)(nil)
	_ crawlkit.Syncer          = (*Crawler)(nil)
	_ crawlkit.Searcher        = (*Crawler)(nil)
	_ crawlkit.WhoMatcher      = (*Crawler)(nil)
	_ crawlkit.Opener          = (*Crawler)(nil)
	_ crawlkit.ContactExporter = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{gog: gog.New(gog.DefaultBinary)}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          appID,
		Surface:     "gmail",
		DisplayName: displayName,
		Description: "Local-first Gmail archive crawler backed by the gog CLI.",
		ShortRefs:   true,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"gmail", "google-contacts", "sqlite", "message-archive", "message-text-search"},
		},
	}
}

func (c *Crawler) Verbs() []crawlkit.Verb {
	return []crawlkit.Verb{
		{
			Name: "sync",
			Flags: func(fs *flag.FlagSet) {
				fs.StringVar(&c.backupRepoPath, "backup-repo", "", "backup repository")
				fs.StringVar(&c.syncQuery, "query", "", "Gmail search query")
				fs.IntVar(&c.syncMax, "max", 0, "maximum Gmail messages")
			},
		},
		{
			Name:  "contacts_export",
			Store: crawlkit.StoreNone,
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(appID, "Archive has not been synced.")
	status.State = "missing"
	status.DatabasePath = req.Paths.Archive
	status.Counts = statusCounts(archive.Status{})
	if req.Store == nil {
		return &status, nil
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive database cannot be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive status cannot be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.ArchivePath
	status.DatabaseBytes = archiveStatus.ArchiveBytes
	status.LastSyncAt = archiveStatus.LastSyncAt
	status.Counts = statusCounts(archiveStatus)
	status.State, status.Summary = statusState(archiveStatus)
	return &status, nil
}

func (c *Crawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	return &crawlkit.Doctor{Checks: []crawlkit.Check{
		c.checkGogBinary(),
		c.checkGogVersion(ctx),
		c.checkGogAuth(ctx),
		checkArchive(ctx, req),
	}}, nil
}

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, query crawlkit.Query) (crawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return crawlkit.SearchResult{}, archiveErr(err)
	}
	opts := archive.SearchOptions{
		Query: strings.TrimSpace(query.Text),
		Limit: query.Limit,
		Who:   strings.Join(strings.Fields(query.Who), " "),
	}
	if !query.After.IsZero() {
		opts.After = &query.After
	}
	if !query.Before.IsZero() {
		opts.Before = &query.Before
	}
	result, err := st.Search(ctx, opts)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	hits := make([]crawlkit.Hit, 0, len(result.Results))
	for _, hit := range result.Results {
		converted, err := searchHit(hit)
		if err != nil {
			return crawlkit.SearchResult{}, err
		}
		hits = append(hits, converted)
	}
	out := crawlkit.SearchResult{
		Results:      hits,
		TotalMatches: int(result.TotalMatches),
		Truncated:    result.Truncated,
	}
	if result.WhoResolved != nil {
		out.WhoResolved = &crawlkit.WhoResolved{
			Who:         result.WhoResolved.Who,
			Identifiers: append([]string(nil), result.WhoResolved.Identifiers...),
		}
	}
	_ = logInfo(req, "search_complete", fmt.Sprintf("returned=%d total=%d", len(result.Results), result.TotalMatches))
	return out, nil
}

func (c *Crawler) Who(ctx context.Context, req *crawlkit.Request, person string) ([]whomatch.Candidate, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(err)
	}
	result, err := st.ResolveWho(ctx, strings.Join(strings.Fields(person), " "))
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(result.Candidates))
	for _, candidate := range result.Candidates {
		out = append(out, whoCandidate(candidate))
	}
	return out, nil
}

func (c *Crawler) ContactExport(ctx context.Context, req *crawlkit.Request) (*control.ContactExport, error) {
	contacts, err := c.exportContacts(ctx)
	if err != nil {
		return nil, commandErr("gog_contacts_failed", "gog could not list Google contacts", "run gog auth list --check --plain, then gog login <email> if auth is invalid", err)
	}
	return &control.ContactExport{Contacts: contacts}, nil
}

func statusCounts(status archive.Status) []control.Count {
	return []control.Count{
		control.NewCount("messages", "messages", status.Messages),
		control.NewCount("senders", "senders", status.Senders),
		control.NewCount("since", "since", status.Since),
	}
}

func statusState(status archive.Status) (string, string) {
	switch {
	case status.Messages == 0:
		return "empty", "Archive is empty."
	case status.LastSyncAt == "":
		return "stale", "Archive has no completed sync."
	}
	lastSync, err := time.Parse(time.RFC3339, status.LastSyncAt)
	if err != nil {
		return "error", "Archive freshness timestamp cannot be read."
	}
	if time.Since(lastSync) > statusFreshFor {
		return "stale", "Needs sync."
	}
	return "ok", "Recently synced."
}

func (c *Crawler) checkGogBinary() crawlkit.Check {
	if _, err := c.gog.LookPath(); err != nil {
		return crawlkit.Check{
			ID:      "gog_binary",
			State:   "fail",
			Message: "gog is not on PATH",
			Remedy:  "install gog and make sure your shell can run gog",
		}
	}
	return crawlkit.Check{ID: "gog_binary", State: "ok"}
}

func (c *Crawler) checkGogVersion(ctx context.Context) crawlkit.Check {
	version, err := c.gog.Version(ctx)
	if err != nil {
		return crawlkit.Check{ID: "gog_version", State: "fail", Message: "gog version cannot be checked", Remedy: "upgrade gogcli"}
	}
	if !versionAtLeast(version, minGogVersion) {
		return crawlkit.Check{ID: "gog_version", State: "fail", Message: fmt.Sprintf("gog version %s is below %s", version, minGogVersion), Remedy: "upgrade gogcli"}
	}
	return crawlkit.Check{ID: "gog_version", State: "ok", Message: version}
}

func (c *Crawler) checkGogAuth(ctx context.Context) crawlkit.Check {
	status, err := c.gog.AuthStatus(ctx)
	if err != nil {
		return crawlkit.Check{ID: "gog_auth", State: "fail", Message: "gog auth check failed", Remedy: "run gog login <email>"}
	}
	if !status.FoundAccount {
		return crawlkit.Check{ID: "gog_auth", State: "fail", Message: "gog has no stored account", Remedy: "run gog login <email>"}
	}
	if !status.Authorized {
		return crawlkit.Check{ID: "gog_auth", State: "fail", Message: "gog has no valid stored account", Remedy: "run gog login <email>"}
	}
	return crawlkit.Check{ID: "gog_auth", State: "ok"}
}

func checkArchive(ctx context.Context, req *crawlkit.Request) crawlkit.Check {
	if req.Store == nil {
		return crawlkit.Check{ID: "archive", State: "fail", Message: "archive database has not been synced", Remedy: "run trawl gmail sync"}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		remedy := "run trawl gmail sync to rebuild the archive"
		if errors.Is(err, archive.ErrSchemaMismatch) {
			remedy = "remove the old archive and run trawl gmail sync"
		}
		return crawlkit.Check{ID: "archive", State: "fail", Message: "archive database cannot be read", Remedy: remedy}
	}
	if _, err := st.Status(ctx); err != nil {
		return crawlkit.Check{ID: "archive", State: "fail", Message: "archive status cannot be read", Remedy: "run trawl gmail sync to rebuild the archive"}
	}
	return crawlkit.Check{ID: "archive", State: "ok"}
}
