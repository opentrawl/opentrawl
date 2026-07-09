package trawlkit

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// participantPreviewCap is how many participant names a preview shows before it
// collapses the rest into "+N". One value, used for both the synthesised name
// of an unnamed group and the participants column, so a reader learns one shape.
const participantPreviewCap = 3

type chatsOutput struct {
	Chats []chatOutput `json:"chats"`
	// Truncated is exact, not guessed: the kit fetched one row past the page
	// and saw it come back. A JSON consumer uses it to page with --limit.
	Truncated bool `json:"truncated"`

	unread bool
}

type chatOutput struct {
	// ID is the raw source key messages --chat accepts; Ref is the handle a
	// reader copies from the human table. --json carries both so an agent can
	// act without re-deriving either.
	ID               string   `json:"id"`
	Ref              string   `json:"ref,omitempty"`
	Name             string   `json:"name"`
	Kind             string   `json:"kind,omitempty"`
	Participants     *int64   `json:"participants,omitempty"`
	ParticipantNames []string `json:"participant_names,omitempty"`
	LastActivity     string   `json:"last_activity,omitempty"`
	Unread           *int64   `json:"unread,omitempty"`

	// Human-table cells, computed once in newChatsOutput.
	participantsCell string
	handleCell       string
	lastActivity     time.Time
}

func newChatsOutput(chats []Chat, unread, truncated bool) chatsOutput {
	rows := make([]chatOutput, 0, len(chats))
	for _, chat := range chats {
		name := chatDisplayName(chat)
		rows = append(rows, chatOutput{
			ID:               chat.ID,
			Ref:              chat.Ref,
			Name:             name,
			Kind:             kindLabel(chat.Group),
			Participants:     copyCount(chat.Participants),
			ParticipantNames: append([]string(nil), chat.ParticipantNames...),
			LastActivity:     formatContractTime(chat.LastActivity),
			Unread:           copyCount(chat.Unread),
			participantsCell: chatParticipantsCell(chat),
			handleCell:       firstText(chat.DisplayID, chat.Ref, chat.ID),
			lastActivity:     chat.LastActivity,
		})
	}
	return chatsOutput{Chats: rows, Truncated: truncated, unread: unread}
}

// chatDisplayName is the name the human table and --json both show. It leads
// with the real title; an unnamed group falls back to a preview of its
// participants ("Alice, Bob +3"), which is how a person names such a chat when
// the app does not.
func chatDisplayName(chat Chat) string {
	if title := strings.TrimSpace(chat.Title); title != "" {
		return title
	}
	// No stored name: the participants name the chat. For a dm that is the other
	// person; for a group it is the roster preview.
	if preview := participantPreview(chat.ParticipantNames, chat.Participants); preview != "" {
		return preview
	}
	if chat.Group {
		// A group whose members did not resolve to names still knows how many
		// there are, which beats an anonymous "group chat".
		if chat.Participants != nil && *chat.Participants > 0 {
			return "group of " + render.FormatInteger(*chat.Participants)
		}
		return "group chat"
	}
	return "chat"
}

// chatParticipantsCell fills the participants column. It shows the roster only
// for a named group, where the name does not already carry it; an unnamed group
// puts its roster in the name, so repeating it here would be noise, and a dm's
// participant is its name.
func chatParticipantsCell(chat Chat) string {
	if !chat.Group || strings.TrimSpace(chat.Title) == "" {
		return ""
	}
	return participantPreview(chat.ParticipantNames, chat.Participants)
}

// participantPreview joins up to participantPreviewCap names and collapses the
// rest into "+N", using the head count when the surface resolved fewer names
// than it counted.
func participantPreview(names []string, total *int64) string {
	clean := make([]string, 0, len(names))
	for _, name := range names {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	shown := clean
	if len(shown) > participantPreviewCap {
		shown = shown[:participantPreviewCap]
	}
	head := int64(len(clean))
	if total != nil && *total > head {
		head = *total
	}
	remaining := head - int64(len(shown))
	preview := strings.Join(shown, ", ")
	if remaining > 0 {
		preview += " +" + render.FormatInteger(remaining)
	}
	return preview
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
	// A column appears only when the surface fills it: the participants column
	// shows only when a named group has a roster to list, and unread only when a
	// chat carries a real count. Both are structural choices on the data in
	// hand, so the same rows render the same table every time.
	showParticipants := anyCell(value.Chats, func(c chatOutput) string { return c.participantsCell })
	showUnread := anyCount(value.Chats, func(c chatOutput) *int64 { return c.Unread })

	columns := []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "kind"},
	}
	if showParticipants {
		// Not a wrap column: the preview is capped to a few names, so it fits on
		// one line, and a dm row with no roster shows the plain "-" marker rather
		// than a wrapped "(empty)".
		columns = append(columns, render.TableColumn{Header: "participants"})
	}
	if showUnread {
		columns = append(columns, render.TableColumn{Header: "unread", AlignRight: true})
	}
	columns = append(columns,
		render.TableColumn{Header: "last"},
		render.TableColumn{Header: "chat"},
	)

	rows := make([][]string, 0, len(value.Chats))
	for _, chat := range value.Chats {
		row := []string{chat.Name, chat.Kind}
		if showParticipants {
			row = append(row, chat.participantsCell)
		}
		if showUnread {
			row = append(row, countCell(chat.Unread))
		}
		row = append(row, render.ShortLocalTime(chat.lastActivity), chat.handleCell)
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

func anyCell(chats []chatOutput, pick func(chatOutput) string) bool {
	for _, chat := range chats {
		if strings.TrimSpace(pick(chat)) != "" {
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
