package trawlkit

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

// participantPreviewCap is how many participant names the participants column
// shows before it collapses the rest into "+N". Kept small so the preview fits
// one line beside the other columns without the table having to shrink and clip
// it; the head count carries everyone past the cap as an honest "+N". Three
// names, not two: a two-name preview felt too hidden for scanning who is in a
// group, and three still fits one line beside the other columns.
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

// newChatsOutput builds the rendered rows. aliases maps each chat Ref to its
// short ref from the shared index; the human chat column shows that short ref,
// so a long provider id never reaches a reader. Before the archive indexes a
// chat ref (an archive synced by an older binary), the column falls back to the
// source's DisplayID: a safe, copyable raw id where the source vouches for one,
// a privacy marker, or empty when the source has no handle safe to show. The
// raw Ref and ID never reach the human column; --json keeps both for agents.
func newChatsOutput(chats []Chat, aliases map[string]string, unread, truncated bool, with string) chatsOutput {
	rows := make([]chatOutput, 0, len(chats))
	for _, chat := range chats {
		rows = append(rows, chatOutput{
			ID:               chat.ID,
			Ref:              chat.Ref,
			Name:             chatDisplayName(chat),
			Kind:             kindLabel(chat.Group),
			Participants:     copyCount(chat.Participants),
			ParticipantNames: append([]string(nil), chat.ParticipantNames...),
			LastActivity:     formatContractTime(chat.LastActivity),
			Unread:           copyCount(chat.Unread),
			participantsCell: chatParticipantsCell(chat, with),
			// Short ref first; else the source's pre-index DisplayID (a safe raw id
			// or a privacy marker, empty when none). The raw Ref and ID stay in
			// --json only, never the human chat column.
			handleCell:   firstText(aliases[chat.Ref], chat.DisplayID),
			lastActivity: chat.LastActivity,
		})
	}
	return chatsOutput{Chats: rows, Truncated: truncated, unread: unread}
}

// chatDisplayName is the short identifier the name column shows. A real title
// wins; a dm with no title is named by its one other person; an unnamed group
// is named "group of N" and leaves its roster to the participants column, so
// the name column stays one scannable line and never wraps a long roster.
func chatDisplayName(chat Chat) string {
	if title := strings.TrimSpace(chat.Title); title != "" {
		return title
	}
	if !chat.Group {
		if preview := participantPreview(chat.ParticipantNames, chat.Participants); preview != "" {
			return preview
		}
		return "chat"
	}
	if chat.Participants != nil && *chat.Participants > 0 {
		return "group of " + render.FormatInteger(*chat.Participants)
	}
	return "group chat"
}

// chatParticipantsCell fills the participants column with the roster of any
// group that resolved names, named or not. A dm's one participant is already
// its name, so the dm row leaves the cell blank. Under --with, the matched
// person is pulled to the front so a "chats with X" result always shows X in
// the preview rather than collapsing them into "+N".
func chatParticipantsCell(chat Chat, with string) string {
	if !chat.Group {
		return ""
	}
	names := chat.ParticipantNames
	if strings.TrimSpace(with) != "" {
		names = surfaceMatchedName(names, with)
	}
	return participantPreview(names, chat.Participants)
}

// surfaceMatchedName returns the roster with the first name that matches --with
// moved to the front, so the capped preview shows the person the reader searched
// for. It copies rather than reordering in place, so the JSON participant_names
// keep their source order and stay the honest full roster.
func surfaceMatchedName(names []string, with string) []string {
	for i, name := range names {
		if !matchesName(with, []string{name}) {
			continue
		}
		if i == 0 {
			return names
		}
		reordered := make([]string, 0, len(names))
		reordered = append(reordered, name)
		reordered = append(reordered, names[:i]...)
		reordered = append(reordered, names[i+1:]...)
		return reordered
	}
	return names
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
		// A name is an identifier, not prose: it stays on one line and clips with
		// an ellipsis under pressure, the way a chat list does, rather than
		// wrapping a title across rows mid-list.
		{Header: "name"},
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
