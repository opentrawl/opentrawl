package whatsapp

import (
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

type Config struct {
	Source    string `toml:"source,omitempty"`
	CopyMedia bool   `toml:"copy_media,omitempty"`
}

type Crawler struct {
	cfg Config

	messageFlags messageFlagValues
}

var (
	_ trawlkit.Trawler                = (*Crawler)(nil)
	_ trawlkit.Syncer                 = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.ConversationLister     = (*Crawler)(nil)
	_ trawlkit.TrawlerMessageLister   = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity("whatsapp"),
		RegisteredTrawlerCommandName: "whatsapp",
		RegisteredTrawlerDisplayName: "WhatsApp",
		TrawlerConfiguration:         &c.cfg,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "WhatsApp for macOS's local databases and available media files.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Updates use only local data.",
		},
	}
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SYNC, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{
			SharedTrawlerOperation:                 federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_MESSAGES,
			RegisterTrawlerCommandFlags:            c.bindMessageFlags,
			TrawlerCommandShownInBareTrawlOverview: true,
		},
		{
			SharedTrawlerOperation:                 federationv1.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_CONVERSATIONS,
			TrawlerCommandShownInBareTrawlOverview: true,
		},
	}
}
