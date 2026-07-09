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

// Chat is one conversation on any messaging surface, in the six fields every
// serious unified inbox converges on. Participants and Unread are optional: a
// surface that does not store the fact leaves the pointer nil, so the column
// and JSON field drop out rather than reporting a fake zero.
type Chat struct {
	ID    string
	Title string
	// Group reports whether the chat has more than two people. The kit renders
	// the one true two-value vocabulary ("group" or "dm") from this bool, so no
	// surface can leak its own word ("direct", "user", "channel") into the field.
	Group bool
	// DisplayID is the id as shown in the human table. It exists only so a
	// surface can mask a privacy-sensitive id (a WhatsApp @lid) from a human
	// reader while --json keeps the real ID that messages --chat needs. Empty
	// means show ID unchanged.
	DisplayID    string
	Participants *int64
	LastActivity time.Time
	Unread       *int64
}

// ErrChatsNoReadState is returned by a ChatLister when --unread is requested
// against an archive that holds no read state, for example one synced before
// the surface ingested it. The runner turns it into a clean usage error that
// names the surface.
var ErrChatsNoReadState = errors.New("chats: archive holds no read state")
