package trawlkit

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
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
			ID:           "chat-1",
			Title:        "Weekend Plans",
			Group:        true,
			Participants: int64Ptr(4),
			Unread:       int64Ptr(7),
			LastActivity: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
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
			ID           string `json:"id"`
			Title        string `json:"title"`
			Kind         string `json:"kind"`
			Participants *int64 `json:"participants"`
			LastActivity string `json:"last_activity"`
			Unread       *int64 `json:"unread"`
		} `json:"chats"`
	}
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("chats json = %s err=%v", stdout, err)
	}
	if len(envelope.Chats) != 1 {
		t.Fatalf("chats envelope = %#v", envelope)
	}
	row := envelope.Chats[0]
	if row.ID != "chat-1" || row.Title != "Weekend Plans" || row.Kind != "group" {
		t.Fatalf("chat row identity = %#v", row)
	}
	if row.Participants == nil || *row.Participants != 4 || row.Unread == nil || *row.Unread != 7 {
		t.Fatalf("chat row counts = %#v", row)
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

// The people and unread columns appear only when the surface fills them: an
// iMessage-shaped source that counts participants but stores no read state
// shows people and hides unread.
func TestRunChatsTextShowsParticipantsHidesMissingUnread(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID:           "iMessage;-;+15550100",
			Title:        "Ada Example",
			Group:        false,
			Participants: int64Ptr(2),
			LastActivity: time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC),
		}}, nil
	}}
	code, stdout, stderr := runForTestAt(stateRoot, []string{"chats"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats text code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "Chats: showing 1, newest first.") {
		t.Fatalf("missing heading:\n%s", stdout)
	}
	if !strings.Contains(stdout, "people") {
		t.Fatalf("expected people column:\n%s", stdout)
	}
	if strings.Contains(stdout, "unread") {
		t.Fatalf("unread column must be hidden when no chat carries a count:\n%s", stdout)
	}
	if !strings.Contains(stdout, "Ada Example") {
		t.Fatalf("missing chat title:\n%s", stdout)
	}
}

// A WhatsApp-shaped source counts unread but not participants, and masks a
// privacy id in the human table while --json keeps the real id.
func TestRunChatsTextMasksDisplayIDButJSONKeepsRealID(t *testing.T) {
	stateRoot := t.TempDir()
	createArchive(t, stateRoot)
	source := &testChatCrawler{chatsFn: func(ctx context.Context, req *Request, q ChatQuery) ([]Chat, error) {
		return []Chat{{
			ID:           "155500000000002@lid",
			Title:        "unknown participant",
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
		t.Fatalf("human table missing the display mask:\n%s", stdout)
	}
	if !strings.Contains(stdout, "unread") {
		t.Fatalf("expected unread column:\n%s", stdout)
	}
	if strings.Contains(stdout, "people") {
		t.Fatalf("people column must be hidden when no chat carries a count:\n%s", stdout)
	}

	code, stdout, stderr = runForTestAt(stateRoot, []string{"chats", "--json"}, source, runOptions{})
	if code != 0 {
		t.Fatalf("chats json code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, "155500000000002@lid") {
		t.Fatalf("json must keep the real id for messages --chat:\n%s", stdout)
	}
	if strings.Contains(stdout, "privacy id") {
		t.Fatalf("json must not carry the human-only display mask:\n%s", stdout)
	}
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
	if !strings.Contains(stdout, "stores no read state") || !strings.Contains(stdout, "--unread is not available") {
		t.Fatalf("usage error text = %s", stdout)
	}
}
