package whatsapp

import (
	"fmt"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/config"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	plugincontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/plugin"
)

type WhatsAppCrawlerConfiguration struct {
	Source    string `toml:"source,omitempty"`
	CopyMedia bool   `toml:"copy_media,omitempty"`
}

type Crawler struct {
	cfg WhatsAppCrawlerConfiguration

	messageFlags messageFlagValues
}

var (
	_ trawlkit.Trawler                               = (*Crawler)(nil)
	_ trawlkit.Updater                               = (*Crawler)(nil)
	_ trawlkit.Searcher                              = (*Crawler)(nil)
	_ trawlkit.WhoMatcher                            = (*Crawler)(nil)
	_ trawlkit.ConversationLister                    = (*Crawler)(nil)
	_ trawlkit.ConversationRecordReferenceRecognizer = (*Crawler)(nil)
	_ trawlkit.TrawlerMessageLister                  = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider                = (*Crawler)(nil)
	_ trawlkit.CanonicalRecordOpener                 = (*Crawler)(nil)
	_ trawlkit.ArchiveIndexChangeExporter            = (*Crawler)(nil)
	_ trawlkit.PluginCapabilityDeclarator            = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (*Crawler) DeclaredPluginCapabilities() *plugincontract.PluginDeclaredCapabilities {
	return &plugincontract.PluginDeclaredCapabilities{
		DeclaredReadPaths:                []string{"~/Library/Group Containers/group.net.whatsapp.WhatsApp.shared"},
		DeclaredConfigReadPathFields:     []string{"source"},
		SupportsArchiveIndexChangeExport: true,
	}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity("whatsapp"),
		RegisteredTrawlerCommandName: "whatsapp",
		RegisteredTrawlerDisplayName: "WhatsApp",
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "WhatsApp's local databases and media files stored on your Mac.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Updates use only local data.",
		},
		RegisteredTrawlerBranding: &federation.TrawlerBranding{
			SymbolName:              "phone.bubble.fill",
			AccentColor:             "#25D366",
			BundleIdentifier:        "net.whatsapp.WhatsApp",
			ArtworkBundleIdentifier: "net.whatsapp.WhatsApp",
		},
		SourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_SOURCE_APP_RUNNING,
	}
}

func (c *Crawler) LoadTrawlerConfiguration(trawlerConfigurationFilePath trawlkit.TrawlerConfigurationFilePath) error {
	loadedWhatsAppConfiguration := c.cfg
	if err := config.LoadTOMLFileIfPresent(string(trawlerConfigurationFilePath), &loadedWhatsAppConfiguration); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	c.cfg = loadedWhatsAppConfiguration
	return nil
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:      c.bindMessageFlags,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
		{
			SharedTrawlerOperation:           federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		},
	}
}
