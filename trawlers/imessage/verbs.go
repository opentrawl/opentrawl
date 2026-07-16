package imsgcrawl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const defaultMessageLimit = 20

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

type messageListOutput struct {
	listHeader
	ChatID     string               `json:"chat_id"`
	Chat       *archive.ChatSummary `json:"chat,omitempty"`
	Order      string               `json:"order"`
	Items      []archive.MessageRow `json:"items"`
	chatHandle string
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
			Name: "chats",
		},
		{
			Name:  "messages",
			Help:  "List archived iMessage messages in one chat.",
			Flags: c.bindMessagesFlags,
			Run:   c.runMessages,
		},
	}
}

func (c *Crawler) bindMessagesFlags(fs *flag.FlagSet) {
	c.messages = messagesOptions{limit: defaultMessageLimit}
	fs.StringVar(&c.messages.chatID, "chat", "", "chat id")
	fs.Var(intFlag{value: &c.messages.limit, seen: &c.messages.limitSet}, "limit", "maximum messages")
	fs.BoolVar(&c.messages.asc, "asc", false, "show oldest messages first")
}

// Chats implements trawlkit.ChatLister. The kit owns the chats verb, its
// flags, the JSON shape and the table; this only maps one store query into the
// shared Chat. Unread is the count of received messages the owner has not
// read; it is nil, and --unread is refused, only when the archive predates
// read-state ingestion, so a stale archive degrades honestly rather than
// reporting fake zeros. Re-syncing fills it.
func (c *Crawler) Chats(ctx context.Context, req *trawlkit.Request, q trawlkit.ChatQuery) ([]trawlkit.Chat, error) {
	limit := q.Limit
	if q.All {
		limit = 0
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	summaries, err := st.Chats(ctx, archive.ChatListOptions{Limit: limit, UnreadOnly: q.Unread})
	if errors.Is(err, archive.ErrNoReadState) {
		return nil, trawlkit.ErrChatsNoReadState
	}
	if err != nil {
		return nil, err
	}
	chats := make([]trawlkit.Chat, 0, len(summaries))
	for _, summary := range summaries {
		people := summary.ParticipantCount
		chats = append(chats, trawlkit.Chat{
			ID:  summary.ChatID,
			Ref: archive.ChatRef(summary.ChatID),
			// The chat.db rowid is a short, non-sensitive key messages --chat
			// accepts, so it is the safe fallback the human chat column shows in
			// the window before the archive indexes this chat's short ref.
			DisplayID: summary.ChatID,
			// An iMessage group is often unnamed. Title carries the real subject
			// when there is one, else it is empty and the kit names the chat from
			// these participants ("Alice, Bob +3").
			Title:            chatListTitle(summary),
			Group:            summary.Kind == "group",
			Participants:     &people,
			ParticipantNames: chatParticipantNames(summary),
			LastActivity:     archive.AppleDateTime(summary.LatestMessageDate),
			Unread:           summary.Unread,
		})
	}
	return chats, nil
}

func (c *Crawler) runMessages(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 0 {
		return usageErr(errors.New("messages takes flags only"))
	}
	// A reader pastes the chats-table short ref; an agent passes the full
	// imessage:chat/<id> ref or the raw id. All three resolve to the same chat.
	chatID, err := req.ResolveChatArg(ctx, c.messages.chatID, archive.ChatRefPrefix)
	if errors.Is(err, trawlkit.ErrShortRefNotChat) {
		return commandErr(1, "not_a_chat", errors.New("that short ref is a message, not a chat"), "run trawl imessage chats and copy a chat's ref")
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return commandErr(1, "ambiguous_short_ref", errors.New("short ref matches more than one chat"), "run trawl imessage chats and copy a chat's ref")
	}
	if err != nil {
		return err
	}
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
	refs := make([]string, 0, len(messages)+1)
	chatRef := archive.ChatRef(chatID)
	refs = append(refs, chatRef)
	for index := range messages {
		messages[index].Ref = archive.MessageRef(messages[index].MessageID)
		refs = append(refs, messages[index].Ref)
	}
	aliases, err := req.ShortRefAliases(ctx, refs)
	if err != nil {
		return err
	}
	for index := range messages {
		messages[index].ShortRef = aliases[messages[index].Ref]
	}
	chatHandle := aliases[chatRef]
	if chatHandle == "" {
		chatHandle = chatID
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
		chatHandle: chatHandle,
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
