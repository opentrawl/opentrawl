package cli

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func TestHumanRenderersUseSharedComponents(t *testing.T) {
	useFixedLocalZone(t)
	t.Setenv("COLUMNS", "120")
	db, aliases := seedHumanOutputArchive(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "chats",
			args: []string{"--db", db, "chats"},
			want: lines(
				"Chats: showing 2 of 2, newest first.",
				"Messages: telecrawl messages --chat ID",
				"",
				"last              kind   unread  messages  chat id  name",
				"2026-07-02 14:03  group       0      500+  -100200  Project room",
				"2026-07-02 14:02  chat        1         2  100      Ada Example",
			),
		},
		{
			name: "topics",
			args: []string{"--db", db, "topics", "--chat", "-100200"},
			want: lines(
				"Topics: showing 1.",
				"",
				"last              unread  topic  title",
				"2026-07-02 14:03       2  10     Release planning",
			),
		},
		{
			name: "messages",
			args: []string{"--db", db, "messages", "--limit", "3"},
			want: lines(
				"Messages: showing 3 of 3, newest first.",
				"Open: telecrawl open REF",
				"",
				"date              who          where         ref    text",
				"2026-07-02 14:02  Bob Example  Project room  "+aliases["telecrawl:msg/3"]+"  project launch message with details",
				"2026-07-02 14:01  me           Ada Example   "+aliases["telecrawl:msg/2"]+"  reply from me",
				"2026-07-02 14:00  Ada Example  direct        "+aliases["telecrawl:msg/1"]+"  hello from ada",
			),
		},
		{
			name: "contacts",
			args: []string{"--db", db, "contacts"},
			want: lines(
				"Contacts: showing 2 of 2, A to Z.",
				"",
				"name         username     phone",
				"Ada Example  ada_example  +1555010200",
				"Bob Example  -            -",
			),
		},
		{
			name: "folders",
			args: []string{"--db", db, "folders"},
			want: lines(
				"id  title  count",
				"1   Work       2",
			),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v stderr=%s", strings.Join(tc.args, " "), err, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, tc.want)
			}
		})
	}
}

func TestSearchHumanResultsAndEmptyState(t *testing.T) {
	useFixedLocalZone(t)
	t.Setenv("COLUMNS", "120")
	db, aliases := seedHumanOutputArchive(t)

	stdout, stderr, err := runCLI(t, "--db", db, "search", "launch")
	if err != nil {
		t.Fatalf("search launch: %v stderr=%s", err, stderr)
	}
	want := lines(
		"Search \"launch\": showing 1 of 1.",
		"Open: telecrawl open REF",
		"",
		"date              who          where         ref    text",
		"2026-07-02 14:02  Bob Example  Project room  "+aliases["telecrawl:msg/3"]+"  project launch message with details",
	)
	if stdout != want {
		t.Fatalf("search stdout:\n%s\nwant:\n%s", stdout, want)
	}

	stdout, stderr, err = runCLI(t, "--db", db, "search", "missing")
	if err != nil {
		t.Fatalf("search missing: %v stderr=%s", err, stderr)
	}
	want = lines(
		"No matches for \"missing\".",
	)
	if stdout != want {
		t.Fatalf("empty search stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func TestTopicsAndWhoHumanEmptyStates(t *testing.T) {
	db, _ := seedHumanOutputArchive(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "topics", args: []string{"--db", db, "topics", "--chat", "100"}, want: "No topics: this chat has no forum topics.\n"},
		{name: "who", args: []string{"--db", db, "who", "nobody"}, want: "No people matched \"nobody\".\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, tc.args...)
			if err != nil {
				t.Fatalf("%s: %v stderr=%s", strings.Join(tc.args, " "), err, stderr)
			}
			if stdout != tc.want {
				t.Fatalf("stdout:\n%s\nwant:\n%s", stdout, tc.want)
			}
		})
	}
}

func TestHumanCommandsDoNotStartWithJSON(t *testing.T) {
	db, aliases := seedHumanOutputArchive(t)
	commands := [][]string{
		{"--db", db, "metadata"},
		{"--db", db, "status"},
		{"--db", db, "chats"},
		{"--db", db, "topics", "--chat", "-100200"},
		{"--db", db, "messages", "--limit", "1"},
		{"--db", db, "contacts"},
		{"--db", db, "folders"},
		{"--db", db, "search", "launch"},
		{"--db", db, "who", "nobody"},
		{"--db", db, "open", aliases["telecrawl:msg/3"]},
	}
	for _, args := range commands {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			stdout, stderr, err := runCLI(t, args...)
			if err != nil {
				t.Fatalf("%s: %v stderr=%s", strings.Join(args, " "), err, stderr)
			}
			trimmed := strings.TrimLeft(stdout, " \t\r\n")
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				t.Fatalf("human output starts with JSON:\n%s", stdout)
			}
		})
	}
}

func TestHumanRenderersRespectTerminalWidths(t *testing.T) {
	db, aliases := seedHumanOutputArchive(t)
	for _, width := range []string{"72", "100", "160"} {
		t.Run("columns "+width, func(t *testing.T) {
			t.Setenv("COLUMNS", width)
			for _, args := range [][]string{
				{"--db", db, "chats"},
				{"--db", db, "topics", "--chat", "-100200"},
				{"--db", db, "messages", "--limit", "3"},
				{"--db", db, "contacts"},
				{"--db", db, "folders"},
				{"--db", db, "search", "launch"},
				{"--db", db, "open", aliases["telecrawl:msg/3"]},
			} {
				stdout, stderr, err := runCLI(t, args...)
				if err != nil {
					t.Fatalf("%s: %v stderr=%s", strings.Join(args, " "), err, stderr)
				}
				assertLineWidths(t, stdout, atoi(width))
			}
		})
	}
}

func TestPrintRejectsMissingHumanRenderer(t *testing.T) {
	var out strings.Builder
	err := (&runtime{stdout: &out}).print(struct{ Value string }{Value: "missing"})
	if err == nil || !strings.Contains(err.Error(), "internal: no human renderer") {
		t.Fatalf("print error = %v, want missing renderer error", err)
	}
	if out.String() != "" {
		t.Fatalf("missing renderer wrote stdout:\n%s", out.String())
	}
}

func TestEmptyJSONListsUseArrays(t *testing.T) {
	db := seedEmptyArchive(t)
	for _, args := range [][]string{
		{"--db", db, "topics", "--chat", "100", "--json"},
		{"--db", db, "contacts", "--json"},
		{"--db", db, "folders", "--json"},
	} {
		t.Run(strings.Join(args[2:], " "), func(t *testing.T) {
			stdout, stderr, err := runCLI(t, args...)
			if err != nil {
				t.Fatalf("%s: %v stderr=%s", strings.Join(args, " "), err, stderr)
			}
			assertEmptyJSONArray(t, stdout)
		})
	}

	chatsOut, stderr, err := runCLI(t, "--db", db, "chats", "--json")
	if err != nil {
		t.Fatalf("chats json: %v stderr=%s", err, stderr)
	}
	assertJSONFieldIsEmptyArray(t, chatsOut, "chats")
	assertJSONEnvelopeTotals(t, chatsOut, 0, false)

	messagesOut, stderr, err := runCLI(t, "--db", db, "messages", "--json")
	if err != nil {
		t.Fatalf("messages json: %v stderr=%s", err, stderr)
	}
	assertJSONFieldIsEmptyArray(t, messagesOut, "messages")
	assertJSONEnvelopeTotals(t, messagesOut, 0, false)

	searchOut, stderr, err := runCLI(t, "--db", db, "search", "missing", "--json")
	if err != nil {
		t.Fatalf("search missing: %v stderr=%s", err, stderr)
	}
	assertJSONFieldIsEmptyArray(t, searchOut, "results")

	whoOut, stderr, err := runCLI(t, "--db", db, "who", "nobody", "--json")
	if err != nil {
		t.Fatalf("who nobody: %v stderr=%s", err, stderr)
	}
	assertJSONFieldIsEmptyArray(t, whoOut, "candidates")
}

func TestChatsAndMessagesJSONUseHumanContractShapes(t *testing.T) {
	useFixedLocalZone(t)
	db, aliases := seedHumanOutputArchive(t)

	chatsOut, stderr, err := runCLI(t, "--db", db, "chats", "--json")
	if err != nil {
		t.Fatalf("chats json: %v stderr=%s", err, stderr)
	}
	assertJSONEnvelopeTotals(t, chatsOut, 2, false)
	var chats struct {
		Chats []map[string]json.RawMessage `json:"chats"`
	}
	if err := json.Unmarshal([]byte(chatsOut), &chats); err != nil {
		t.Fatalf("chats json = %s err=%v", chatsOut, err)
	}
	assertJSONKeys(t, chats.Chats[0], []string{"chat_id", "kind", "name", "username", "last_message_at", "unread_count", "message_count", "message_count_capped", "forum"})
	assertJSONOmits(t, chats.Chats[0], []string{"jid", "chat_jid", "folder_id"})

	messagesOut, stderr, err := runCLI(t, "--db", db, "messages", "--limit", "3", "--json")
	if err != nil {
		t.Fatalf("messages json: %v stderr=%s", err, stderr)
	}
	assertJSONEnvelopeTotals(t, messagesOut, 3, false)
	var messages struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(messagesOut), &messages); err != nil {
		t.Fatalf("messages json = %s err=%v", messagesOut, err)
	}
	assertJSONKeys(t, messages.Messages[0], []string{"ref", "short_ref", "time", "who", "where", "text"})
	assertJSONOmits(t, messages.Messages[0], []string{"source_pk", "raw_type", "message_type", "chat_jid", "sender_jid", "reactions_json", "edit_timestamp"})
	var direct messageJSON
	if err := json.Unmarshal(mustJSONRaw(t, messages.Messages[2]), &direct); err != nil {
		t.Fatalf("direct message json = %#v err=%v", messages.Messages[2], err)
	}
	if direct.Ref != "telecrawl:msg/1" || direct.ShortRef != aliases["telecrawl:msg/1"] || direct.Where != "direct" || direct.Time != "2026-07-02T14:00:00+02:00" {
		t.Fatalf("direct message = %#v", direct)
	}
}

func TestTopLevelHelpUsesGroupedUsageDoc(t *testing.T) {
	stdout, stderr, err := runCLI(t, "--help")
	if err != nil {
		t.Fatalf("help: %v stderr=%s", err, stderr)
	}
	want := lines(
		"telecrawl: your Telegram archive: chats, messages and contacts",
		"",
		"Read your archive:",
		"  chats          Your chats, newest first.",
		"  topics         Forum topics in one chat.",
		"  messages       Archived messages, newest first.",
		"  contacts       People from the archive.",
		"  folders        Telegram folders from the archive.",
		"  search         Search archived messages.",
		"  open           Open one message with nearby context.",
		"  who            Resolve a person across handles and phones.",
		"",
		"Keep it fresh:",
		"  import         Read Telegram Desktop data into the archive.",
		"  backup         Manage encrypted archive backups.",
		"",
		"Health:",
		"  status         Show archive health and counts.",
		"  doctor         Check source access and archive readability.",
		"  metadata       Print the crawler manifest.",
		"  version        Print the telecrawl version.",
		"",
		"Global flags:",
		"  --json         Print machine-readable JSON output.",
		"  --db PATH      Archive database path.",
		"  --source PATH  Telegram source path for doctor and import.",
		"  -v, -vv        Log to stderr.",
		"",
		"Examples:",
		"  telecrawl chats --limit 20",
		"  telecrawl search \"launch\"",
		"  telecrawl open REF",
		"  telecrawl who \"alex\"",
		"",
		"Run 'telecrawl COMMAND --help' for flags and details.",
		"Logs: ~/.telecrawl/logs/telecrawl.log",
	)
	if stdout != want {
		t.Fatalf("help stdout:\n%s\nwant:\n%s", stdout, want)
	}
}

func seedHumanOutputArchive(t *testing.T) (string, map[string]string) {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	contacts := []store.Contact{
		{JID: "200", Phone: "+1555010200", FullName: "Ada Example", Username: "ada_example"},
		{JID: "300", FullName: "Bob Example"},
	}
	chats := []store.Chat{
		{JID: "100", Kind: "chat", Name: "Ada Example", LastMessageAt: now.Add(2 * time.Minute), UnreadCount: 1, MessageCount: 2, FolderID: "1"},
		{JID: "-100200", Kind: "group", Name: "Project room", LastMessageAt: now.Add(3 * time.Minute), MessageCount: telegramdesktop.DefaultMessagesLimit, FolderID: "1", Forum: true},
	}
	folders := []store.Folder{{ID: "1", Title: "Work"}}
	folderChats := []store.FolderChat{
		{FolderID: "1", ChatJID: "100", Position: 0},
		{FolderID: "1", ChatJID: "-100200", Position: 1},
	}
	topics := []store.Topic{{
		ChatJID:       "-100200",
		TopicID:       "10",
		Title:         "Release planning",
		UnreadCount:   2,
		LastMessageAt: now.Add(3 * time.Minute),
	}}
	messages := []store.Message{
		{SourcePK: 1, ChatJID: "100", ChatName: "Ada Example", MessageID: "0:1", SenderJID: "200", SenderName: "Ada Example", Timestamp: now, Text: "hello from ada"},
		{SourcePK: 2, ChatJID: "100", ChatName: "Ada Example", MessageID: "0:2", SenderJID: "999", SenderName: "Archive Owner", Timestamp: now.Add(time.Minute), FromMe: true, Text: "reply from me"},
		{SourcePK: 3, ChatJID: "-100200", ChatName: "Project room", MessageID: "0:3", SenderJID: "300", SenderName: "Bob Example", Timestamp: now.Add(2 * time.Minute), Text: "project launch message with details"},
	}
	if err := st.ReplaceAll(context.Background(), store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now}, contacts, chats, folders, folderChats, topics, nil, messages); err != nil {
		t.Fatal(err)
	}
	if err := st.RebuildShortRefs(context.Background()); err != nil {
		t.Fatal(err)
	}
	aliases, err := st.ShortRefsFor(context.Background(), []string{"telecrawl:msg/1", "telecrawl:msg/2", "telecrawl:msg/3"})
	if err != nil {
		t.Fatal(err)
	}
	return db, aliases
}

func seedEmptyArchive(t *testing.T) string {
	t.Helper()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertEmptyJSONArray(t *testing.T, stdout string) {
	t.Helper()
	var values []json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &values); err != nil {
		t.Fatalf("json = %s err=%v", stdout, err)
	}
	if values == nil {
		t.Fatalf("json array is nil, output was %s", stdout)
	}
	if len(values) != 0 {
		t.Fatalf("json array = %#v, want empty", values)
	}
	if strings.Contains(stdout, "null") {
		t.Fatalf("json output contains null:\n%s", stdout)
	}
}

func assertJSONFieldIsEmptyArray(t *testing.T, stdout string, field string) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("json = %s err=%v", stdout, err)
	}
	if strings.TrimSpace(string(root[field])) != "[]" {
		t.Fatalf("%s = %s, want [] in %s", field, root[field], stdout)
	}
}

func assertJSONEnvelopeTotals(t *testing.T, stdout string, total int, truncated bool) {
	t.Helper()
	var root struct {
		Total     int  `json:"total"`
		Truncated bool `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(stdout), &root); err != nil {
		t.Fatalf("json = %s err=%v", stdout, err)
	}
	if root.Total != total || root.Truncated != truncated {
		t.Fatalf("json totals = %#v, want total=%d truncated=%t in %s", root, total, truncated, stdout)
	}
}

func assertJSONKeys(t *testing.T, row map[string]json.RawMessage, want []string) {
	t.Helper()
	if len(row) != len(want) {
		t.Fatalf("keys = %#v, want %v", row, want)
	}
	for _, key := range want {
		if _, ok := row[key]; !ok {
			t.Fatalf("keys = %#v, missing %q", row, key)
		}
	}
}

func assertJSONOmits(t *testing.T, row map[string]json.RawMessage, forbidden []string) {
	t.Helper()
	for _, key := range forbidden {
		if _, ok := row[key]; ok {
			t.Fatalf("row leaked %q: %#v", key, row)
		}
	}
}

func mustJSONRaw(t *testing.T, row map[string]json.RawMessage) []byte {
	t.Helper()
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertLineWidths(t *testing.T, output string, width int) {
	t.Helper()
	for lineNo, line := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
		if got := render.DisplayWidth(line); got > width {
			t.Fatalf("line %d width = %d, want <= %d:\n%s", lineNo+1, got, width, output)
		}
	}
}

func atoi(value string) int {
	var out int
	for _, ch := range value {
		out = out*10 + int(ch-'0')
	}
	return out
}

func lines(values ...string) string {
	return strings.Join(values, "\n") + "\n"
}
