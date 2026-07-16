package trawlkit

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/shortref"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

// testChatCrawler is a minimal ChatLister: it owns only the store-query hook so
// the kit's shared flag parsing, JSON envelope and human table are what the
// tests actually exercise.
type testChatCrawler struct {
	testStatusCrawler
	chatsFn func(context.Context, *Request, ChatQuery) ([]Chat, error)
}

func (c *testChatCrawler) Chats(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
	return c.chatsFn(ctx, req, q)
}

func int64Ptr(n int64) *int64 { return &n }

func TestRunChatsJSONEnvelopeAndFlags(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	var got ChatQuery
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		got = q
		return []Chat{{
			ID:               "42139272",
			Ref:              "telegram:chat/42139272",
			Title:            "Weekend Plans",
			Group:            true,
			Participants:     int64Ptr(4),
			ParticipantNames: []string{"Ada", "Bo", "Cy"},
			Unread:           int64Ptr(7),
			LastActivity:     time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		}}, nil
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("default chats code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// The crawler sees the page limit plus the one-row truncation probe.
	if got.Limit != defaultChatLimit+1 || got.All || got.Unread {
		t.Fatalf("default query = %#v", got)
	}
	var envelope struct {
		Chats []struct {
			ID               string   `json:"id"`
			Ref              string   `json:"ref"`
			Name             string   `json:"name"`
			Kind             string   `json:"kind"`
			Participants     *int64   `json:"participants"`
			ParticipantNames []string `json:"participant_names"`
			LastActivity     string   `json:"last_activity"`
			Unread           *int64   `json:"unread"`
		} `json:"chats"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("chats json = %s err=%v", stdout, err)
	}
	if len(envelope.Chats) != 1 {
		t.Fatalf("chats envelope = %#v", envelope)
	}
	row := envelope.Chats[0]
	// --json keeps the raw source key messages --chat accepts, and carries the
	// ref the human table shows so an agent can act from either.
	if row.ID != "42139272" || row.Ref != "telegram:chat/42139272" {
		t.Fatalf("chat row handles = %#v", row)
	}
	if row.Name != "Weekend Plans" || row.Kind != "group" {
		t.Fatalf("chat row identity = %#v", row)
	}
	if row.Participants == nil || *row.Participants != 4 || row.Unread == nil || *row.Unread != 7 {
		t.Fatalf("chat row counts = %#v", row)
	}
	if len(row.ParticipantNames) != 3 || row.ParticipantNames[0] != "Ada" {
		t.Fatalf("chat row participant_names = %#v", row.ParticipantNames)
	}
	if row.LastActivity != "2026-07-02T12:00:00Z" {
		t.Fatalf("chat row last_activity = %q", row.LastActivity)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json", "--limit", "3", "--unread"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("flagged chats code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if got.Limit != 4 || got.All || !got.Unread {
		t.Fatalf("flagged query = %#v", got)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json", "--all"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("all chats code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !got.All || got.Limit != 0 {
		t.Fatalf("all query = %#v", got)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json", "leftover"}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "chats takes flags only") || stderr != "" {
		t.Fatalf("positional arg code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json", "--limit", "0"}, source, runOptions{})
	if code != 2 || !strings.Contains(stdout, "--limit must be at least 1") || stderr != "" {
		t.Fatalf("bad limit code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

// In a mixed list, a dm carries no group member list: the participants column shows
// its "-" marker on the dm row while the named group beside it shows its member list,
// so the column stays honest without inventing a dm's members.
func TestRunChatsTextMixedDMAndGroupParticipants(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{
			{
				ID:               "g1",
				Ref:              "imessage:chat/g1",
				Title:            "Book Club",
				Group:            true,
				Participants:     int64Ptr(3),
				ParticipantNames: []string{"Ada", "Bo", "Cy"},
				LastActivity:     time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
			},
			{
				ID:           "d1",
				Ref:          "imessage:chat/d1",
				Title:        "Ada Example",
				Group:        false,
				Participants: int64Ptr(2),
				LastActivity: time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC),
			},
		}, nil
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// Cap is 3 names; a 3-person group shows its whole member list with no remainder.
	if !strings.Contains(stdout, "Ada, Bo, Cy") {
		t.Fatalf("group must show its member list:\n%s", stdout)
	}
	// The dm's participant cell is the "-" marker, never a fabricated member list.
	dmLine := ""
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "Ada Example") {
			dmLine = line
		}
	}
	if dmLine == "" || !strings.Contains(dmLine, " - ") {
		t.Fatalf("dm row must show the empty participants marker:\n%s", stdout)
	}
	// Neither row has an indexed short ref or a DisplayID, so the chat column is
	// blank rather than leaking the raw "imessage:chat/..." provider ref.
	if strings.Contains(stdout, "imessage:chat/") {
		t.Fatalf("chat column must not leak the provider ref when unindexed:\n%s", stdout)
	}
}

// The name leads and the raw source key never does: a named group shows its
// title in the name column and its member list in the participants column, while the
// chat column carries the short pre-index handle a reader pastes into messages
// --chat, never the long "telegram:chat/..." provider ref. The head count
// collapses the member list past the cap into an honest "+N".
func TestRunChatsTextNameLeadsAndParticipantsColumn(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID:               "-4118982174",
			Ref:              "telegram:chat/-4118982174",
			DisplayID:        "-4118982174",
			Title:            "padel wankers",
			Group:            true,
			Participants:     int64Ptr(8),
			ParticipantNames: []string{"Ana", "Bob", "Cy", "Dee"},
			Unread:           int64Ptr(1),
			LastActivity:     time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		}}, nil
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	header := firstLineAfterBlank(stdout)
	if !strings.HasPrefix(header, "name") {
		t.Fatalf("name must lead the table, got header %q:\n%s", header, stdout)
	}
	if strings.Index(header, "chat") <= strings.Index(header, "name") {
		t.Fatalf("chat (ref) column must trail the name, header %q", header)
	}
	if !strings.Contains(stdout, "participants") {
		t.Fatalf("named group must show a participants column:\n%s", stdout)
	}
	if !strings.Contains(stdout, "padel wankers") {
		t.Fatalf("missing chat name:\n%s", stdout)
	}
	// Cap is 3 names; the 8-person head count makes the remainder "+5".
	if !strings.Contains(stdout, "Ana, Bob, Cy +5") {
		t.Fatalf("participants preview wrong:\n%s", stdout)
	}
	// The chat column shows the source's safe pre-index handle (the raw id),
	// never the long "telegram:chat/..." provider ref.
	if !strings.Contains(stdout, "-4118982174") {
		t.Fatalf("chat column must show the pre-index handle:\n%s", stdout)
	}
	if strings.Contains(stdout, "telegram:chat/") {
		t.Fatalf("chat column must not show the long provider ref:\n%s", stdout)
	}
}

// An unnamed group (common on iMessage) keeps a short one-line name, "group of
// N", and shows its member list in the participants column, so the name never wraps a
// long member list across rows. A source with no read state hides the unread column.
func TestRunChatsTextUnnamedGroupNameAndMembers(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID:               "970057",
			Ref:              "imessage:chat/970057",
			DisplayID:        "970057",
			Title:            "",
			Group:            true,
			Participants:     int64Ptr(5),
			ParticipantNames: []string{"Alice", "Bob", "Carol", "Dan"},
			LastActivity:     time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		}}, nil
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "group of 5") {
		t.Fatalf("unnamed group must be named 'group of N':\n%s", stdout)
	}
	// The member list lives in the participants column, capped at 3 with an honest "+2".
	if !strings.Contains(stdout, "participants") || !strings.Contains(stdout, "Alice, Bob, Carol +2") {
		t.Fatalf("unnamed group must show its member list in the participants column:\n%s", stdout)
	}
	if strings.Contains(stdout, "unread") {
		t.Fatalf("unread column must be hidden when no chat carries a count:\n%s", stdout)
	}
	// The chat column shows the source's safe pre-index handle (the raw rowid),
	// never the long "imessage:chat/..." provider ref.
	if !strings.Contains(stdout, "970057") {
		t.Fatalf("chat column must show the pre-index handle:\n%s", stdout)
	}
	if strings.Contains(stdout, "imessage:chat/") {
		t.Fatalf("chat column must not show the long provider ref:\n%s", stdout)
	}
}

// A WhatsApp-shaped source counts unread but not participants, and masks a
// privacy @lid in the human table's chat column while --json keeps the real id
// and ref that messages --chat needs.
func TestRunChatsTextMasksDisplayIDButJSONKeepsRealID(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID:           "155500000000002@lid",
			Ref:          "whatsapp:chat/155500000000002@lid",
			Title:        "unknown participant (privacy id)",
			Group:        false,
			DisplayID:    "privacy id",
			Unread:       int64Ptr(3),
			LastActivity: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		}}, nil
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "@lid") {
		t.Fatalf("human table leaked the raw privacy id:\n%s", stdout)
	}
	if !strings.Contains(stdout, "privacy id") {
		t.Fatalf("human table missing the display mask in the chat column:\n%s", stdout)
	}
	if !strings.Contains(stdout, "unread") {
		t.Fatalf("expected unread column:\n%s", stdout)
	}
	if strings.Contains(stdout, "participants") {
		t.Fatalf("participants column must be hidden for a source with no member list:\n%s", stdout)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats json code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, `"id": "155500000000002@lid"`) || !strings.Contains(stdout, `"ref": "whatsapp:chat/155500000000002@lid"`) {
		t.Fatalf("json must keep the real id and ref for messages --chat:\n%s", stdout)
	}
	// The human-only mask replaces the ref in the chat column, never the JSON
	// handles; the id and ref above are the real ones, not "privacy id".
	if strings.Contains(stdout, `"ref": "privacy id"`) || strings.Contains(stdout, `"id": "privacy id"`) {
		t.Fatalf("json must not carry the human-only display mask:\n%s", stdout)
	}
}

// Once the archive indexes a chat ref, the human chat column shows its short
// ref, not the long provider id, so a reader copies the short ref straight into
// messages --chat. --json still keeps the raw id and full ref.
func TestRunChatsTextChatColumnShowsShortRef(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	ref := "whatsapp:chat/15550001111-1700000000@g.us"
	alias := seedShortRef(t, stateRoot, ref)

	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID:           "15550001111-1700000000@g.us",
			Ref:          ref,
			Title:        "Neighbourhood group",
			Group:        true,
			LastActivity: time.Date(2026, 7, 4, 14, 17, 0, 0, time.UTC),
		}}, nil
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, alias) {
		t.Fatalf("chat column must show the short ref %q:\n%s", alias, stdout)
	}
	if strings.Contains(stdout, ref) || strings.Contains(stdout, "@g.us") {
		t.Fatalf("chat column must not show the long provider id:\n%s", stdout)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats json code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// --json is the script contract: the raw id and full ref stay, the short ref
	// (a human display alias) never leaks in.
	if !strings.Contains(stdout, `"ref": "`+ref+`"`) || !strings.Contains(stdout, `"id": "15550001111-1700000000@g.us"`) {
		t.Fatalf("json must keep the raw id and full ref:\n%s", stdout)
	}
	if strings.Contains(stdout, alias) {
		t.Fatalf("json must not carry the display short ref:\n%s", stdout)
	}
}

// --with filters to the chats a named person is in. The match spans a group's
// resolved member list and a dm's partner name, is case-insensitive, and drops every
// chat that person is not in. The kit fetches every chat (--all) so a match past
// the first page is never lost, and pages the survivors itself.
func TestRunChatsWithFiltersByParticipant(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	var got ChatQuery
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		got = q
		return []Chat{
			{
				ID: "g1", Ref: "imessage:chat/g1", Title: "Book Club", Group: true,
				Participants: int64Ptr(3), ParticipantNames: []string{"Ada", "Zoe", "Bo"},
				LastActivity: time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC),
			},
			{
				// A dm whose partner name lives in the title (Telegram/WhatsApp shape),
				// with no member list: --with must still find it by the title.
				ID: "d1", Ref: "telegram:chat/d1", Title: "Zoe Example", Group: false,
				LastActivity: time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
			},
			{
				ID: "g2", Ref: "imessage:chat/g2", Title: "Padel", Group: true,
				Participants: int64Ptr(2), ParticipantNames: []string{"Ada", "Bo"},
				LastActivity: time.Date(2026, 7, 2, 9, 0, 0, 0, time.UTC),
			},
		}, nil
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats", "--with", "zoe"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats --with code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// The kit asks the source for every chat, then filters in the kit.
	if !got.All || got.Limit != 0 || got.With != "zoe" {
		t.Fatalf("with query must fetch all and carry the person: %#v", got)
	}
	if !strings.Contains(stdout, "Book Club") || !strings.Contains(stdout, "Zoe Example") {
		t.Fatalf("both chats with Zoe must appear:\n%s", stdout)
	}
	if strings.Contains(stdout, "Padel") {
		t.Fatalf("a chat without Zoe must be dropped:\n%s", stdout)
	}

	// A discovery filter stays literal: a close-spelled query must not pull in a
	// different person. "Zoa" is one edit from "Zoe" (the resolver would treat it
	// as a close-spelling match), but it is nobody in this list, so --with drops it.
	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--with", "Zoa"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats --with Zoa code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "No chats") {
		t.Fatalf("close-spelled query must not match a different name:\n%s", stdout)
	}
}

// --with never matches or prints a raw privacy handle. A WhatsApp @lid chat with
// no resolved name is not findable by its handle digits and never leaks the raw
// id, while a real name in the same list still matches.
func TestRunChatsWithNeverLeaksPrivacyID(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{
			{
				// Masked dm: the raw @lid is machine-only; the human title is a mask.
				ID: "155500000000002@lid", Ref: "whatsapp:chat/155500000000002@lid",
				Title: "unknown participant (privacy id)", Group: false, DisplayID: "privacy id",
				LastActivity: time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC),
			},
			{
				ID: "g1", Ref: "whatsapp:chat/g1", Title: "Padel", Group: true,
				Participants: int64Ptr(2), ParticipantNames: []string{"Ada", "Bo"},
				LastActivity: time.Date(2026, 7, 3, 9, 0, 0, 0, time.UTC),
			},
		}, nil
	}}

	// The raw handle digits find nothing and never surface the @lid.
	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats", "--with", "155500000000002"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats --with code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "@lid") {
		t.Fatalf("--with must never print a raw privacy id:\n%s", stdout)
	}
	if !strings.Contains(stdout, "No chats") {
		t.Fatalf("a privacy handle must match nothing:\n%s", stdout)
	}

	// A real name still matches by name in the same archive.
	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--with", "ada"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats --with ada code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Padel") || strings.Contains(stdout, "@lid") {
		t.Fatalf("a named participant must match without leaking a handle:\n%s", stdout)
	}
}

// Under --with, the matched person is pulled to the front of the capped preview,
// so a "chats with X" result shows X rather than collapsing them into "+N". The
// JSON member list keeps its source order and full membership.
func TestRunChatsWithSurfacesMatchPastCap(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID: "g1", Ref: "imessage:chat/g1", Title: "Big Group", Group: true,
			Participants:     int64Ptr(6),
			ParticipantNames: []string{"Ada", "Bo", "Cy", "Dee", "Zoe", "El"},
			LastActivity:     time.Date(2026, 7, 4, 9, 0, 0, 0, time.UTC),
		}}, nil
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats", "--with", "zoe"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats --with code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// Zoe is the 5th name, past the 3-name cap; the match surfaces her first.
	if !strings.Contains(stdout, "Zoe, Ada, Bo +3") {
		t.Fatalf("matched person must lead the preview:\n%s", stdout)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--with", "zoe", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats --with --json code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	// The JSON member list is the honest full list in source order, not reordered.
	if !strings.Contains(stdout, `"Ada",`) || !strings.Contains(stdout, `"Zoe",`) {
		t.Fatalf("json must keep the full member list:\n%s", stdout)
	}
	var envelope struct {
		Chats []struct {
			ParticipantNames []string `json:"participant_names"`
		} `json:"chats"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("json parse: %v\n%s", err, stdout)
	}
	if len(envelope.Chats) != 1 || len(envelope.Chats[0].ParticipantNames) != 6 || envelope.Chats[0].ParticipantNames[0] != "Ada" {
		t.Fatalf("json member list must keep source order and full membership: %#v", envelope.Chats)
	}
}

// seedShortRef indexes one ref in the archive the test source reads, the way a
// sync would, and returns the display alias the chat column should show.
func seedShortRef(t *testing.T, stateRoot, ref string) string {
	t.Helper()
	path := filepath.Join(stateRoot, "testcrawl", "testcrawl.db")
	st, err := ckstore.Open(context.Background(), ckstore.Options{Path: path, Schema: shortref.Schema})
	if err != nil {
		t.Fatal(err)
	}
	req := &Request{Store: st}
	if _, err := req.AssignShortRefs(context.Background(), []ShortRefRecord{{Ref: ref}}); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := shortref.BuildSlice([]string{ref})
	if err != nil || len(entries) != 1 {
		t.Fatalf("build alias: %v entries=%d", err, len(entries))
	}
	return entries[0].Alias
}

// firstLineAfterBlank returns the table header: the first non-empty line after
// the blank line that follows the "Chats: showing ..." heading.
func firstLineAfterBlank(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			for _, rest := range lines[i+1:] {
				if strings.TrimSpace(rest) != "" {
					return rest
				}
			}
		}
	}
	return ""
}

func TestRunChatsTextEmptyAndUnreadEmpty(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return nil, nil
	}}

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 || strings.TrimSpace(stdout) != "No chats." || stderr != "" {
		t.Fatalf("empty chats code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--unread"}, source, runOptions{})
	if code != 0 || strings.TrimSpace(stdout) != "No unread chats." || stderr != "" {
		t.Fatalf("empty unread code=%d stdout=%q stderr=%s", code, stdout, stderr)
	}
}

// Truncation is exact: the kit fetches one row past the page, so the hint
// appears only when a chat truly fell off the end. An archive holding exactly
// --limit chats shows no hint, and the extra probe row is never rendered.
func TestRunChatsTruncationIsExact(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	newSource := func(total int) *testChatCrawler {
		return &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
			n := total
			if q.Limit > 0 && q.Limit < n {
				n = q.Limit
			}
			rows := make([]Chat, n)
			for i := range rows {
				rows[i] = Chat{ID: "c", Title: "Chat", Group: true, LastActivity: time.Unix(0, 0).UTC()}
			}
			return rows, nil
		}}
	}
	const hint = "More: raise --limit, or list all with --all"

	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats", "--limit", "2"}, newSource(3), runOptions{})
	if code != 0 {
		t.Fatalf("truncated chats code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, hint) {
		t.Fatalf("missing truncation hint:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Chats: showing 2, newest first.") {
		t.Fatalf("probe row leaked into the rendered page:\n%s", stdout)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--limit", "2"}, newSource(2), runOptions{})
	if code != 0 {
		t.Fatalf("exact-page chats code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, hint) {
		t.Fatalf("hint shown though the archive holds exactly --limit chats:\n%s", stdout)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json", "--limit", "2"}, newSource(3), runOptions{})
	if code != 0 {
		t.Fatalf("truncated json code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var envelope struct {
		Chats     []struct{} `json:"chats"`
		Truncated bool       `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("chats json = %s err=%v", stdout, err)
	}
	if len(envelope.Chats) != 2 || !envelope.Truncated {
		t.Fatalf("json truncation = %#v", envelope)
	}
}

// A surface with no read state turns --unread into a clean usage error that
// names the surface, never a raw sentinel or stack.
func TestRunChatsUnreadUnsupportedIsUsageError(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return nil, ErrChatsNoReadState
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats", "--json", "--unread"}, source, runOptions{})
	if code != 2 {
		t.Fatalf("unsupported unread code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "no read state") || !strings.Contains(stdout, "--unread is not available") {
		t.Fatalf("usage error text = %s", stdout)
	}
}
