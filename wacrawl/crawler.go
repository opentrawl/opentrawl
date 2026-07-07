package wacrawl

import (
	"flag"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/wacrawl/internal/backup"
)

type Config struct {
	Source    string        `toml:"source,omitempty"`
	CopyMedia bool          `toml:"copy_media,omitempty"`
	Backup    backup.Config `toml:"backup,omitempty"`
}

type Crawler struct {
	cfg Config

	chatsLimit  intFlag
	chatsAll    bool
	chatsUnread bool

	messageFlags messageFlagValues

	backupOpts   backup.Options
	backupNoPush bool
	backupLimit  intFlag
}

var _ crawlkit.FullCrawler = (*Crawler)(nil)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          "wacrawl",
		Surface:     "whatsapp",
		DisplayName: "WhatsApp",
		Description: "Local-first WhatsApp Desktop archive crawler.",
		ShortRefs:   true,
		Config:      &c.cfg,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"whatsapp-desktop", "sqlite", "encrypted-git-backup", "contact-export"},
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
		{
			Name:    "backup init",
			Help:    "Create encrypted Git backup configuration.",
			Flags:   c.bindBackupInitFlags,
			Mutates: true,
			Store:   crawlkit.StoreNone,
			Timeout: 10 * time.Minute,
			Run:     c.runBackupInit,
		},
		{
			Name:    "backup push",
			Help:    "Write an encrypted Git backup snapshot.",
			Flags:   c.bindBackupPushFlags,
			Mutates: true,
			Timeout: 10 * time.Minute,
			Run:     c.runBackupPush,
		},
		{
			Name:    "backup pull",
			Help:    "Restore an encrypted Git backup snapshot.",
			Flags:   c.bindBackupPullFlags,
			Mutates: true,
			Timeout: 10 * time.Minute,
			Run:     c.runBackupPull,
		},
		{
			Name:  "backup status",
			Help:  "Show encrypted Git backup status.",
			Flags: c.bindBackupStatusFlags,
			Store: crawlkit.StoreNone,
			Run:   c.runBackupStatus,
		},
		{
			Name:  "backup snapshots",
			Help:  "List encrypted Git backup snapshots.",
			Flags: c.bindBackupSnapshotsFlags,
			Store: crawlkit.StoreNone,
			Run:   c.runBackupSnapshots,
		},
	}
}

func (c *Crawler) bindChatsFlags(fs *flag.FlagSet) {
	c.chatsLimit = newIntFlag(50)
	c.chatsAll = false
	c.chatsUnread = false
	fs.Var(&c.chatsLimit, "limit", "maximum chats")
	fs.BoolVar(&c.chatsAll, "all", false, "return every chat")
	fs.BoolVar(&c.chatsUnread, "unread", false, "only unread chats")
}

func (c *Crawler) bindUnreadFlags(fs *flag.FlagSet) {
	c.chatsLimit = newIntFlag(50)
	c.chatsAll = false
	c.chatsUnread = true
	fs.Var(&c.chatsLimit, "limit", "maximum chats")
	fs.BoolVar(&c.chatsAll, "all", false, "return every chat")
}
