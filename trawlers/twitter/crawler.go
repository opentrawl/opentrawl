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
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"github.com/opentrawl/opentrawl/twitter/internal/xapi"
	"github.com/opentrawl/opentrawl/twitter/internal/xweb"
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

	connect    connectOptions
	updateOpts updateFlags
	dmOpts     dmMessageOptions
}

var (
	_ trawlkit.Trawler                        = (*Crawler)(nil)
	_ trawlkit.Updater                        = (*Crawler)(nil)
	_ trawlkit.Searcher                       = (*Crawler)(nil)
	_ trawlkit.CanonicalRecordOpener          = (*Crawler)(nil)
	_ trawlkit.ConversationLister             = (*Crawler)(nil)
	_ trawlkit.TrawlerMessageLister           = (*Crawler)(nil)
	_ trawlkit.WhoMatcher                     = (*Crawler)(nil)
	_ trawlkit.PersonMessageCounter           = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider         = (*Crawler)(nil)
	_ trawlkit.ShortReferenceSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{cfg: Config{MonthlyBudgetUSD: "10"}}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName: "twitter",
		RegisteredTrawlerAliases:     []string{"x"},
		RegisteredTrawlerDisplayName: "Twitter",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "An X archive you import and, when connected, your posts, likes, bookmarks, mentions and direct messages from X (read-only via your web session or official X API credentials).",
			LeavesMachine:   "Nothing from your local archive is uploaded. Only explicit connect and update operations request your account data from X.",
			NetworkRequests: "Web-session mode requests x.com (and abs.twimg.com bundles for query ID discovery); official API mode requests api.x.com. Import, search, browse and open are strictly local.",
		},
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:              "bubble.left.and.bubble.right.fill",
			AccentColor:             "#111111",
			ArtworkBundleIdentifier: "com.atebits.Tweetie2",
		},
		SourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_NONE,
	}
}

func (c *Crawler) ListArchiveRecordReferencesForShortReferences(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
) ([]trawlkit.ShortReferenceAssignmentCandidate, bool, error) {
	var candidates []trawlkit.ShortReferenceAssignmentCandidate
	err := c.handler(ctx, req).withReadOnlyStore(func(st *store.Store) error {
		var listErr error
		candidates, listErr = st.ListShortReferenceCandidates(ctx)
		return listErr
	})
	if err != nil {
		return nil, false, err
	}
	return candidates, false, nil
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
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			RegisterTrawlerCommandFlags:      c.updateFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:      c.dmMessageFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		c.browseVerb("bookmarks", trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp),
		c.browseVerb("tweets", trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp),
		c.browseVerb("likes", trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp),
		c.browseVerb("mentions", trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp),
		{
			TrawlerCommandName:               "connect",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandHelpDescription:    "Connect an X web session (auth_token and ct0) for read-only syncing",
			RegisterTrawlerCommandFlags:      c.connectFlags,
			TrawlerCommandChangesArchive:     true,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runConnect()
			},
		},
		{
			TrawlerCommandName:               "disconnect",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandHelpDescription:    "Remove stored X web session cookies; keep the archive",
			TrawlerCommandChangesArchive:     true,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runDisconnect()
			},
		},
		{
			TrawlerCommandName:               "stats",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandHelpDescription:    "Your top tweets by likes, retweets or replies",
			RegisterTrawlerCommandFlags:      c.statsFlags,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runStats()
			},
		},
		{
			TrawlerCommandName:               "spend",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandHelpDescription:    "Monthly Twitter API spend",
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runSpend()
			},
		},
		{
			TrawlerCommandName:                    "import archive",
			TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandHelpDescription:         "Import tweets.js and like.js from an X archive dump",
			TrawlerCommandPositionalArgumentNames: []string{"PATH"},
			TrawlerCommandChangesArchive:          true,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return c.handler(ctx, req).runImportArchive(req.TrawlerCommandPositionalArguments[0])
			},
		},
	}
}

func (c *Crawler) browseVerb(name string, placement trawlkit.TrawlerCommandDiscoveryPlacement) trawlkit.TrawlerCommand {
	selectedBrowseCommand := browseCommands[name]
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:               name,
		TrawlerCommandHelpDescription:    selectedBrowseCommand.title,
		TrawlerCommandDiscoveryPlacement: placement,
		RegisterTrawlerCommandFlags:      c.browseFlags,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			return c.handler(ctx, req).runBrowse(selectedBrowseCommand)
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerCommandLog)
	if err != nil {
		return nil, err
	}
	defer func() { _ = archiveStore.Close() }()
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return nil, err
	}
	counts := []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "authored", ArchiveContentKindDisplayName: "authored", ArchiveContentCount: uint64(max(archiveStatus.Authored, 0))},
		{ArchiveContentKindName: "bookmarks", ArchiveContentKindDisplayName: "bookmarks", ArchiveContentCount: uint64(max(archiveStatus.Bookmarks, 0))},
		{ArchiveContentKindName: "likes_seen", ArchiveContentKindDisplayName: "likes", ArchiveContentCount: uint64(max(archiveStatus.LikesSeen, 0))},
		{ArchiveContentKindName: "replies_to_me", ArchiveContentKindDisplayName: "mentions", ArchiveContentCount: uint64(max(archiveStatus.RepliesToMe, 0))},
	}
	if archiveStatus.DMMessages > 0 || archiveStatus.DMConversations > 0 {
		counts = append(counts,
			&status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
				ArchiveContentKindName:        "dm_messages",
				ArchiveContentKindDisplayName: "DMs",
				ArchiveContentCount:           uint64(max(archiveStatus.DMMessages, 0)),
			},
			&status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
				ArchiveContentKindName:        "dm_conversations",
				ArchiveContentKindDisplayName: "DM threads",
				ArchiveContentCount:           uint64(max(archiveStatus.DMConversations, 0)),
			},
		)
	}
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = counts

	lastUpdated := archiveStatus.LastImportAt
	if archiveStatus.LastLiveUpdate.After(lastUpdated) {
		lastUpdated = archiveStatus.LastLiveUpdate
	}
	if !lastUpdated.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(lastUpdated)
	}
	newest := archiveStatus.NewestTweet
	if archiveStatus.NewestDM.After(newest) {
		newest = archiveStatus.NewestDM
	}
	if !newest.IsZero() {
		trawlerArchiveStatus.NewestArchivedRecordTime = timestamppb.New(newest)
	}

	cfg, _ := loadBirdConfig(string(req.TrawlerArchivePaths.TrawlerConfigurationPath))
	source, sourceErr := cfg.ResolveSyncSource()
	works := false
	if sourceErr == nil {
		switch source {
		case TwitterSyncSourceWebSession:
			works = xweb.WebSessionPresent(cfg.SessionPath()) && archiveStatus.WebSessionValid
		case TwitterSyncSourceOfficialAPI:
			works = xapi.CredentialsPresent(cfg.OfficialAPICredentialsPath())
		case TwitterSyncSourceUnset:
			works = archiveStatus.Tweets > 0 || archiveStatus.DMMessages > 0
		}
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = works
	return response, nil
}

func (c *Crawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	return c.handler(ctx, req).runUpdateReport()
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	return c.handler(ctx, req).search(ctx, query)
}

func (c *Crawler) OpenCanonicalRecord(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (*open.OpenRecord, error) {
	record, err := c.handler(ctx, req).loadOpenRecord(canonicalRecordReference)
	if err != nil {
		return nil, err
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
	fs.StringVar(&c.browseAfter, "after", "", render.OnOrAfterDateOptionDescription)
	fs.StringVar(&c.browseBefore, "before", "", render.OnOrBeforeDateOptionDescription)
}

func (c *Crawler) statsFlags(fs *flag.FlagSet) {
	c.statsWindow = "30d"
	c.statsBy = "likes"
	c.statsLimit = defaultStatsLimit
	c.statsLimitSet = false
	fs.StringVar(&c.statsWindow, "window", "30d", "look back over this duration (for example 7d, 30d, 90d, 365d, 2y, or all)")
	fs.StringVar(&c.statsBy, "by", "likes", "sort by likes, retweets, or replies")
	fs.Var(trackedInt{value: &c.statsLimit, set: &c.statsLimitSet}, "limit", "maximum results")
}

func (c *Crawler) connectFlags(fs *flag.FlagSet) {
	c.connect = connectOptions{}
	fs.StringVar(&c.connect.authToken, "auth-token", "", "X auth_token session cookie")
	fs.StringVar(&c.connect.ct0, "ct0", "", "X ct0 CSRF session cookie")
	fs.StringVar(&c.connect.filePath, "file", "", "Read AUTH_TOKEN and CT0 from a local file")
	fs.BoolVar(&c.connect.fromEnv, "from-env", false, "Read AUTH_TOKEN and CT0 from environment variables")
}

func (c *Crawler) updateFlags(fs *flag.FlagSet) {
	c.updateOpts = updateFlags{}
	fs.BoolVar(&c.updateOpts.force, "force", false, "Bypass the 15-minute web-session sync cooldown")
	fs.BoolVar(&c.updateOpts.full, "full", false, "Sweep deeper bookmark, timeline and DM history instead of stopping at known records")
	fs.IntVar(&c.updateOpts.maxPages, "max-pages", 0, "Maximum pages to fetch per endpoint during this update")
}

func (c *Crawler) dmMessageFlags(fs *flag.FlagSet) {
	c.dmOpts = dmMessageOptions{}
	fs.StringVar(&c.dmOpts.who, "who", "", "Show only direct messages involving `PERSON`")
	fs.BoolVar(&c.dmOpts.fromMe, "from-me", false, "Show only direct messages sent by you")
	fs.BoolVar(&c.dmOpts.fromThem, "from-them", false, "Show only direct messages sent by other people")
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
