package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/wacrawl/internal/store"
)

type chatsEnvelope struct {
	Chats []chatRow `json:"chats"`
	// unread selects the heading and empty sentence; it is never serialized.
	unread bool
}

type chatRow struct {
	ChatID        string `json:"chat_id"`
	Kind          string `json:"kind,omitempty"`
	Name          string `json:"name,omitempty"`
	LastMessageAt string `json:"last_message_at,omitempty"`
	UnreadCount   int    `json:"unread_count"`
	MessageCount  int    `json:"message_count"`
}

func newChatsEnvelope(chats []store.Chat, unread bool) chatsEnvelope {
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
	return chatsEnvelope{Chats: rows, unread: unread}
}

// chatDisplayName is the human-table label; JSON keeps the raw name so
// consumers see exactly what the archive stored.
func chatDisplayName(name string) string {
	if name = outputField(name); name != "" {
		return name
	}
	return "WhatsApp chat"
}

func (a *app) runChats(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("chats", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	limit := fs.Int("limit", 50, "")
	unread := fs.Bool("unread", false, "")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printCommandUsage(a.stdout, "chats")
			return nil
		}
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("chats takes flags only"))
	}
	if *limit < 1 {
		return usageErr(errors.New("chats --limit must be at least 1"))
	}
	return a.withReadStore(ctx, func(st *store.Store) error {
		var (
			chats []store.Chat
			err   error
		)
		if *unread {
			chats, err = st.ListUnreadChats(ctx, *limit)
		} else {
			chats, err = st.ListChats(ctx, *limit)
		}
		if err != nil {
			return err
		}
		return a.print(newChatsEnvelope(chats, *unread))
	})
}

func (a *app) printChats(value chatsEnvelope) error {
	heading := "Chats"
	empty := "No chats."
	hint := "Messages: wacrawl messages --chat CHAT"
	if value.unread {
		heading = "Unread chats"
		empty = "No unread chats."
	}
	if len(value.Chats) == 0 {
		_, err := fmt.Fprintln(a.stdout, empty)
		return err
	}
	if _, err := fmt.Fprintf(a.stdout, "%s: showing %d.\n%s\n\n", heading, len(value.Chats), hint); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Chats))
	for _, chat := range value.Chats {
		rows = append(rows, []string{
			shortLocalTime(parseFormattedTime(chat.LastMessageAt)),
			chat.Kind,
			strconv.Itoa(chat.UnreadCount),
			strconv.Itoa(chat.MessageCount),
			chat.ChatID,
			chatDisplayName(chat.Name),
		})
	}
	return render.WriteTable(a.stdout, []render.TableColumn{
		{Header: "last"},
		{Header: "kind"},
		{Header: "unread", AlignRight: true},
		{Header: "messages", AlignRight: true},
		{Header: "chat id"},
		{Header: "name", Wrap: true},
	}, rows)
}
