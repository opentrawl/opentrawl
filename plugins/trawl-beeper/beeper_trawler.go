package beeper

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	indexcontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/index"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
)

const (
	beeperTrawlerIdentifier     = "beeper"
	beeperTrawlerDisplayName    = "Beeper"
	beeperProviderDisplayName   = "Beeper Desktop"
	beeperAccessTokenSecretKey  = "beeper_access_token"
	beeperDesktopAppBundleID    = "com.automattic.beeper.desktop"
	beeperDefaultSelfHostedHost = "127.0.0.1:23373"
)

// BeeperTrawler implements the standalone external OpenTrawl plugin for Beeper Desktop.
type BeeperTrawler struct {
	cfg BeeperTrawlerConfiguration

	connectFlags beeperConnectFlagValues
	updateFlags  beeperUpdateFlagValues
	messageFlags beeperMessageFlagValues
}

var (
	_ trawlkit.Trawler                    = (*BeeperTrawler)(nil)
	_ trawlkit.Updater                    = (*BeeperTrawler)(nil)
	_ trawlkit.Searcher                   = (*BeeperTrawler)(nil)
	_ trawlkit.CanonicalRecordOpener      = (*BeeperTrawler)(nil)
	_ trawlkit.ConversationLister         = (*BeeperTrawler)(nil)
	_ trawlkit.TrawlerMessageLister       = (*BeeperTrawler)(nil)
	_ trawlkit.WhoMatcher                 = (*BeeperTrawler)(nil)
	_ trawlkit.PersonMessageCounter       = (*BeeperTrawler)(nil)
	_ trawlkit.PeopleSnapshotProvider     = (*BeeperTrawler)(nil)
	_ trawlkit.ArchiveIndexChangeExporter = (*BeeperTrawler)(nil)
	_ trawlkit.PluginCapabilityDeclarator = (*BeeperTrawler)(nil)
)

// NewBeeperTrawler returns a BeeperTrawler initialized with default settings.
func NewBeeperTrawler() *BeeperTrawler {
	return &BeeperTrawler{
		cfg: defaultBeeperTrawlerConfiguration(),
	}
}

// DeclaredPluginCapabilities declares the exact sandboxed resources used by trawl-beeper.
func (*BeeperTrawler) DeclaredPluginCapabilities() *plugincontract.PluginDeclaredCapabilities {
	return &plugincontract.PluginDeclaredCapabilities{
		DeclaredSelfHostedNetworkHosts:   []string{beeperDefaultSelfHostedHost},
		DeclaredReadSecretKeys:           []string{beeperAccessTokenSecretKey},
		DeclaredWriteSecretKeys:          []string{beeperAccessTokenSecretKey},
		SupportsArchiveIndexChangeExport: true,
	}
}

// RegisteredTrawlerDeclaration describes Beeper's identity, branding, and privacy boundary.
func (*BeeperTrawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(beeperTrawlerIdentifier),
		RegisteredTrawlerCommandName: beeperTrawlerIdentifier,
		RegisteredTrawlerDisplayName: beeperTrawlerDisplayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Accounts, conversations, participants, and paginated message history from Beeper Desktop's local read-only HTTP API.",
			LeavesMachine:   "Nothing. All API requests stay on loopback (127.0.0.1:23373) and the SQLite archive stays on this Mac.",
			NetworkRequests: "Strictly read-only HTTP GET requests to Beeper Desktop on 127.0.0.1:23373 during connect and update. Search, open, status, and browse are strictly local.",
		},
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:              "bubble.left.and.bubble.right.fill",
			AccentColor:             "#7C3AED",
			BundleIdentifier:        beeperDesktopAppBundleID,
			ArtworkBundleIdentifier: beeperDesktopAppBundleID,
		},
		SourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_SOURCE_APP_RUNNING,
	}
}

// LoadTrawlerConfiguration loads optional TOML settings from <stateRoot>/beeper/config.toml.
func (t *BeeperTrawler) LoadTrawlerConfiguration(path trawlkit.TrawlerConfigurationFilePath) error {
	loaded := defaultBeeperTrawlerConfiguration()
	if err := config.LoadTOMLFileIfPresent(string(path), &loaded); err != nil {
		return fmt.Errorf("load beeper config: %w", err)
	}
	if err := loaded.Validate(); err != nil {
		return err
	}
	t.cfg = loaded
	return nil
}

// TrawlerCommands declares shared operations and Beeper-specific subcommands.
func (t *BeeperTrawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
			RegisterTrawlerCommandFlags:      t.bindUpdateFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:      t.bindMessageFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		{
			TrawlerCommandName:               "networks",
			TrawlerCommandHelpDescription:    "Show archived Beeper networks and message counts",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return t.runNetworks(ctx, req)
			},
		},
		{
			TrawlerCommandName:               "connect",
			TrawlerCommandHelpDescription:    "Verify and save a Beeper Desktop read-only Bearer token in Keychain",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			RegisterTrawlerCommandFlags:      t.bindConnectFlags,
			TrawlerCommandChangesArchive:     true,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return t.runConnect(ctx, req)
			},
		},
		{
			TrawlerCommandName:               "disconnect",
			TrawlerCommandHelpDescription:    "Remove the stored Beeper Desktop Bearer token; keep the archive",
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
			TrawlerCommandChangesArchive:     true,
			ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
				return t.runDisconnect(ctx, req)
			},
		},
	}
}

// ExportArchiveIndexChanges exports committed Beeper messages and chat short
// references to OpenTrawl's core unified index.
func (t *BeeperTrawler) ExportArchiveIndexChanges(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	sinceOpaqueCursor string,
	emit func(*indexcontract.ArchiveIndexChangeBatch) error,
) error {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archiveFailure(fmt.Errorf("open beeper archive: %w", err))
	}
	return st.ExportArchiveIndexChanges(ctx, sinceOpaqueCursor, emit)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
