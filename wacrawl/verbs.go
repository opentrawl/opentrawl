package wacrawl

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit"
	ckflags "github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/store"
)

type intFlag struct {
	value int
	set   bool
}

func newIntFlag(defaultValue int) intFlag {
	return intFlag{value: defaultValue}
}

func (f *intFlag) Set(value string) error {
	n, err := strconv.Atoi(value)
	if err != nil {
		return err
	}
	f.value = n
	f.set = true
	return nil
}

func (f *intFlag) String() string {
	return strconv.Itoa(f.value)
}

type chatsEnvelope struct {
	Chats     []chatRow `json:"chats"`
	Total     int       `json:"total"`
	Truncated bool      `json:"truncated"`
	unread    bool
}

type chatRow struct {
	ChatID        string `json:"chat_id"`
	Kind          string `json:"kind,omitempty"`
	Name          string `json:"name,omitempty"`
	LastMessageAt string `json:"last_message_at,omitempty"`
	UnreadCount   int    `json:"unread_count"`
	MessageCount  int    `json:"message_count"`
}

func (c *Crawler) runChats(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("chats takes flags only"))
	}
	return c.listChats(ctx, req, c.chatsUnread)
}

func (c *Crawler) runUnread(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("unread takes flags only"))
	}
	return c.listChats(ctx, req, true)
}

func (c *Crawler) listChats(ctx context.Context, req *crawlkit.Request, unread bool) error {
	limit, err := ckflags.Limit(c.chatsLimit.value, c.chatsLimit.set, c.chatsAll)
	if err != nil {
		return usageErr(err)
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(err)
	}
	var chats []store.Chat
	var total int
	if unread {
		chats, err = st.ListUnreadChats(ctx, limit)
		if err == nil {
			total, err = st.CountUnreadChats(ctx)
		}
	} else {
		chats, err = st.ListChats(ctx, limit)
		if err == nil {
			total, err = st.CountChats(ctx)
		}
	}
	if err != nil {
		return err
	}
	result := newChatsEnvelope(chats, total, unread)
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "chats", result)
	}
	return printChats(req, result)
}

func newChatsEnvelope(chats []store.Chat, total int, unread bool) chatsEnvelope {
	rows := make([]chatRow, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, chatRow{
			ChatID:        chat.JID,
			Kind:          chat.Kind,
			Name:          outputField(chat.Name),
			LastMessageAt: formatTime(chat.LastMessageAt),
			UnreadCount:   chat.UnreadCount,
			MessageCount:  chat.MessageCount,
		})
	}
	return chatsEnvelope{Chats: rows, Total: total, Truncated: total > len(rows), unread: unread}
}

func printChats(req *crawlkit.Request, value chatsEnvelope) error {
	heading := "Chats"
	empty := "No chats."
	hint := "Messages: wacrawl messages --chat CHAT"
	if value.unread {
		heading = "Unread chats"
		empty = "No unread chats."
	}
	if len(value.Chats) == 0 {
		_, err := fmt.Fprintln(req.Out, empty)
		return err
	}
	if _, err := fmt.Fprintf(req.Out, "%s: showing %d of %d.\n%s\n", heading, len(value.Chats), value.Total, hint); err != nil {
		return err
	}
	if value.Truncated {
		if value.unread {
			_, _ = fmt.Fprintln(req.Out, "All: wacrawl unread --all")
		} else {
			_, _ = fmt.Fprintln(req.Out, "All: wacrawl chats --all")
		}
	}
	if _, err := fmt.Fprintln(req.Out); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Chats))
	for _, chat := range value.Chats {
		rows = append(rows, []string{
			render.ShortLocalTime(parseFormattedTime(chat.LastMessageAt)),
			chat.Kind,
			strconv.Itoa(chat.UnreadCount),
			strconv.Itoa(chat.MessageCount),
			chat.ChatID,
			chatDisplayName(chat.Name),
		})
	}
	return render.WriteTable(req.Out, []render.TableColumn{
		{Header: "last"},
		{Header: "kind"},
		{Header: "unread", AlignRight: true},
		{Header: "messages", AlignRight: true},
		{Header: "chat id"},
		{Header: "name", Wrap: true},
	}, rows)
}

func chatDisplayName(name string) string {
	if name = outputField(name); name != "" {
		return name
	}
	return "WhatsApp chat"
}

type messageFlagValues struct {
	chat     string
	sender   string
	limit    intFlag
	all      bool
	after    string
	before   string
	fromMe   bool
	fromThem bool
	hasMedia bool
	asc      bool
}

func (c *Crawler) bindMessageFlags(fs *flag.FlagSet) {
	c.messageFlags = messageFlagValues{limit: newIntFlag(defaultMessageLimit)}
	fs.StringVar(&c.messageFlags.chat, "chat", "", "only messages in this chat")
	fs.StringVar(&c.messageFlags.sender, "sender", "", "only messages from this sender")
	fs.Var(&c.messageFlags.limit, "limit", "maximum messages")
	fs.BoolVar(&c.messageFlags.all, "all", false, "return every message")
	fs.StringVar(&c.messageFlags.after, "after", "", "only messages at or after this date")
	fs.StringVar(&c.messageFlags.before, "before", "", "only messages before this date")
	fs.BoolVar(&c.messageFlags.fromMe, "from-me", false, "only messages sent by you")
	fs.BoolVar(&c.messageFlags.fromThem, "from-them", false, "only messages sent by others")
	fs.BoolVar(&c.messageFlags.hasMedia, "has-media", false, "only messages with media")
	fs.BoolVar(&c.messageFlags.asc, "asc", false, "oldest first")
}

func (f messageFlagValues) resolve() (store.MessageFilter, error) {
	if f.fromMe && f.fromThem {
		return store.MessageFilter{}, fmt.Errorf("--from-me and --from-them are mutually exclusive")
	}
	limit, err := ckflags.Limit(f.limit.value, f.limit.set, f.all)
	if err != nil {
		return store.MessageFilter{}, err
	}
	out := store.MessageFilter{
		ChatJID:  f.chat,
		Sender:   f.sender,
		Limit:    limit,
		HasMedia: f.hasMedia,
		Asc:      f.asc,
	}
	if f.fromMe {
		v := true
		out.FromMe = &v
	}
	if f.fromThem {
		v := false
		out.FromMe = &v
	}
	if strings.TrimSpace(f.after) != "" {
		t, err := parseTime(f.after)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.After = &t
	}
	if strings.TrimSpace(f.before) != "" {
		t, err := parseTime(f.before)
		if err != nil {
			return store.MessageFilter{}, err
		}
		out.Before = &t
	}
	return out, nil
}

type messageListOutput struct {
	Query     string          `json:"query,omitempty"`
	Returned  int             `json:"returned"`
	Limit     int             `json:"limit"`
	Truncated bool            `json:"truncated"`
	Messages  []store.Message `json:"results"`
	aliases   map[string]string
}

func (c *Crawler) runMessages(ctx context.Context, req *crawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(fmt.Errorf("messages takes flags only"))
	}
	filter, err := c.messageFlags.resolve()
	if err != nil {
		return usageErr(err)
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(err)
	}
	messages, err := st.Messages(ctx, filter)
	if err != nil {
		return err
	}
	aliases, err := searchAliases(ctx, st, messages)
	if err != nil {
		return err
	}
	result := newMessageListOutput(filter.Limit, messages, aliases)
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "messages", result)
	}
	return printMessages(req, result)
}

func newMessageListOutput(limit int, messages []store.Message, aliases map[string]string) messageListOutput {
	if messages == nil {
		messages = []store.Message{}
	}
	return messageListOutput{
		Returned:  len(messages),
		Limit:     limit,
		Truncated: limit > 0 && len(messages) == limit,
		Messages:  messages,
		aliases:   aliases,
	}
}

func printMessages(req *crawlkit.Request, value messageListOutput) error {
	hints := []string{"Open: wacrawl open REF"}
	if value.Truncated {
		hints = append(hints, "Narrow: wacrawl messages --limit N --after DATE --before DATE --chat JID")
	}
	return render.WriteList(req.Out, render.List{
		Heading:   fmt.Sprintf("Messages: showing %d, newest first.", value.Returned),
		Hints:     hints,
		Items:     messageListItems(value.Messages, value.aliases),
		ClampText: 0,
		Empty:     "No messages.",
	})
}

func messageListItems(messages []store.Message, aliases map[string]string) []render.ListItem {
	items := make([]render.ListItem, 0, len(messages))
	for _, m := range messages {
		full := messageRef(m)
		items = append(items, render.ListItem{
			Time:  m.Timestamp,
			Who:   outputField(messageWho(m)),
			Where: outputField(messageWhere(m)),
			Ref:   displayRef(full, aliases[full]),
			Text:  messageText(m),
		})
	}
	return items
}
