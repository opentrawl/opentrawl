package trawlkit

import (
	"errors"
	"time"
)

// ChatQuery carries the runner-owned flags for the chats verb. The kit parses
// them once from --limit, --all and --unread; a crawler never re-parses them.
type ChatQuery struct {
	// Limit caps the number of chats returned. Zero means no cap; the runner
	// sets it to zero when --all is given.
	Limit  int
	All    bool
	Unread bool
}

// Chat is one conversation on any messaging surface. A surface reports the
// facts it stores; the kit owns all human formatting (the display name, the
// participant preview, the ref). Participants, ParticipantNames and Unread are
// optional: a surface that does not store the fact leaves them empty, so the
// column and JSON field drop out rather than reporting a fake zero.
type Chat struct {
	// ID is the raw source key that messages --chat accepts: an Apple chat.db
	// rowid, a Telegram peer id, a WhatsApp JID. It is machine-only; --json
	// keeps it, the human table never leads with it.
	ID string
	// Ref is the source-scoped handle a reader copies from the chats table into
	// messages --chat, e.g. "telegram:chat/42139272", built the same way a
	// message ref is. The source fills it; it is the human table's chat column.
	Ref string
	// Title is the real, clean conversation name the surface stores: the group
	// subject, the dm partner. It is empty when the surface has no name to show
	// (a common iMessage group), and the kit synthesises a name from the
	// participants instead. A surface must not pass a machine id or a raw handle
	// here; those belong in ID.
	Title string
	// Group reports whether the chat has more than two people. The kit renders
	// the one true two-value vocabulary ("group" or "dm") from this bool, so no
	// surface can leak its own word ("direct", "user", "channel") into the field.
	Group bool
	// DisplayID masks the chat column in the human table. It exists only so a
	// surface can hide a privacy-sensitive handle (a WhatsApp @lid) from a human
	// reader while --json keeps the real ID and Ref that messages --chat needs.
	// Empty means show the ref unchanged.
	DisplayID string
	// Participants is the total head count, nil when the surface does not store
	// it. ParticipantNames are the resolved display names the surface could
	// resolve; the kit caps and joins them. A surface that resolves fewer names
	// than Participants still gets an honest "+N" from the count.
	Participants     *int64
	ParticipantNames []string
	LastActivity     time.Time
	Unread           *int64
}

// ErrChatsNoReadState is returned by a ChatLister when --unread is requested
// against an archive that holds no read state, for example one synced before
// the surface ingested it. The runner turns it into a clean usage error that
// names the surface.
var ErrChatsNoReadState = errors.New("chats: archive holds no read state")
