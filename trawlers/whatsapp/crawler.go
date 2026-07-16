package wacrawl

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
	_ trawlkit.Crawler                = (*Crawler)(nil)
	_ trawlkit.Syncer                 = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.ChatLister             = (*Crawler)(nil)
	_ trawlkit.Opener                 = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          "whatsapp",
		Surface:     "whatsapp",
		DisplayName: "WhatsApp",
		Headlines:   []string{"chats", "groups"},
		Config:      &c.cfg,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"whatsapp-desktop", "sqlite", "people-snapshot"},
		},
	}
}

func (c *Crawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		{
			Name: "chats",
		},
		{
			Name:  "messages",
			Help:  "List archived WhatsApp messages.",
			Flags: c.bindMessageFlags,
			Run:   c.runMessages,
		},
	}
}
