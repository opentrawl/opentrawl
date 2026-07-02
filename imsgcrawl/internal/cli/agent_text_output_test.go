package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func TestArchiveTextOutputIsAgentReadable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "sync")

	chats := runOK(t, "--archive", archivePath, "chats", "--limit", "2")
	assertTextContains(t, chats,
		"Chats: showing 2 of 4, newest first.",
		"More: imsgcrawl chats --limit 4",
		"All: imsgcrawl chats --all",
		"Open: imsgcrawl messages --chat CHAT_ID",
		"chat",
		"kind",
		"conversation",
		"group",
		"Cabinet Group",
		"Fixture Person",
		"opaque-handle",
	)
	assertNotSecretJSON(t, chats)

	messages := runOK(t, "--archive", archivePath, "messages", "--chat", "2", "--limit", "1")
	assertTextContains(t, messages,
		"Messages in Most Recent Name (chat 2): showing 1 of 2, newest-first.",
		"More: imsgcrawl messages --chat 2 --limit 2",
		"All: imsgcrawl messages --chat 2 --all",
		"date",
		"from",
		"text",
		"me",
		"latest launch note",
		"full tail marker",
	)
	if strings.Contains(messages, "[3]") || strings.Contains(messages, "message_id") {
		t.Fatalf("messages text leaked unlabeled message IDs:\n%s", messages)
	}
	if strings.Contains(messages, "service") {
		t.Fatalf("messages text kept low-value service column:\n%s", messages)
	}
	assertNotSecretJSON(t, messages)

	search := runOK(t, "--archive", archivePath, "search", "--limit", "1", "launch")
	assertTextContains(t, search,
		"Search \"launch\": showing 1 of 2.",
		"More: imsgcrawl search --limit 2 \"launch\"",
		"Open: imsgcrawl open REF",
		"Use --json when you need refs for follow-up commands.",
		"launch note",
		"conversation",
		"text",
		"Most Recent Name",
	)
	if strings.Contains(search, "[3]") || strings.Contains(search, "message_id") {
		t.Fatalf("search text leaked unlabeled message IDs:\n%s", search)
	}
	if strings.Contains(search, "\n#") || strings.Contains(search, "\n1.") || strings.Contains(search, "\t2\t") {
		t.Fatalf("search text kept raw result numbers or chat ID table shape:\n%s", search)
	}
	assertNotSecretJSON(t, search)
	conformance.AssertHumanOutput(t, search)

	searchJSON := runOK(t, "--archive", archivePath, "--json", "search", "--limit", "1", "launch")
	var searchPayload searchListJSON
	if err := json.Unmarshal([]byte(searchJSON), &searchPayload); err != nil {
		t.Fatalf("search json = %s err=%v", searchJSON, err)
	}
	if len(searchPayload.Results) != 1 {
		t.Fatalf("search json results = %#v", searchPayload.Results)
	}
	open := runOK(t, "--archive", archivePath, "open", searchPayload.Results[0].Ref)
	assertTextContains(t, open,
		"Message "+searchPayload.Results[0].Ref+" in Most Recent Name",
		"Context:",
		"earlier launch note",
		"latest launch note",
	)
	assertNotSecretJSON(t, open)

	directSender := runOK(t, "--archive", archivePath, "messages", "--chat", "2", "--asc", "--limit", "1")
	assertTextContains(t, directSender, "Most Recent Name")

	groupSender := runOK(t, "--archive", archivePath, "messages", "--chat", "4", "--asc")
	assertTextContains(t, groupSender, "Cabinet Group", "Fixture Person", "opaque-handle")
}

func TestMetadataAndSyncTextOutputIsAgentReadable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)

	metadata := runOK(t, "--db", dbPath, "metadata")
	assertTextContains(t, metadata,
		"iMessage (imsgcrawl)",
		"Agent-facing commands:",
		"status",
		"Machine output: add --json",
	)
	assertNotSecretJSON(t, metadata)

	syncOut := runOK(t, "--db", dbPath, "--archive", archivePath, "sync")
	assertTextContains(t, syncOut,
		"Sync complete",
		"Messages source:",
		"Local archive:",
		"Chats: 4",
		"Messages: 5",
	)
	assertNotSecretJSON(t, syncOut)

	status := runOK(t, "--db", dbPath, "--archive", archivePath, "status")
	assertTextContains(t, status,
		"Status: ok",
		"Messages source:",
		"Local archive:",
		"Messages: 5",
	)
	conformance.AssertHumanOutput(t, status)

	doctor := runOK(t, "--db", dbPath, "--archive", archivePath, "doctor")
	assertTextContains(t, doctor,
		"Doctor checks:",
		"source_store: ok",
		"archive: ok",
		"full_disk_access: ok",
	)
	conformance.AssertHumanOutput(t, doctor)
}

func TestDisplayMessageTextNormalizesAttachmentPlaceholder(t *testing.T) {
	if got := displayMessageText("\uFFFC", true); got != "(attachment)" {
		t.Fatalf("attachment-only text = %q", got)
	}
	if got := displayMessageText("photo \uFFFC attached", true); got != "photo [attachment] attached" {
		t.Fatalf("mixed attachment text = %q", got)
	}
}

func TestChatConversationSuppressesMachineGroupTitle(t *testing.T) {
	chat := archive.ChatSummary{
		Title:              "chat297778184386366590",
		Kind:               "group",
		ParticipantCount:   2,
		ParticipantHandles: []string{"alice@example.com", "bob@example.com"},
	}
	if got := chatConversation(chat); got != "group with alice@example.com, bob@example.com" {
		t.Fatalf("machine group title was not suppressed: %q", got)
	}
}

func assertTextContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func assertNotSecretJSON(t *testing.T, got string) {
	t.Helper()
	if strings.Contains(got, `"items"`) || strings.Contains(got, `"schema_version"`) {
		t.Fatalf("text output looks like JSON:\n%s", got)
	}
}
