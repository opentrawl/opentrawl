package telegram

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramnetwork"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
)

const appID = "telegram"

type Crawler struct {
	cfg    TelegramCrawlerConfiguration
	update updateOptions
	search searchOptions

	archiveSourcePathUsedByCurrentUpdate string

	messages messageOptions
	connect  telegramConnectFlags
}

type updateOptions struct {
	Path                                                     string
	DialogsLimit                                             int
	MessagesLimit                                            int
	LocalConversationShortReferenceAcceptedBySelectedTrawler string
	FetchMedia                                               bool
	FullHistory                                              bool
}

// TelegramCrawlerConfiguration contains durable Telegram acquisition choices. FullHistory is set
// only after an explicitly requested cloud-history download completes, so
// interrupted first runs remain resumable rather than silently changing the
// behaviour of normal update.
type TelegramCrawlerConfiguration struct {
	FullHistory bool   `toml:"full_history"`
	APIID       int    `toml:"api_id"`
	APIHash     string `toml:"api_hash"`
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

type messageOptions struct {
	Who      string
	FromMe   bool
	FromThem bool
	HasMedia bool
	Pinned   bool
}

var (
	_ trawlkit.Trawler                                    = (*Crawler)(nil)
	_ trawlkit.Updater                                    = (*Crawler)(nil)
	_ trawlkit.Searcher                                   = (*Crawler)(nil)
	_ trawlkit.WhoMatcher                                 = (*Crawler)(nil)
	_ trawlkit.ConversationLister                         = (*Crawler)(nil)
	_ trawlkit.ConversationRecordReferenceRecognizer      = (*Crawler)(nil)
	_ trawlkit.TrawlerMessageLister                       = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider                     = (*Crawler)(nil)
	_ trawlkit.SuccessfullyCompletedArchiveUpdateRecorder = (*Crawler)(nil)
	_ trawlkit.CanonicalRecordOpener                      = (*Crawler)(nil)
	_ trawlkit.ArchiveIndexChangeExporter                 = (*Crawler)(nil)
	_ trawlkit.PluginCapabilityDeclarator                 = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (*Crawler) DeclaredPluginCapabilities() *plugincontract.PluginDeclaredCapabilities {
	return &plugincontract.PluginDeclaredCapabilities{
		DeclaredNetworkHosts:             telegramnetwork.NetworkHosts(),
		DeclaredReadPaths:                []string{"~/Library/Group Containers/6N38VWS5BX.ru.keepcoder.Telegram"},
		SupportsArchiveIndexChangeExport: true,
	}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName: "telegram",
		RegisteredTrawlerDisplayName: "Telegram",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Telegram cloud chats when connected directly, or Telegram's local database and media on macOS.",
			LeavesMachine:   "Direct sign-in and cloud history communicate with Telegram. Archive searches run locally.",
			NetworkRequests: "Direct updates download cloud messages from Telegram. Desktop updates access Telegram only with --full-history or --fetch-media.",
		},
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:              "paperplane.fill",
			AccentColor:             "#229ED9",
			BundleIdentifier:        "ru.keepcoder.Telegram",
			ArtworkBundleIdentifier: "ru.keepcoder.Telegram",
		},
		SourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_SOURCE_APP_RUNNING,
	}
}

func (c *Crawler) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	return st.ExportArchiveIndexChanges(ctx, sinceOpaqueCursor, emit)
}

func (c *Crawler) LoadTrawlerConfiguration(trawlerConfigurationFilePath trawlkit.TrawlerConfigurationFilePath) error {
	loadedTelegramConfiguration := c.cfg
	if err := config.LoadTOMLFileIfPresent(string(trawlerConfigurationFilePath), &loadedTelegramConfiguration); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	c.cfg = loadedTelegramConfiguration
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{TrawlerCommandName: "connect", TrawlerCommandHelpDescription: "Connect directly to Telegram without Full Disk Access", RegisterTrawlerCommandFlags: c.bindConnectFlags, TrawlerCommandChangesArchive: true, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessNone, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp, ExecuteTrawlerCommand: c.connectTelegram},
		{TrawlerCommandName: "login-qr", TrawlerCommandHelpDescription: "Show the path to the current Telegram QR image", TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessNone, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp, ExecuteTrawlerCommand: c.telegramLoginQR},
		{TrawlerCommandName: "disconnect", TrawlerCommandHelpDescription: "Revoke the direct Telegram session", TrawlerCommandChangesArchive: true, TrawlerCommandArchiveAccess: trawlkit.TrawlerCommandArchiveAccessNone, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp, ExecuteTrawlerCommand: c.disconnectTelegram},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			RegisterTrawlerCommandFlags:      c.bindUpdateFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			RegisterTrawlerCommandFlags:      c.bindSearchFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:      c.bindMessagesFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
	}
}

func (c *Crawler) handler(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) *runtime {
	return &runtime{
		c:          c,
		ctx:        ctx,
		req:        req,
		dbPath:     req.TrawlerArchivePaths.TrawlerArchivePath,
		configPath: string(req.TrawlerArchivePaths.TrawlerConfigurationPath),
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

func (c *Crawler) bindUpdateFlags(fs *flag.FlagSet) {
	c.update = updateOptions{}
	fs.StringVar(&c.update.Path, "path", "", "Use this Telegram data folder")
	fs.StringVar(&c.update.LocalConversationShortReferenceAcceptedBySelectedTrawler, "conversation", "", "Update only this conversation `LINK`")
	fs.BoolVar(&c.update.FetchMedia, "fetch-media", false, "Download missing media from Telegram")
	fs.BoolVar(&c.update.FullHistory, "full-history", false, "Download older Telegram messages; attachments are separate")
}

func (c *Crawler) bindSearchFlags(fs *flag.FlagSet) {
	c.search = searchOptions{}
	fs.StringVar(&c.search.LocalConversationShortReferenceAcceptedBySelectedTrawler, "conversation", "", "Search only this conversation `LINK`")
	fs.StringVar(&c.search.Sender, "sender", "", "Show only messages from `PERSON`")
	fs.BoolVar(&c.search.FromMe, "from-me", false, "Show only messages sent by you")
	fs.BoolVar(&c.search.FromThem, "from-them", false, "Show only messages sent by other people")
	fs.BoolVar(&c.search.HasMedia, "has-media", false, "Show only messages with media")
	fs.BoolVar(&c.search.Pinned, "pinned", false, "Show only pinned messages")
	fs.BoolVar(&c.search.Asc, "asc", false, "Show oldest messages first")
}

func (c *Crawler) bindMessagesFlags(fs *flag.FlagSet) {
	c.messages = messageOptions{}
	fs.StringVar(&c.messages.Who, "who", "", "Show only messages that involve `PERSON`")
	fs.BoolVar(&c.messages.FromMe, "from-me", false, "Show only messages sent by you")
	fs.BoolVar(&c.messages.FromThem, "from-them", false, "Show only messages sent by other people")
	fs.BoolVar(&c.messages.HasMedia, "has-media", false, "Show only messages with media")
	fs.BoolVar(&c.messages.Pinned, "pinned", false, "Show only pinned messages")
}

func normalizeWords(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func (*Crawler) CanonicalRecordReferenceNamesConversation(canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference) bool {
	return strings.HasPrefix(trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReference), store.ChatRefPrefix)
}
