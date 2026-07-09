package imsgcrawl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const (
	defaultChatLimit    = 50
	defaultMessageLimit = 20
)

type chatsOptions struct {
	limit    int
	limitSet bool
}

type messagesOptions struct {
	chatID   string
	limit    int
	limitSet bool
	asc      bool
}

type listHeader struct {
	SchemaVersion string `json:"schema_version"`
	AppID         string `json:"app_id"`
	Command       string `json:"command"`
	Returned      int    `json:"returned"`
	Total         int64  `json:"total"`
	Limit         int    `json:"limit,omitempty"`
	Complete      bool   `json:"complete"`
}

type chatListOutput struct {
	listHeader
	Items []archive.ChatSummary `json:"items"`
}

type messageListOutput struct {
	listHeader
	ChatID string               `json:"chat_id"`
	Chat   *archive.ChatSummary `json:"chat,omitempty"`
	Order  string               `json:"order"`
	Items  []archive.MessageRow `json:"items"`
}

type openOutput struct {
	Ref     string              `json:"ref"`
	Chat    openChatOutput      `json:"chat"`
	Message openMessageOutput   `json:"message"`
	Context []openMessageOutput `json:"context"`
}

type openChatOutput struct {
	Name         string   `json:"name"`
	Participants []string `json:"participants,omitempty"`
}

type openMessageOutput struct {
	Ref            string `json:"ref"`
	Time           string `json:"time"`
	Who            string `json:"who"`
	Where          string `json:"where"`
	Text           string `json:"text"`
	FromMe         bool   `json:"from_me"`
	HasAttachments bool   `json:"has_attachments,omitempty"`
	Target         bool   `json:"target,omitempty"`
}

func (c *Crawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		{
			Name:  "chats",
			Help:  "List archived iMessage chats.",
			Flags: c.bindChatsFlags,
			Run:   c.runChats,
		},
		{
			Name:  "messages",
			Help:  "List archived iMessage messages in one chat.",
			Flags: c.bindMessagesFlags,
			Run:   c.runMessages,
		},
	}
}

func (c *Crawler) bindChatsFlags(fs *flag.FlagSet) {
	c.chats = chatsOptions{limit: defaultChatLimit}
	fs.Var(intFlag{value: &c.chats.limit, seen: &c.chats.limitSet}, "limit", "maximum chats")
}

func (c *Crawler) bindMessagesFlags(fs *flag.FlagSet) {
	c.messages = messagesOptions{limit: defaultMessageLimit}
	fs.StringVar(&c.messages.chatID, "chat", "", "chat id")
	fs.Var(intFlag{value: &c.messages.limit, seen: &c.messages.limitSet}, "limit", "maximum messages")
	fs.BoolVar(&c.messages.asc, "asc", false, "show oldest messages first")
}

func (c *Crawler) runChats(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	rows, err := flags.Limit(c.chats.limit, c.chats.limitSet)
	if err != nil {
		return usageErr(err)
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	chats, err := st.Chats(ctx, rows)
	if err != nil {
		return err
	}
	total, err := st.CountChats(ctx)
	if err != nil {
		return err
	}
	out := chatListOutput{
		listHeader: newListHeader("chats", len(chats), total, rows),
		Items:      chats,
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "chats", out)
	}
	return printChatsText(req.Out, out)
}

func (c *Crawler) runMessages(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	chatID := strings.TrimSpace(c.messages.chatID)
	if chatID == "" {
		return usageErr(errors.New("messages requires --chat"))
	}
	rows, err := flags.Limit(c.messages.limit, c.messages.limitSet)
	if err != nil {
		return usageErr(err)
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messages, err := st.Messages(ctx, chatID, rows, c.messages.asc)
	if err != nil {
		return err
	}
	chat, err := st.Chat(ctx, chatID)
	if errors.Is(err, archive.ErrChatNotFound) {
		return commandErr(1, "not_found", fmt.Errorf("chat %s was not found", chatID), "run trawl imessage chats and use a current chat id")
	}
	if err != nil {
		return err
	}
	total, err := st.CountMessages(ctx, chatID)
	if err != nil {
		return err
	}
	order := "newest-first"
	if c.messages.asc {
		order = "oldest-first"
	}
	out := messageListOutput{
		listHeader: newListHeader("messages", len(messages), total, rows),
		ChatID:     chatID,
		Chat:       &chat,
		Order:      order,
		Items:      messages,
	}
	if req.Format == output.JSON {
		return output.Write(req.Out, req.Format, "messages", out)
	}
	return printMessagesText(req.Out, out)
}

func newListHeader(command string, returned int, total int64, limit int) listHeader {
	return listHeader{
		SchemaVersion: control.StatusSchemaVersion,
		AppID:         appID,
		Command:       command,
		Returned:      returned,
		Total:         total,
		Limit:         limit,
		Complete:      total <= int64(returned),
	}
}

type intFlag struct {
	value *int
	seen  *bool
}

func (f intFlag) String() string {
	if f.value == nil {
		return "0"
	}
	return strconv.Itoa(*f.value)
}

func (f intFlag) Set(raw string) error {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return err
	}
	*f.value = value
	*f.seen = true
	return nil
}
