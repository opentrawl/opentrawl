package trawlkit

import (
	"fmt"
	"io"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type chatsOutput struct {
	Chats []chatOutput `json:"chats"`
	// Truncated is exact, not guessed: the kit fetched one row past the page
	// and saw it come back. A JSON consumer uses it to page with --limit.
	Truncated bool `json:"truncated"`

	unread bool
}

type chatOutput struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Kind         string `json:"kind,omitempty"`
	Participants *int64 `json:"participants,omitempty"`
	LastActivity string `json:"last_activity,omitempty"`
	Unread       *int64 `json:"unread,omitempty"`

	displayID    string
	lastActivity time.Time
}

func newChatsOutput(chats []Chat, unread, truncated bool) chatsOutput {
	rows := make([]chatOutput, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, chatOutput{
			ID:           chat.ID,
			Title:        chat.Title,
			Kind:         kindLabel(chat.Group),
			Participants: copyCount(chat.Participants),
			LastActivity: formatContractTime(chat.LastActivity),
			Unread:       copyCount(chat.Unread),
			displayID:    firstText(chat.DisplayID, chat.ID),
			lastActivity: chat.LastActivity,
		})
	}
	return chatsOutput{Chats: rows, Truncated: truncated, unread: unread}
}

// kindLabel is the whole vocabulary of the kind field: a chat is either a
// group or a one-to-one dm. Defined here so every surface reads the same.
func kindLabel(group bool) string {
	if group {
		return "group"
	}
	return "dm"
}

func copyCount(n *int64) *int64 {
	if n == nil {
		return nil
	}
	v := *n
	return &v
}

func writeChatsText(w io.Writer, value chatsOutput) error {
	if len(value.Chats) == 0 {
		empty := "No chats."
		if value.unread {
			empty = "No unread chats."
		}
		_, err := fmt.Fprintln(w, empty)
		return err
	}
	heading := "Chats"
	if value.unread {
		heading = "Unread chats"
	}
	if _, err := fmt.Fprintf(w, "%s: showing %s, newest first.\n", heading, render.FormatInteger(int64(len(value.Chats)))); err != nil {
		return err
	}
	if value.Truncated {
		if _, err := fmt.Fprintln(w, "More: raise --limit, or list all with --all"); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	// A column appears only when the surface fills it: participant counts and
	// unread counts are omitted whole rather than shown as a fake zero. This
	// is a deterministic, structural choice on the data already in hand, so
	// the same result renders the same table every time.
	showPeople := anyCount(value.Chats, func(c chatOutput) *int64 { return c.Participants })
	showUnread := anyCount(value.Chats, func(c chatOutput) *int64 { return c.Unread })

	columns := []render.TableColumn{
		{Header: "last"},
		{Header: "chat"},
		{Header: "kind"},
	}
	if showPeople {
		columns = append(columns, render.TableColumn{Header: "people", AlignRight: true})
	}
	if showUnread {
		columns = append(columns, render.TableColumn{Header: "unread", AlignRight: true})
	}
	columns = append(columns, render.TableColumn{Header: "name", Wrap: true})

	rows := make([][]string, 0, len(value.Chats))
	for _, chat := range value.Chats {
		row := []string{
			render.ShortLocalTime(chat.lastActivity),
			chat.displayID,
			chat.Kind,
		}
		if showPeople {
			row = append(row, countCell(chat.Participants))
		}
		if showUnread {
			row = append(row, countCell(chat.Unread))
		}
		row = append(row, chat.Title)
		rows = append(rows, row)
	}
	return render.WriteTable(w, columns, rows)
}

func anyCount(chats []chatOutput, pick func(chatOutput) *int64) bool {
	for _, chat := range chats {
		if pick(chat) != nil {
			return true
		}
	}
	return false
}

func countCell(n *int64) string {
	if n == nil {
		return ""
	}
	return render.FormatInteger(*n)
}
