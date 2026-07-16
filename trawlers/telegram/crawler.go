package telecrawl

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

const appID = "telegram"

type Crawler struct {
	sync   syncOptions
	search searchOptions

	messages messageOptions
	contacts listOptions
	topics   topicsOptions
}

type syncOptions struct {
	Path          string
	DialogsLimit  int
	MessagesLimit int
	Chat          string
	FetchMedia    bool
}

type searchOptions struct {
	ChatJID  string
	Sender   string
	TopicID  string
	FromMe   bool
	FromThem bool
	HasMedia bool
	Pinned   bool
	Asc      bool
}

type listOptions struct {
	Limit    int
	LimitSet bool
}

type messageOptions struct {
	ChatJID  string
	Sender   string
	TopicID  string
	Who      string
	Limit    int
	LimitSet bool
	After    string
	Before   string
	FromMe   bool
	FromThem bool
	HasMedia bool
	Pinned   bool
	Asc      bool
}

type topicsOptions struct {
	ChatID   string
	Limit    int
	LimitSet bool
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
		ID:          appID,
		Surface:     "telegram",
		DisplayName: "Telegram",
		Headlines:   []string{"chats", "folders", "topics"},
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"telegram-desktop", "telegram-macos-postbox", "sqlite"},
		},
	}
}

func (c *Crawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		{Name: "sync", Flags: c.bindSyncFlags},
		{Name: "search", Flags: c.bindSearchFlags},
		{Name: "chats"},
		{Name: "folders", Help: "List archived Telegram folders.", Run: c.runFolders},
		{Name: "topics", Help: "List archived Telegram forum topics.", Flags: c.bindTopicsFlags, Run: c.runTopics},
		{Name: "messages", Help: "List archived Telegram messages.", Flags: c.bindMessagesFlags, Run: c.runMessages},
		{Name: "contacts", Help: "List archived Telegram contacts.", Flags: c.bindContactsFlags, Run: c.runContacts},
	}
}

func (c *Crawler) handler(ctx context.Context, req *trawlkit.Request) *runtime {
	return &runtime{
		c:          c,
		ctx:        ctx,
		req:        req,
		stdout:     req.Out,
		json:       req.Format == ckoutput.JSON,
		dbPath:     req.Paths.Archive,
		configPath: req.Paths.Config,
		log:        req.Log,
	}
}

type runtime struct {
	c          *Crawler
	ctx        context.Context
	req        *trawlkit.Request
	stdout     io.Writer
	json       bool
	dbPath     string
	configPath string
	log        *cklog.Run
}

func (r *runtime) withStore(fn func(*store.Store) error) error {
	st, err := store.Use(r.ctx, r.req.Store, r.req.Paths.Archive)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	return fn(st)
}

func (r *runtime) withReadOnlyStore(fn func(*store.Store) error) error {
	st, err := store.UseExisting(r.ctx, r.req.Store, r.req.Paths.Archive)
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
	c.sync = syncOptions{
		DialogsLimit:  defaultDialogsLimit(),
		MessagesLimit: defaultMessagesLimit(),
	}
	fs.StringVar(&c.sync.Path, "path", "", "Telegram data directory")
	fs.IntVar(&c.sync.DialogsLimit, "dialogs-limit", c.sync.DialogsLimit, "maximum dialogs to sync")
	fs.IntVar(&c.sync.MessagesLimit, "messages-limit", c.sync.MessagesLimit, "maximum messages per dialog")
	fs.StringVar(&c.sync.Chat, "chat", "", "only sync one chat id")
	fs.BoolVar(&c.sync.FetchMedia, "fetch-media", false, "fetch missing cloud media")
}

func (c *Crawler) bindSearchFlags(fs *flag.FlagSet) {
	c.search = searchOptions{}
	fs.StringVar(&c.search.ChatJID, "chat", "", "only results in this chat")
	fs.StringVar(&c.search.Sender, "sender", "", "only results from this sender")
	fs.StringVar(&c.search.TopicID, "topic", "", "only results in this topic")
	fs.BoolVar(&c.search.FromMe, "from-me", false, "only outgoing messages")
	fs.BoolVar(&c.search.FromThem, "from-them", false, "only incoming messages")
	fs.BoolVar(&c.search.HasMedia, "media", false, "only media messages")
	fs.BoolVar(&c.search.Pinned, "pinned", false, "only pinned messages")
	fs.BoolVar(&c.search.Asc, "asc", false, "oldest results first")
}

func (c *Crawler) bindContactsFlags(fs *flag.FlagSet) {
	c.contacts = listOptions{Limit: 100}
	fs.Var(trackedInt{value: &c.contacts.Limit, seen: &c.contacts.LimitSet}, "limit", "maximum contacts")
}

func (c *Crawler) bindTopicsFlags(fs *flag.FlagSet) {
	c.topics = topicsOptions{Limit: 100}
	fs.StringVar(&c.topics.ChatID, "chat", "", "chat id")
	fs.Var(trackedInt{value: &c.topics.Limit, seen: &c.topics.LimitSet}, "limit", "maximum topics")
}

func (c *Crawler) bindMessagesFlags(fs *flag.FlagSet) {
	c.messages = messageOptions{Limit: defaultMessageLimit}
	fs.StringVar(&c.messages.ChatJID, "chat", "", "only messages in this chat")
	fs.StringVar(&c.messages.Sender, "sender", "", "only messages from this sender")
	fs.StringVar(&c.messages.TopicID, "topic", "", "only messages in this topic")
	fs.StringVar(&c.messages.Who, "who", "", "only messages involving this person")
	fs.Var(trackedInt{value: &c.messages.Limit, seen: &c.messages.LimitSet}, "limit", "maximum messages")
	fs.StringVar(&c.messages.After, "after", "", "only messages at or after this date")
	fs.StringVar(&c.messages.Before, "before", "", "only messages before this date")
	fs.BoolVar(&c.messages.FromMe, "from-me", false, "only outgoing messages")
	fs.BoolVar(&c.messages.FromThem, "from-them", false, "only incoming messages")
	fs.BoolVar(&c.messages.HasMedia, "media", false, "only media messages")
	fs.BoolVar(&c.messages.Pinned, "pinned", false, "only pinned messages")
	fs.BoolVar(&c.messages.Asc, "asc", false, "oldest messages first")
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
		return fmt.Errorf("must be a number: %s", raw)
	}
	*v.value = n
	if v.seen != nil {
		*v.seen = true
	}
	return nil
}

func defaultDialogsLimit() int {
	return 200
}

func defaultMessagesLimit() int {
	return 500
}

func normalizeWords(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
