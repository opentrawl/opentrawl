package wacrawl

import (
	"context"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/render"
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

// Chats implements trawlkit.ChatLister. WhatsApp Desktop stores a real
// unread count per chat, so both the plain list and the --unread filter are
// answered from the store; the kit owns the verb, flags, JSON and table.
func (c *Crawler) Chats(ctx context.Context, req *trawlkit.Request, q trawlkit.ChatQuery) ([]trawlkit.Chat, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	}
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(err)
	}
	var rows []store.Chat
	if q.Unread {
		rows, err = st.ListUnreadChats(ctx, limit)
	} else {
		rows, err = st.ListChats(ctx, limit)
	}
	if err != nil {
		return nil, err
	}
	chats := make([]trawlkit.Chat, 0, len(rows))
	for _, row := range rows {
		unread := int64(row.UnreadCount)
		chat := trawlkit.Chat{
			// Only a one-to-one "dm" is a dm; groups, newsletters and status
			// broadcasts are all group-shaped, never a private thread.
			ID:           row.JID,
			Title:        whatsappChatTitle(row),
			Group:        row.Kind != "dm",
			LastActivity: row.LastMessageAt,
			Unread:       &unread,
		}
		// A privacy @lid is both a machine ref and privacy-sensitive, so it is
		// masked in the human table while --json keeps the real id.
		if privacyID(row.JID) {
			chat.DisplayID = "privacy id"
		}
		chats = append(chats, chat)
	}
	return chats, nil
}

func whatsappChatTitle(chat store.Chat) string {
	// humanParticipantLabel masks a stored name that is itself a privacy
	// @lid, so no title path can print one raw.
	if name := humanParticipantLabel(chat.Name); name != "" {
		return name
	}
	if privacyID(chat.JID) {
		return unknownPrivacyParticipant
	}
	return "WhatsApp chat"
}

type messageFlagValues struct {
	chat     string
	sender   string
	limit    intFlag
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
	limit, err := ckflags.Limit(f.limit.value, f.limit.set)
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

func (c *Crawler) runMessages(ctx context.Context, req *trawlkit.Request) error {
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
	aliases, err := searchAliases(ctx, req, messages)
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

func printMessages(req *trawlkit.Request, value messageListOutput) error {
	hints := []string{"Open: trawl whatsapp open REF"}
	if value.Truncated {
		hints = append(hints, "Narrow: trawl whatsapp messages --limit N --after DATE --before DATE --chat JID")
	}
	return render.WriteList(req.Out, render.List{
		Heading:   fmt.Sprintf("Messages: showing %s, newest first.", render.FormatInteger(int64(value.Returned))),
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
			Ref:   full,
			Text:  messageText(m),
		})
	}
	return items
}
