package wacrawl

import (
	"flag"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
)

type Config struct {
	Source    string `toml:"source,omitempty"`
	CopyMedia bool   `toml:"copy_media,omitempty"`
}

type Crawler struct {
	cfg Config

	chatsLimit  intFlag
	chatsUnread bool

	messageFlags messageFlagValues
}

var _ crawlkit.FullCrawler = (*Crawler)(nil)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          "whatsapp",
		Surface:     "whatsapp",
		DisplayName: "WhatsApp",
		Description: "Local-first WhatsApp Desktop archive crawler.",
		ShortRefs:   true,
		Config:      &c.cfg,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"whatsapp-desktop", "sqlite", "contact-export"},
		},
	}
}

func (c *Crawler) Verbs() []crawlkit.Verb {
	return []crawlkit.Verb{
		{
			Name:  "chats",
			Help:  "List archived WhatsApp chats.",
			Flags: c.bindChatsFlags,
			Run:   c.runChats,
		},
		{
			Name:  "unread",
			Help:  "List archived WhatsApp chats with unread messages.",
			Flags: c.bindUnreadFlags,
			Run:   c.runUnread,
		},
		{
			Name:  "messages",
			Help:  "List archived WhatsApp messages.",
			Flags: c.bindMessageFlags,
			Run:   c.runMessages,
		},
	}
}

func (c *Crawler) bindChatsFlags(fs *flag.FlagSet) {
	c.chatsLimit = newIntFlag(50)
	c.chatsUnread = false
	fs.Var(&c.chatsLimit, "limit", "maximum chats")
	fs.BoolVar(&c.chatsUnread, "unread", false, "only unread chats")
}

func (c *Crawler) bindUnreadFlags(fs *flag.FlagSet) {
	c.chatsLimit = newIntFlag(50)
	c.chatsUnread = true
	fs.Var(&c.chatsLimit, "limit", "maximum chats")
}
