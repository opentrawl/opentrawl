package birdcrawl

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	cklog "github.com/openclaw/crawlkit/log"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

const appID = "twitter"

type Crawler struct {
	cfg Config

	browseLimit    int
	browseLimitSet bool
	browseAfter    string
	browseBefore   string

	statsWindow   string
	statsBy       string
	statsLimit    int
	statsLimitSet bool
}

var (
	_ crawlkit.Crawler  = (*Crawler)(nil)
	_ crawlkit.Syncer   = (*Crawler)(nil)
	_ crawlkit.Searcher = (*Crawler)(nil)
	_ crawlkit.Opener   = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{cfg: Config{MonthlyBudgetUSD: "10"}}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          appID,
		Surface:     "twitter",
		Aliases:     []string{"x"},
		DisplayName: "X",
		Description: "Local-first X archive crawler.",
		Config:      &c.cfg,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"x-archive-dump", "sqlite"},
		},
	}
}

func (c *Crawler) Verbs() []crawlkit.Verb {
	return []crawlkit.Verb{
		c.browseVerb("tweets"),
		c.browseVerb("bookmarks"),
		c.browseVerb("likes"),
		c.browseVerb("mentions"),
		{
			Name:  "stats",
			Help:  "Your top tweets by likes, retweets or replies",
			Flags: c.statsFlags,
			Run: func(ctx context.Context, req *crawlkit.Request) error {
				return c.handler(ctx, req).runStats(req.Args)
			},
		},
		{
			Name: "spend",
			Help: "Monthly X API spend",
			Run: func(ctx context.Context, req *crawlkit.Request) error {
				return c.handler(ctx, req).runSpend(req.Args)
			},
		},
		{
			Name:    "import archive",
			Help:    "Import tweets.js and like.js from an X archive dump",
			Args:    []string{"PATH"},
			Mutates: true,
			Run: func(ctx context.Context, req *crawlkit.Request) error {
				return c.handler(ctx, req).runImportArchive(req.Args)
			},
		},
	}
}

func (c *Crawler) browseVerb(name string) crawlkit.Verb {
	command := browseCommands[name]
	return crawlkit.Verb{
		Name:  name,
		Help:  command.title,
		Flags: c.browseFlags,
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			return c.handler(ctx, req).runBrowse(command, req.Args)
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	return c.handler(ctx, req).status(ctx)
}

func (c *Crawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	return c.handler(ctx, req).doctor(ctx)
}

func (c *Crawler) Sync(ctx context.Context, req *crawlkit.Request) (*crawlkit.SyncReport, error) {
	return c.handler(ctx, req).runSyncReport()
}

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, query crawlkit.Query) (crawlkit.SearchResult, error) {
	return c.handler(ctx, req).search(ctx, query)
}

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	return c.handler(ctx, req).runOpen([]string{ref})
}

type runtime struct {
	c          *Crawler
	ctx        context.Context
	req        *crawlkit.Request
	stdout     io.Writer
	json       bool
	dbPath     string
	configPath string
	log        *cklog.Run
}

func (c *Crawler) handler(ctx context.Context, req *crawlkit.Request) *runtime {
	return &runtime{
		ctx:        ctx,
		req:        req,
		stdout:     req.Out,
		json:       req.Format == ckoutput.JSON,
		dbPath:     req.Paths.Archive,
		configPath: req.Paths.Config,
		log:        req.Log,
		c:          c,
	}
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Use(r.ctx, r.req.Store, r.req.Log)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.UseExisting(r.ctx, r.req.Store, r.req.Log)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (c *Crawler) browseFlags(fs *flag.FlagSet) {
	c.browseLimit = defaultSearchLimit
	c.browseLimitSet = false
	c.browseAfter = ""
	c.browseBefore = ""
	fs.Var(trackedInt{value: &c.browseLimit, set: &c.browseLimitSet}, "limit", "maximum results")
	fs.StringVar(&c.browseAfter, "after", "", "only results at or after this date")
	fs.StringVar(&c.browseBefore, "before", "", "only results before this date")
}

func (c *Crawler) statsFlags(fs *flag.FlagSet) {
	c.statsWindow = "30d"
	c.statsBy = "likes"
	c.statsLimit = defaultStatsLimit
	c.statsLimitSet = false
	fs.StringVar(&c.statsWindow, "window", "30d", "look back over this duration")
	fs.StringVar(&c.statsBy, "by", "likes", "sort by likes, retweets, or replies")
	fs.Var(trackedInt{value: &c.statsLimit, set: &c.statsLimitSet}, "limit", "maximum results")
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

type trackedInt struct {
	value *int
	set   *bool
}

func (v trackedInt) String() string {
	if v.value == nil {
		return "0"
	}
	return strconv.Itoa(*v.value)
}

func (v trackedInt) Set(raw string) error {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("must be a number: %s", raw)
	}
	*v.value = n
	if v.set != nil {
		*v.set = true
	}
	return nil
}
