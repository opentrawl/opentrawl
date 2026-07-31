package twitter

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	syncv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync/v1"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	_ trawlkit.Trawler      = (*Crawler)(nil)
	_ trawlkit.Syncer       = (*Crawler)(nil)
	_ trawlkit.Searcher     = (*Crawler)(nil)
	_ trawlkit.RecordOpener = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{cfg: Config{MonthlyBudgetUSD: "10"}}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:                           trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName:                "x",
		RegisteredTrawlerAliases:                    []string{"twitter"},
		RegisteredTrawlerDisplayName:                "Twitter (X)",
		TrawlerCommandNamesShownInBareTrawlOverview: []string{"tweets", "bookmarks", "likes", "mentions"},
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "An X archive you import and, when you run update, your posts, likes, bookmarks, mentions and engagement counts from X.",
			LeavesMachine:   "Nothing from your local archive is uploaded. An explicit update requests your account data from X.",
			NetworkRequests: "Only an explicit update requests data from api.x.com. Import, search and other archive commands are local.",
		},
	}
}

func (c *Crawler) LoadTrawlerConfiguration(trawlerConfigurationFilePath trawlkit.TrawlerConfigurationFilePath) error {
	loadedTwitterConfiguration := c.cfg
	if err := config.LoadTOMLFileIfPresent(string(trawlerConfigurationFilePath), &loadedTwitterConfiguration); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := loadedTwitterConfiguration.Validate(); err != nil {
		return err
	}
	c.cfg = loadedTwitterConfiguration
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		c.browseVerb("tweets"),
		c.browseVerb("bookmarks"),
		c.browseVerb("likes"),
		c.browseVerb("mentions"),
		{
			TrawlerCommandName:            "stats",
			TrawlerCommandHelpDescription: "Your top tweets by likes, retweets or replies",
			RegisterTrawlerCommandFlags:   c.statsFlags,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runStats(req.TrawlerCommandPositionalArguments)
			},
		},
		{
			TrawlerCommandName:            "spend",
			TrawlerCommandHelpDescription: "Monthly X API spend",
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runSpend(req.TrawlerCommandPositionalArguments)
			},
		},
		{
			TrawlerCommandName:                    "import archive",
			TrawlerCommandHelpDescription:         "Import tweets.js and like.js from an X archive dump",
			TrawlerCommandPositionalArgumentNames: []string{"PATH"},
			TrawlerCommandChangesArchive:          true,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runImportArchive(req.TrawlerCommandPositionalArguments)
			},
		},
	}
}

func (c *Crawler) browseVerb(name string) trawlkit.TrawlerCommand {
	command := browseCommands[name]
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:            name,
		TrawlerCommandHelpDescription: command.title,
		RegisterTrawlerCommandFlags:   c.browseFlags,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
			return c.handler(ctx, req).runBrowse(command, req.TrawlerCommandPositionalArguments)
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*statusv1.TrawlerStatusResponse, error) {
	status := &statusv1.TrawlerArchiveStatus{}
	response := &statusv1.TrawlerStatusResponse{TrawlerArchiveStatus: status}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerCommandLog)
	if err != nil {
		return response, nil
	}
	defer func() { _ = archiveStore.Close() }()
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return response, nil
	}
	status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "authored", ArchiveContentKindDisplayName: "authored", ArchiveContentCount: uint64(archiveStatus.Authored)},
		{ArchiveContentKindName: "bookmarks", ArchiveContentKindDisplayName: "bookmarks", ArchiveContentCount: uint64(archiveStatus.Bookmarks)},
		{ArchiveContentKindName: "likes_seen", ArchiveContentKindDisplayName: "tweets liked", ArchiveContentCount: uint64(archiveStatus.LikesSeen)},
		{ArchiveContentKindName: "replies_to_me", ArchiveContentKindDisplayName: "replies to me", ArchiveContentCount: uint64(archiveStatus.RepliesToMe)},
	}
	lastSuccessfullyCompletedArchiveSyncTime := archiveStatus.LastImportAt
	if archiveStatus.LiveSyncResult == "ok" && archiveStatus.LastLiveSync.After(lastSuccessfullyCompletedArchiveSyncTime) {
		lastSuccessfullyCompletedArchiveSyncTime = archiveStatus.LastLiveSync
	}
	if !lastSuccessfullyCompletedArchiveSyncTime.IsZero() {
		status.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(lastSuccessfullyCompletedArchiveSyncTime)
	}
	if archiveReady(archiveStatus) {
		status.TrawlerArchiveCanAnswerCurrentCommands = true
	}
	return response, nil
}

func (c *Crawler) Sync(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*syncv1.TrawlerArchiveSyncReport, error) {
	return c.handler(ctx, req).runSyncReport()
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	return c.handler(ctx, req).search(ctx, query)
}

func (c *Crawler) OpenRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*openv1.OpenRecord, error) {
	value, err := c.handler(ctx, req).loadOpenPost(localShortReference)
	if err != nil {
		return nil, err
	}
	machine := projectOpenRecord(value)
	values := []string{machine.Tweet.GetTime(), machine.Tweet.GetCountsAsOf()}
	for _, tweet := range machine.Ancestors {
		values = append(values, tweet.GetTime())
	}
	for _, tweet := range machine.Replies {
		values = append(values, tweet.GetTime())
	}
	if err := presentation.ValidateTimestamps(values...); err != nil {
		return nil, err
	}
	data, err := anypb.New(machine)
	if err != nil {
		return nil, err
	}
	record := &openv1.OpenRecord{
		RecordTrawler:            c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(machine.GetRef()),
		TypedOpenedRecord: &openv1.OpenRecord_TrawlerSpecificOpenedRecord{
			TrawlerSpecificOpenedRecord: &openv1.TrawlerSpecificOpenedRecord{
				TypedTrawlerSpecificOpenedRecord:              data,
				TrawlerSpecificOpenedRecordDetailPresentation: projectOpenDetailPresentation(value),
			},
		},
	}
	if err := openrecord.Validate(record); err != nil {
		return nil, err
	}
	return record, nil
}

type runtime struct {
	c          *Crawler
	ctx        context.Context
	req        *trawlkit.TrawlerCommandExecutionRequest
	dbPath     string
	configPath string
	log        *cklog.Run
}

func (c *Crawler) handler(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) *runtime {
	return &runtime{
		ctx:        ctx,
		req:        req,
		dbPath:     req.TrawlerArchivePaths.TrawlerArchivePath,
		configPath: string(req.TrawlerArchivePaths.TrawlerConfigurationPath),
		log:        req.TrawlerCommandLog,
		c:          c,
	}
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Use(r.ctx, r.req.OpenedTrawlerArchiveStore, r.req.TrawlerCommandLog)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.UseExisting(r.ctx, r.req.OpenedTrawlerArchiveStore, r.req.TrawlerCommandLog)
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
