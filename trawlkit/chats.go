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
	// With is the runner-owned --with person filter. It is empty by default. A
	// crawler never reads it: the kit filters the crawler's chats by participant
	// name itself, so every source gets the same source-agnostic matching. It is
	// carried on the query only so a crawler that fetches from its own store sees
	// the same struct the kit parsed.
	With string
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
	// Ref is the source-scoped handle built the same way a message ref is, e.g.
	// "telegram:chat/42139272". The source fills it; the kit indexes it in the
	// shared short-ref table and shows the reader its short ref in the chat
	// column, so the reader never sees a long provider id and can paste the
	// short ref straight into messages --chat.
	Ref string
	// Title is the real, clean conversation name the surface stores: the group
	// subject, the dm partner. It is empty when the surface has no name to show
	// (a common iMessage group), and the kit names the chat "group of N"
	// instead, moving the member list to the participants column. A surface must not
	// pass a machine id or a raw handle here; those belong in ID.
	Title string
	// Group reports whether the chat has more than two people. The kit renders
	// the one true two-value vocabulary ("group" or "dm") from this bool, so no
	// surface can leak its own word ("direct", "user", "channel") into the field.
	Group bool
	// DisplayID is what the human chat column shows before the archive indexes
	// this chat's short ref (an archive synced by an older binary). The kit never
	// prints the raw ID or Ref to a human, so a surface whose raw ID is safe to
	// show sets DisplayID to it (an iMessage rowid, a Telegram peer id) to keep a
	// copyable handle in the window before indexing; a surface whose raw ID is
	// sensitive sets a privacy marker (a WhatsApp @lid) or leaves it empty, and
	// the column is simply blank until the next sync fills in the short ref. The
	// kit prefers the short ref once indexed. --json always keeps the real ID and
	// Ref, never this display value.
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
