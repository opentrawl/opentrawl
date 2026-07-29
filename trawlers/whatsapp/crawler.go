package whatsapp

import (
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
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
		RegisteredTrawlerManifestIdentity:           "whatsapp",
		RegisteredTrawlerCommandName:                "whatsapp",
		RegisteredTrawlerDisplayName:                "WhatsApp",
		TrawlerCommandNamesShownInBareTrawlOverview: []string{"messages", "conversations"},
		TrawlerConfiguration:                        &c.cfg,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "WhatsApp for macOS's local databases and available media files.",
			LeavesMachine:   "Nothing. Normal sync and search stay on your Mac.",
			NetworkRequests: "None. Normal sync is local.",
		},
	}
}

func (c *Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{
			TrawlerCommandName:          "messages",
			RegisterTrawlerCommandFlags: c.bindMessageFlags,
		},
	}
}
