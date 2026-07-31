package telegram

import (
	"context"
	"errors"
	"flag"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

const appID = "telegram"

type Crawler struct {
	cfg    Config
	sync   syncOptions
	search searchOptions

	archiveSourcePathUsedByCurrentSync string

	messages messageOptions
	contacts listOptions
}

type syncOptions struct {
	Path                                                     string
	DialogsLimit                                             int
	MessagesLimit                                            int
	LocalConversationShortReferenceAcceptedBySelectedTrawler string
	FetchMedia                                               bool
	FullHistory                                              bool
}

// Config contains durable Telegram acquisition choices. FullHistory is set
// only after an explicitly requested cloud-history download completes, so
// interrupted first runs remain resumable rather than silently changing the
// behaviour of normal sync.
type Config struct {
	FullHistory bool `toml:"full_history"`
}

type searchOptions struct {
	LocalConversationShortReferenceAcceptedBySelectedTrawler string
	Sender                                                   string
	FromMe                                                   bool
	FromThem                                                 bool
	HasMedia                                                 bool
	Pinned                                                   bool
	Asc                                                      bool
}

type listOptions struct {
	Limit    int
	LimitSet bool
}

type messageOptions struct {
	Who      string
	After    string
	Before   string
	FromMe   bool
	FromThem bool
	HasMedia bool
	Pinned   bool
}

var (
	_ trawlkit.Trawler                                  = (*Crawler)(nil)
	_ trawlkit.Syncer                                   = (*Crawler)(nil)
	_ trawlkit.Searcher                                 = (*Crawler)(nil)
	_ trawlkit.WhoMatcher                               = (*Crawler)(nil)
	_ trawlkit.ConversationLister                       = (*Crawler)(nil)
	_ trawlkit.TrawlerMessageLister                     = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider                   = (*Crawler)(nil)
	_ trawlkit.SuccessfullyCompletedArchiveSyncRecorder = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName: "telegram",
		RegisteredTrawlerDisplayName: "Telegram",
		TrawlerConfiguration:         &c.cfg,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Telegram for macOS's local database and any media already stored on your Mac.",
			LeavesMachine:   "Nothing leaves your Mac during a default update. If you enable full history or request missing media, OpenTrawl asks Telegram for it using your existing Telegram session.",
			NetworkRequests: "Default updates are local. --full-history gets older messages from Telegram. --fetch-media gets missing media from Telegram.",
		},
	}
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			SharedTrawlerOperation:      federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC,
			RegisterTrawlerCommandFlags: c.bindSyncFlags,
			TrawlerCommandHelpListing:   trawlkit.TrawlerCommandHiddenFromHumanHelp,
		},
		{
			SharedTrawlerOperation:      federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			RegisterTrawlerCommandFlags: c.bindSearchFlags,
			TrawlerCommandHelpListing:   trawlkit.TrawlerCommandHiddenFromHumanHelp,
		},
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			TrawlerCommandName:            "folders",
			TrawlerCommandHelpDescription: "List folders",
			TrawlerCommandArchiveAccess:   trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:         c.runFolders,
		},
		{
			SharedTrawlerOperation:                 federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:            c.bindMessagesFlags,
			TrawlerCommandShownInBareTrawlOverview: true,
		},
		{
			SharedTrawlerOperation:                 federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandShownInBareTrawlOverview: true,
		},
		{
			TrawlerCommandName:            "contacts",
			TrawlerCommandHelpDescription: "List archived Telegram contacts.",
			RegisterTrawlerCommandFlags:   c.bindContactsFlags,
			TrawlerCommandHelpListing:     trawlkit.TrawlerCommandHiddenFromHumanHelp,
			TrawlerCommandArchiveAccess:   trawlkit.TrawlerCommandArchiveAccessRequired,
			ExecuteTrawlerCommand:         c.runContacts,
		},
	}
}

func (c *Crawler) handler(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) *runtime {
	return &runtime{
		c:          c,
		ctx:        ctx,
		req:        req,
		dbPath:     req.TrawlerArchivePaths.TrawlerArchivePath,
		configPath: req.TrawlerArchivePaths.TrawlerConfigurationPath,
		log:        req.TrawlerCommandLog,
	}
}

type runtime struct {
	c          *Crawler
	ctx        context.Context
	req        *trawlkit.TrawlerCommandExecutionRequest
	dbPath     string
	configPath string
	log        *cklog.Run
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Use(r.ctx, r.req.OpenedTrawlerArchiveStore, r.req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.UseExisting(r.ctx, r.req.OpenedTrawlerArchiveStore, r.req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) logInfo(event, message string) error {
	if r == nil || r.log == nil {
		return nil
	}
	return r.log.Info(event, message)
}

func (r *runtime) logDebug(event, message string) error {
	if r == nil || r.log == nil {
		return nil
	}
	return r.log.Debug(event, message)
}

func (c *Crawler) bindSyncFlags(fs *flag.FlagSet) {
	c.sync = syncOptions{}
	fs.StringVar(&c.sync.Path, "path", "", "Telegram data directory")
	fs.StringVar(&c.sync.LocalConversationShortReferenceAcceptedBySelectedTrawler, "conversation", "", "Conversation `LINK`")
	fs.BoolVar(&c.sync.FetchMedia, "fetch-media", false, "Download missing media from Telegram")
	fs.BoolVar(&c.sync.FullHistory, "full-history", false, "Download older Telegram messages; attachments are separate")
}

func (c *Crawler) bindSearchFlags(fs *flag.FlagSet) {
	c.search = searchOptions{}
	fs.StringVar(&c.search.LocalConversationShortReferenceAcceptedBySelectedTrawler, "conversation", "", "Conversation `LINK`")
	fs.StringVar(&c.search.Sender, "sender", "", "Show only messages from `PERSON`")
	fs.BoolVar(&c.search.FromMe, "from-me", false, "Show only messages sent by you")
	fs.BoolVar(&c.search.FromThem, "from-them", false, "Show only messages sent by other people")
	fs.BoolVar(&c.search.HasMedia, "has-media", false, "Show only messages with media")
	fs.BoolVar(&c.search.Pinned, "pinned", false, "Show only pinned messages")
	fs.BoolVar(&c.search.Asc, "asc", false, "Show oldest messages first")
}

func (c *Crawler) bindContactsFlags(fs *flag.FlagSet) {
	c.contacts = listOptions{Limit: 100}
	fs.Var(trackedInt{value: &c.contacts.Limit, seen: &c.contacts.LimitSet}, "limit", "maximum contacts")
}

func (c *Crawler) bindMessagesFlags(fs *flag.FlagSet) {
	c.messages = messageOptions{}
	fs.StringVar(&c.messages.Who, "who", "", "Show only messages that involve `PERSON`")
	fs.StringVar(&c.messages.After, "after", "", "Messages on or after `DATE`")
	fs.StringVar(&c.messages.Before, "before", "", "Messages on or before `DATE_OR_TIME`")
	fs.BoolVar(&c.messages.FromMe, "from-me", false, "Show only messages sent by you")
	fs.BoolVar(&c.messages.FromThem, "from-them", false, "Show only messages sent by other people")
	fs.BoolVar(&c.messages.HasMedia, "has-media", false, "Show only messages with media")
	fs.BoolVar(&c.messages.Pinned, "pinned", false, "Show only pinned messages")
}

type trackedInt struct {
	value *int
	seen  *bool
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
		return errors.New("must be a whole number")
	}
	*v.value = n
	if v.seen != nil {
		*v.seen = true
	}
	return nil
}

func normalizeWords(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
