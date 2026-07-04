package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit/conformance"
	"github.com/openclaw/crawlkit/control"
	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func TestRunEndToEnd(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.db")
	createMessagesFixture(t, dbPath)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"help", nil, "imsgcrawl reads local iMessage"},
		{"version", []string{"--version"}, version},
		{"metadata global json", []string{"--json", "metadata"}, `"id": "imsgcrawl"`},
		{"metadata trailing json", []string{"metadata", "--json"}, `"contact-export"`},
		{"status", []string{"--db", dbPath, "--json", "status"}, `"messages": 5`},
		{"contacts export", []string{"--db", dbPath, "--json", "contacts", "export"}, `"display_name": "Fixture Person"`},
		{"contacts export trailing json", []string{"--db", dbPath, "contacts", "export", "--json"}, `"phone_numbers"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if err := Run(ctx, tc.args, &stdout, &stderr); err != nil {
				t.Fatalf("Run() error = %v stderr=%s", err, stderr.String())
			}
			if !strings.Contains(stdout.String(), tc.want) {
				t.Fatalf("stdout missing %q:\n%s", tc.want, stdout.String())
			}
		})
	}
}

func TestContactsExportShapeAndDedupe(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "chat.db")
	createMessagesFixture(t, dbPath)
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"--db", dbPath, "--json", "contacts", "export"}, &stdout, &stderr); err != nil {
		t.Fatalf("contacts export: %v stderr=%s", err, stderr.String())
	}
	assertContactExportKeys(t, stdout.Bytes())
	var payload struct {
		Contacts []struct {
			DisplayName  string   `json:"display_name"`
			PhoneNumbers []string `json:"phone_numbers"`
			Service      string   `json:"service"`
			Messages     int64    `json:"messages"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", stdout.String(), err)
	}
	got := map[string]string{}
	for _, contact := range payload.Contacts {
		if contact.Service != "" || contact.Messages != 0 {
			t.Fatalf("leaked source fields = %#v", contact)
		}
		if len(contact.PhoneNumbers) != 1 {
			t.Fatalf("phone_numbers = %#v", contact.PhoneNumbers)
		}
		got[contact.PhoneNumbers[0]] = contact.DisplayName
	}
	want := map[string]string{
		"0015550100": "Most Recent Name",
		"+15550103":  "Fixture Person",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contacts = %#v, want %#v", got, want)
	}
}

func TestArchiveCommandsSyncReadAndSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)

	syncOut := runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")
	syncLines := strings.Split(strings.TrimSpace(syncOut), "\n")
	if len(syncLines) != 3 {
		t.Fatalf("sync jsonl lines = %d, output:\n%s", len(syncLines), syncOut)
	}
	for i, line := range syncLines[:2] {
		var progress struct {
			Event string `json:"event"`
			Stage string `json:"stage"`
			Done  int    `json:"done"`
			Total int    `json:"total"`
		}
		if err := json.Unmarshal([]byte(line), &progress); err != nil {
			t.Fatalf("sync progress json = %s err=%v", line, err)
		}
		if progress.Event != "progress" || progress.Stage != "messages" {
			t.Fatalf("sync progress %d = %#v", i, progress)
		}
	}
	var syncResult struct {
		Event      string          `json:"event"`
		State      string          `json:"state"`
		Counts     []control.Count `json:"counts"`
		FinishedAt string          `json:"finished_at"`
	}
	if err := json.Unmarshal([]byte(syncLines[len(syncLines)-1]), &syncResult); err != nil {
		t.Fatalf("sync final json = %s err=%v", syncLines[len(syncLines)-1], err)
	}
	if syncResult.Event != "complete" || syncResult.State != "ok" {
		t.Fatalf("sync result = %#v", syncResult)
	}
	assertRFC3339(t, syncResult.FinishedAt)
	assertSyncCounts(t, syncResult.Counts, 5, 4, 6)
	if strings.Contains(syncLines[len(syncLines)-1], "archive_path") || strings.Contains(syncLines[len(syncLines)-1], "source_path") {
		t.Fatalf("sync final outcome leaked archive internals: %s", syncLines[len(syncLines)-1])
	}

	statusOut := runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "status")
	var status statusOutput
	if err := json.Unmarshal([]byte(statusOut), &status); err != nil {
		t.Fatalf("status json = %s err=%v", statusOut, err)
	}
	if status.Source == nil || status.Archive == nil {
		t.Fatalf("status missing source/archive = %#v", status)
	}
	if status.Source.Messages != status.Archive.Messages || status.Archive.ChatMessages != 6 {
		t.Fatalf("status counts = source %#v archive %#v", status.Source, status.Archive)
	}
	assertStatusCounts(t, status.Counts, status.Archive.Messages, status.Archive.Chats, status.Archive.NamedContacts, archive.AppleDateTime(100).Year())
	if status.Freshness == nil {
		t.Fatalf("status missing freshness = %#v", status)
	}
	assertRFC3339(t, status.Freshness.LastSync)
	if status.Log == nil || status.Log.LastRun == nil || status.Log.LastRun.Command != "sync" || status.Log.LastRun.Outcome != "success" {
		t.Fatalf("status log tail = %#v", status.Log)
	}
	firstRef := firstSearchRef(t, archivePath, "launch")
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")
	secondRef := firstSearchRef(t, archivePath, "launch")
	if firstRef != secondRef {
		t.Fatalf("search ref changed across sync: %q then %q", firstRef, secondRef)
	}
	shortRef := firstSearchShortRef(t, archivePath, "launch")
	if shortRef == "" {
		t.Fatal("search did not expose a short ref for text output")
	}
	shortOpenOut := runOK(t, "--archive", archivePath, "--json", "open", shortRef)
	var shortOpened openJSON
	if err := json.Unmarshal([]byte(shortOpenOut), &shortOpened); err != nil {
		t.Fatalf("short open json = %s err=%v", shortOpenOut, err)
	}
	if shortOpened.Ref != firstRef {
		t.Fatalf("short ref opened %q, want %q", shortOpened.Ref, firstRef)
	}
	assertShortRefError(t, archivePath, unusedShortAlias(t, archivePath), "unknown_short_ref")
	makeAmbiguousShortRef(t, archivePath, "22222")
	assertShortRefError(t, archivePath, "22222", "ambiguous_short_ref")

	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}

	allChatsOut := runOK(t, "--archive", archivePath, "--json", "chats")
	var allChats chatListJSON
	if err := json.Unmarshal([]byte(allChatsOut), &allChats); err != nil {
		t.Fatalf("all chats json = %s err=%v", allChatsOut, err)
	}
	if allChats.Returned != 4 || allChats.Total != 4 || allChats.Limit != defaultChatLimit || !allChats.Complete || len(allChats.Items) != 4 {
		t.Fatalf("bare chats should return all chats, got %#v", allChats)
	}

	limitedChatsOut := runOK(t, "--archive", archivePath, "--json", "chats", "--limit", "2")
	var limitedChats chatListJSON
	if err := json.Unmarshal([]byte(limitedChatsOut), &limitedChats); err != nil {
		t.Fatalf("limited chats json = %s err=%v", limitedChatsOut, err)
	}
	if limitedChats.Returned != 2 || limitedChats.Total != 4 || limitedChats.Limit != 2 || limitedChats.Complete || len(limitedChats.Items) != 2 {
		t.Fatalf("limited chats = %#v", limitedChats)
	}

	chatsOut := runOK(t, "--archive", archivePath, "--json", "chats", "--limit", "4")
	var chats chatListJSON
	if err := json.Unmarshal([]byte(chatsOut), &chats); err != nil {
		t.Fatalf("chats json = %s err=%v", chatsOut, err)
	}
	if len(chats.Items) != 4 {
		t.Fatalf("chats = %#v", chats)
	}
	if !chatHasMessage(t, chats.Items, "3", "Fixture Person", 1) || !chatHasMessage(t, chats.Items, "4", "Cabinet Group", 2) {
		t.Fatalf("chats did not preserve chat_message_join rows: %#v", chats)
	}
	for _, chat := range chats.Items {
		if chat.ChatID == "4" && (chat.Kind != "group" || chat.ParticipantCount != 3) {
			t.Fatalf("group chat context = %#v", chat)
		}
		if chat.ChatID == "4" && (!hasString(chat.ParticipantHandles, "Fixture Person") || !hasString(chat.ParticipantHandles, "opaque-handle")) {
			t.Fatalf("group participant handles = %#v", chat)
		}
		if chat.ChatID == "2" && (chat.Kind != "direct" || chat.ParticipantCount != 1) {
			t.Fatalf("direct chat context = %#v", chat)
		}
	}

	messagesOut := runOK(t, "--archive", archivePath, "--json", "messages", "--chat", "2", "--asc")
	var messageRows messageListJSON
	if err := json.Unmarshal([]byte(messagesOut), &messageRows); err != nil {
		t.Fatalf("messages json = %s err=%v", messagesOut, err)
	}
	if messageRows.ChatID != "2" || messageRows.Order != "oldest-first" || messageRows.Returned != 2 || messageRows.Total != 2 || !messageRows.Complete {
		t.Fatalf("message envelope = %#v", messageRows)
	}
	if messageRows.Chat == nil || messageRows.Chat.Title != "Most Recent Name" {
		t.Fatalf("message chat context = %#v", messageRows.Chat)
	}
	if len(messageRows.Items) != 2 || messageRows.Items[0].Text != "earlier launch note" || !strings.Contains(messageRows.Items[1].Text, "full tail marker") {
		t.Fatalf("messages = %#v", messageRows)
	}
	if messageRows.Items[1].GUID != "message-three" || !messageRows.Items[1].FromMe || messageRows.Items[1].Service != "SMS" {
		t.Fatalf("source message fields = %#v", messageRows.Items[1])
	}
	if messageRows.Items[0].Time == "" || strings.Contains(messagesOut, `"date"`) {
		t.Fatalf("message time fields = item %#v json %s", messageRows.Items[0], messagesOut)
	}
	assertRFC3339(t, messageRows.Items[0].Time)
	if messageRows.Items[0].SenderLabel != "Most Recent Name" || messageRows.Items[0].SenderHandle != "0015550100" || messageRows.Items[1].SenderLabel != "me" {
		t.Fatalf("sender labels = %#v", messageRows.Items)
	}

	attachedOut := runOK(t, "--archive", archivePath, "--json", "messages", "--chat", "3", "--asc")
	var attachedRows messageListJSON
	if err := json.Unmarshal([]byte(attachedOut), &attachedRows); err != nil {
		t.Fatalf("attached json = %s err=%v", attachedOut, err)
	}
	if len(attachedRows.Items) != 1 || !attachedRows.Items[0].HasAttachments {
		t.Fatalf("attached rows = %#v", attachedRows)
	}
	groupOut := runOK(t, "--archive", archivePath, "--json", "messages", "--chat", "4", "--asc")
	var groupRows messageListJSON
	if err := json.Unmarshal([]byte(groupOut), &groupRows); err != nil {
		t.Fatalf("group json = %s err=%v", groupOut, err)
	}
	if len(groupRows.Items) != 2 || groupRows.Items[0].SenderLabel != "Fixture Person" || groupRows.Items[1].SenderLabel != "opaque-handle" {
		t.Fatalf("group sender labels = %#v", groupRows.Items)
	}

	emptyMessagesOut := runOK(t, "--archive", archivePath, "--json", "messages", "--chat", "999")
	var emptyMessages messageListJSON
	if err := json.Unmarshal([]byte(emptyMessagesOut), &emptyMessages); err != nil {
		t.Fatalf("empty messages json = %s err=%v", emptyMessagesOut, err)
	}
	if emptyMessages.Returned != 0 || emptyMessages.Total != 0 || !emptyMessages.Complete || len(emptyMessages.Items) != 0 {
		t.Fatalf("empty messages output = %#v", emptyMessages)
	}

	searchOut := runOK(t, "--archive", archivePath, "--json", "search", "launch")
	assertSearchEnvelopeKeys(t, []byte(searchOut))
	conformance.AssertSearchEnvelope(t, []byte(searchOut))
	var results searchListJSON
	if err := json.Unmarshal([]byte(searchOut), &results); err != nil {
		t.Fatalf("search json = %s err=%v", searchOut, err)
	}
	if results.Query != "launch" || results.TotalMatches != 2 || results.Truncated || len(results.Results) != 2 {
		t.Fatalf("search results = %#v", results)
	}
	for _, result := range results.Results {
		if !strings.HasPrefix(result.Ref, messageRefPrefix) {
			t.Fatalf("search result ref = %#v", result)
		}
		assertRFC3339(t, result.Time)
		if result.Who == "" || result.Where != "Most Recent Name" || !strings.Contains(result.Snippet, "launch") {
			t.Fatalf("search result fields = %#v", result)
		}
		if strings.ContainsAny(result.Who+result.Where+result.Snippet, "\n\t") || strings.ContainsAny(result.Snippet, "[]") || strings.Contains(result.Snippet, "...") {
			t.Fatalf("search result kept marker or multiline fields = %#v", result)
		}
	}

	trailingFlagOut := runOK(t, "--archive", archivePath, "--json", "search", "launch", "--limit", "1")
	var trailingFlagSearch searchListJSON
	if err := json.Unmarshal([]byte(trailingFlagOut), &trailingFlagSearch); err != nil {
		t.Fatalf("trailing flag search json = %s err=%v", trailingFlagOut, err)
	}
	if trailingFlagSearch.Query != "launch" || len(trailingFlagSearch.Results) != 1 || trailingFlagSearch.TotalMatches != 2 || !trailingFlagSearch.Truncated {
		t.Fatalf("trailing flag search = %#v", trailingFlagSearch)
	}

	openOut := runOK(t, "--archive", archivePath, "--json", "open", results.Results[0].Ref)
	var opened openJSON
	if err := json.Unmarshal([]byte(openOut), &opened); err != nil {
		t.Fatalf("open json = %s err=%v", openOut, err)
	}
	if opened.Ref != results.Results[0].Ref || opened.Message.Ref != results.Results[0].Ref || opened.Chat.Name != "Most Recent Name" {
		t.Fatalf("open round trip = %#v", opened)
	}
	if len(opened.Context) == 0 || len(opened.Context) > 21 {
		t.Fatalf("open context size = %#v", opened.Context)
	}
	targets := 0
	for _, item := range opened.Context {
		assertRFC3339(t, item.Time)
		if item.Target {
			targets++
		}
	}
	if targets != 1 || !strings.Contains(opened.Message.Text, "launch note") {
		t.Fatalf("open target/context = %#v", opened)
	}
	assertForeignOpenRefFailsCleanly(t, archivePath)

	fallbackSearchOut := runOK(t, "--archive", archivePath, "--json", "search", "opaque")
	var fallbackSearch searchListJSON
	if err := json.Unmarshal([]byte(fallbackSearchOut), &fallbackSearch); err != nil {
		t.Fatalf("fallback search json = %s err=%v", fallbackSearchOut, err)
	}
	if len(fallbackSearch.Results) != 1 {
		t.Fatalf("fallback search results = %#v", fallbackSearch)
	}
	if fallbackSearch.Results[0].Who != "opaque-handle" {
		t.Fatalf("fallback sender label = %#v", fallbackSearch.Results[0])
	}

	emptySearchOut := runOK(t, "--archive", archivePath, "--json", "search", "zzznomatchimsgcrawl")
	var emptySearch searchListJSON
	if err := json.Unmarshal([]byte(emptySearchOut), &emptySearch); err != nil {
		t.Fatalf("empty search json = %s err=%v", emptySearchOut, err)
	}
	if emptySearch.TotalMatches != 0 || emptySearch.Truncated || len(emptySearch.Results) != 0 {
		t.Fatalf("empty search output = %#v", emptySearch)
	}
}

func TestLimitFlagsAreExplicit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")

	for _, args := range [][]string{
		{"--archive", archivePath, "chats", "--all", "--limit", "2"},
		{"--archive", archivePath, "chats", "--limit", "0"},
		{"--archive", archivePath, "messages", "--chat", "1", "--all", "--limit", "2"},
		{"--archive", archivePath, "search", "--all", "--limit", "2", "launch"},
		{"--archive", archivePath, "search", "--all", "launch"},
		{"--archive", archivePath, "messages", "--chat", "1", "--limit", "0"},
		{"--archive", archivePath, "search", "--limit", "0", "launch"},
		{"--archive", archivePath, "messages", "--chat", "1", "--limit", "201"},
		{"--archive", archivePath, "search", "--limit", "201", "launch"},
	} {
		var stdout, stderr bytes.Buffer
		err := Run(context.Background(), args, &stdout, &stderr)
		if err == nil || ExitCode(err) != 2 {
			t.Fatalf("Run(%v) expected usage error, got err=%v stdout=%s stderr=%s", args, err, stdout.String(), stderr.String())
		}
	}

	allMessagesOut := runOK(t, "--archive", archivePath, "--json", "messages", "--chat", "2", "--all")
	var allMessages messageListJSON
	if err := json.Unmarshal([]byte(allMessagesOut), &allMessages); err != nil {
		t.Fatalf("all messages json = %s err=%v", allMessagesOut, err)
	}
	if allMessages.Returned != 2 || allMessages.Total != 2 || !allMessages.Complete || len(allMessages.Items) != 2 {
		t.Fatalf("all messages = %#v", allMessages)
	}
}

func TestArchiveCommandsRequireSync(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "chats"},
		{"--json", "messages", "--chat", "1"},
		{"--json", "search", "hello"},
		{"--json", "open", "imsgcrawl:msg/1"},
	} {
		var stdout, stderr bytes.Buffer
		missingPath := filepath.Join(t.TempDir(), "missing.db")
		withArchive := append([]string{"--archive", missingPath}, args...)
		err := Run(context.Background(), withArchive, &stdout, &stderr)
		if err == nil {
			t.Fatalf("Run(%v) expected missing archive error", withArchive)
		}
		if !strings.Contains(err.Error(), "run imsgcrawl sync first") {
			t.Fatalf("err = %v", err)
		}
	}
}

func TestStatusArchiveStates(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	createMessagesFixture(t, dbPath)

	missingOut := runOK(t, "--db", dbPath, "--archive", filepath.Join(dir, "missing.db"), "--json", "status")
	var missing statusOutput
	if err := json.Unmarshal([]byte(missingOut), &missing); err != nil {
		t.Fatalf("missing status json = %s err=%v", missingOut, err)
	}
	if missing.State != "missing" || !hasWarning(missing.Warnings, "archive has not been synced") {
		t.Fatalf("missing archive status = %#v", missing)
	}
	if missing.Freshness != nil {
		t.Fatalf("missing archive should omit freshness = %#v", missing.Freshness)
	}

	emptyArchivePath := filepath.Join(dir, "empty.db")
	emptyStore, err := archive.Open(context.Background(), emptyArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := emptyStore.Close(); err != nil {
		t.Fatal(err)
	}
	emptyOut := runOK(t, "--db", dbPath, "--archive", emptyArchivePath, "--json", "status")
	var empty statusOutput
	if err := json.Unmarshal([]byte(emptyOut), &empty); err != nil {
		t.Fatalf("empty status json = %s err=%v", emptyOut, err)
	}
	if empty.State != "empty" || empty.Freshness != nil {
		t.Fatalf("empty archive status = %#v", empty)
	}

	corruptPath := filepath.Join(dir, "corrupt.db")
	if err := os.WriteFile(corruptPath, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	corruptOut := runOK(t, "--db", dbPath, "--archive", corruptPath, "--json", "status")
	var corrupt statusOutput
	if err := json.Unmarshal([]byte(corruptOut), &corrupt); err != nil {
		t.Fatalf("corrupt status json = %s err=%v", corruptOut, err)
	}
	if corrupt.State != "error" || len(corrupt.Warnings) == 0 {
		t.Fatalf("corrupt archive status = %#v", corrupt)
	}

	archivePath := filepath.Join(dir, "archive.db")
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")
	db, err := sql.Open("sqlite", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	staleSync := time.Now().Add(-statusStaleAfter - time.Hour).UTC().Format(time.RFC3339)
	if _, err := db.Exec(`insert into sync_state(key, value) values('last_sync_at', ?) on conflict(key) do update set value = excluded.value`, staleSync); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	staleOut := runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "status")
	var stale statusOutput
	if err := json.Unmarshal([]byte(staleOut), &stale); err != nil {
		t.Fatalf("stale status json = %s err=%v", staleOut, err)
	}
	if stale.State != "stale" || stale.Freshness == nil {
		t.Fatalf("stale status = %#v", stale)
	}
}

func TestMetadataAdvertisesCrawlerCommands(t *testing.T) {
	manifest := controlManifest()
	command, ok := manifest.Commands["contact-export"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if command.Mutates || !command.JSON {
		t.Fatalf("contact-export command = %#v", command)
	}
	want := []string{"imsgcrawl", "contacts", "export", "--json"}
	if !reflect.DeepEqual(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
	for _, name := range []string{"sync", "doctor", "chats", "messages", "who", "search", "open"} {
		command, ok := manifest.Commands[name]
		if !ok {
			t.Fatalf("missing command %q in %#v", name, manifest.Commands)
		}
		if !command.JSON {
			t.Fatalf("%s command is not JSON = %#v", name, command)
		}
	}
	if !manifest.Commands["sync"].Mutates {
		t.Fatalf("sync should be marked mutating = %#v", manifest.Commands["sync"])
	}
	if manifest.Commands["doctor"].Mutates {
		t.Fatalf("doctor should not be marked mutating = %#v", manifest.Commands["doctor"])
	}
	if !reflect.DeepEqual(manifest.Commands["open"].Argv, []string{"imsgcrawl", "open", "REF", "--json"}) {
		t.Fatalf("open argv = %#v", manifest.Commands["open"].Argv)
	}
	if !reflect.DeepEqual(manifest.Commands["who"].Argv, []string{"imsgcrawl", "who", "NAME", "--json"}) {
		t.Fatalf("who argv = %#v", manifest.Commands["who"].Argv)
	}
	for _, want := range []string{"message-archive", "message-text-search"} {
		if !hasString(manifest.Privacy.LocalOnlyScopes, want) {
			t.Fatalf("local_only_scopes = %#v, missing %q", manifest.Privacy.LocalOnlyScopes, want)
		}
	}
	if !hasString(manifest.Capabilities, "who") {
		t.Fatalf("capabilities = %#v, missing who", manifest.Capabilities)
	}
	if !hasString(manifest.Capabilities, "short_refs") {
		t.Fatalf("capabilities = %#v, missing short_refs", manifest.Capabilities)
	}
	if !hasString(manifest.Capabilities, "verbose_logs") {
		t.Fatalf("capabilities = %#v, missing verbose_logs", manifest.Capabilities)
	}
	if manifest.Paths.DefaultLogs != filepath.Join(defaultBaseDir(), "logs") {
		t.Fatalf("default logs = %q, want %q", manifest.Paths.DefaultLogs, filepath.Join(defaultBaseDir(), "logs"))
	}
}

func TestVerboseLogsWriteFileAndStreamToStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	logPath := filepath.Join(home, ".imsgcrawl", "logs", "imsgcrawl.log")

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"metadata"}, &stdout, &stderr); err != nil {
		t.Fatalf("metadata error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("metadata without -v wrote stderr:\n%s", stderr.String())
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file missing at %s: %v", logPath, err)
	}
	logText := readTestLog(t)
	for _, want := range []string{"metadata start:", "metadata finish: outcome=success"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(context.Background(), []string{"-v", "metadata"}, &stdout, &stderr); err != nil {
		t.Fatalf("metadata -v error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "metadata start:") || !strings.Contains(stderr.String(), "metadata finish: outcome=success") {
		t.Fatalf("-v stderr missing log lines:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "DEBUG") {
		t.Fatalf("-v streamed debug line:\n%s", stderr.String())
	}
}

func TestSyncVerboseLogsPhaseTimings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"-vv", "--db", dbPath, "--archive", archivePath, "--json", "sync"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("sync -vv error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	logText := readTestLog(t)
	for _, want := range []string{
		"sync_done: messages=5",
		"chats=4",
		"participants=6",
		"sync_phase: source=messages",
		"extract_ms=",
		"contacts_ms=",
		"map_ms=",
		"write_ms=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("sync log missing %q:\n%s", want, logText)
		}
	}
	if !strings.Contains(stderr.String(), "sync_done: messages=5") || !strings.Contains(stderr.String(), "sync_phase: source=messages") {
		t.Fatalf("-vv stderr missing sync log lines:\n%s", stderr.String())
	}
}

func TestDoctorChecks(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "chat.db")
	archivePath := filepath.Join(dir, "archive.db")
	createMessagesFixture(t, dbPath)
	_ = runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "sync")

	successOut := runOK(t, "--db", dbPath, "--archive", archivePath, "--json", "doctor")
	var success doctorOutput
	if err := json.Unmarshal([]byte(successOut), &success); err != nil {
		t.Fatalf("doctor json = %s err=%v", successOut, err)
	}
	assertDoctorCheck(t, success, "source_store", "ok", "")
	assertDoctorCheck(t, success, "archive", "ok", "")
	assertDoctorCheck(t, success, "full_disk_access", "ok", "")

	missingDBPath := filepath.Join(dir, "missing", "chat.db")
	missingArchivePath := filepath.Join(dir, "missing-archive.db")
	failureOut := runOK(t, "--db", missingDBPath, "--archive", missingArchivePath, "--json", "doctor")
	assertDoctorJSONNoLogInternals(t, failureOut)
	var failure doctorOutput
	if err := json.Unmarshal([]byte(failureOut), &failure); err != nil {
		t.Fatalf("doctor failure json = %s err=%v", failureOut, err)
	}
	assertDoctorCheck(t, failure, "source_store", "fail", "")
	assertDoctorCheck(t, failure, "archive", "fail", "")
	assertDoctorCheck(t, failure, "full_disk_access", "fail", fullDiskAccessRemedy)

	loggedFailureOut := runOK(t, "--db", missingDBPath, "--archive", missingArchivePath, "--json", "doctor")
	assertDoctorJSONNoLogInternals(t, loggedFailureOut)
	var loggedFailure doctorOutput
	if err := json.Unmarshal([]byte(loggedFailureOut), &loggedFailure); err != nil {
		t.Fatalf("doctor logged failure json = %s err=%v", loggedFailureOut, err)
	}
	if loggedFailure.Log == nil || loggedFailure.Log.MostRecentError == nil {
		t.Fatalf("doctor log tail missing previous user-facing error: %#v", loggedFailure.Log)
	}
	if loggedFailure.Log.MostRecentError.Remedy == "" {
		t.Fatalf("doctor log tail missing remedy: %#v", loggedFailure.Log.MostRecentError)
	}
}

func TestRunUsageErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"bogus"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected usage exit, got err=%v code=%d", err, ExitCode(err))
	}
	if ExitCode(nil) != 0 {
		t.Fatal("nil exit code should be zero")
	}
	if ExitCode(errors.New("plain")) != 1 {
		t.Fatal("plain error exit code should be one")
	}
}

func TestJSONUsageErrorIsSingleRenderedDocument(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"search", "--limit", "1000", "a", "--json"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 2 {
		t.Fatalf("expected usage exit, got err=%v code=%d", err, ExitCode(err))
	}
	if !ckoutput.IsRendered(err) {
		t.Fatalf("error was not marked rendered: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("JSON error wrote stderr: %s", stderr.String())
	}
	var payload errorJSON
	assertSingleJSONDocument(t, stdout.String(), &payload)
	if payload.Error.Code != "usage" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("error payload = %#v", payload)
	}
}

func TestHumanUsageErrorStrings(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "search limit",
			args: []string{"search", "--limit", "1000", "a"},
			want: "search --limit must be 200 or less",
		},
		{
			name: "who blank",
			args: []string{"who", " "},
			want: "who requires a name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Run(context.Background(), tc.args, &stdout, &stderr)
			if err == nil || ExitCode(err) != 2 {
				t.Fatalf("expected usage exit, got err=%v code=%d", err, ExitCode(err))
			}
			if err.Error() != tc.want {
				t.Fatalf("error = %q, want %q", err.Error(), tc.want)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("usage wrote stdout=%q stderr=%q", stdout.String(), stderr.String())
			}
		})
	}
}

func assertContactExportKeys(t *testing.T, data []byte) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	contactsJSON, ok := root["contacts"]
	if !ok || len(root) != 1 {
		t.Fatalf("root keys = %#v, want only contacts", root)
	}
	var contacts []map[string]json.RawMessage
	if err := json.Unmarshal(contactsJSON, &contacts); err != nil {
		t.Fatal(err)
	}
	for _, contact := range contacts {
		if _, ok := contact["display_name"]; !ok {
			t.Fatalf("contact keys = %#v, missing display_name", contact)
		}
		if _, ok := contact["phone_numbers"]; !ok {
			t.Fatalf("contact keys = %#v, missing phone_numbers", contact)
		}
		if len(contact) != 2 {
			t.Fatalf("contact keys = %#v, want only display_name and phone_numbers", contact)
		}
	}
}

func assertSingleJSONDocument(t *testing.T, data string, out any) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(data))
	if err := dec.Decode(out); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, data)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON output had trailing data: %v\n%s", err, data)
	}
}

func firstSearchRef(t *testing.T, archivePath, query string) string {
	t.Helper()
	out := runOK(t, "--archive", archivePath, "--json", "search", "--limit", "1", query)
	var payload searchListJSON
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", out, err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("search results = %#v, want one result", payload)
	}
	return payload.Results[0].Ref
}

func firstSearchShortRef(t *testing.T, archivePath, query string) string {
	t.Helper()
	st, err := archive.OpenExisting(context.Background(), archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	page, err := st.SearchPage(context.Background(), query, archive.SearchOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("search results = %#v, want one result", page.Items)
	}
	return page.Items[0].ShortRef
}

func unusedShortAlias(t *testing.T, archivePath string) string {
	t.Helper()
	db, err := sql.Open("sqlite", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, alias := range []string{"22222", "33333", "44444", "55555", "66666"} {
		var count int
		if err := db.QueryRow(`select count(*) from short_refs where alias = ?`, alias).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			return alias
		}
	}
	t.Fatal("no unused short alias candidate")
	return ""
}

func makeAmbiguousShortRef(t *testing.T, archivePath, alias string) {
	t.Helper()
	db, err := sql.Open("sqlite", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`delete from short_refs`); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 5; id++ {
		if _, err := db.Exec(`insert into short_refs(alias, full_ref) values(?, ?)`, alias, archive.MessageRef(strconv.Itoa(id))); err != nil {
			t.Fatal(err)
		}
	}
}

func assertShortRefError(t *testing.T, archivePath, alias, code string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--archive", archivePath, "--json", "open", alias}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("open %q error = %v stdout=%s stderr=%s", alias, err, stdout.String(), stderr.String())
	}
	var payload errorJSON
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("short ref error json = %s err=%v", stdout.String(), err)
	}
	if payload.Error.Code != code || payload.Error.Remedy == "" {
		t.Fatalf("short ref error = %#v, want code %q", payload, code)
	}
}

func assertSearchEnvelopeKeys(t *testing.T, data []byte) {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"query":         true,
		"results":       true,
		"total_matches": true,
		"truncated":     true,
	}
	if len(root) != len(want) {
		t.Fatalf("search root keys = %#v", root)
	}
	for key := range root {
		if !want[key] {
			t.Fatalf("search root key %q not in contract keys %#v", key, want)
		}
	}
}

func assertForeignOpenRefFailsCleanly(t *testing.T, archivePath string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--archive", archivePath, "--json", "open", "telecrawl:msg/1"}, &stdout, &stderr)
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("foreign open ref error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("json error wrote stderr: %s", stderr.String())
	}
	var payload errorJSON
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("foreign ref json = %s err=%v", stdout.String(), err)
	}
	if payload.Error.Code != "foreign_ref" || payload.Error.Message == "" || payload.Error.Remedy == "" {
		t.Fatalf("foreign ref envelope = %#v", payload)
	}
}

func assertStatusCounts(t *testing.T, counts []control.Count, messages, chats, namedContacts int64, sinceYear int) {
	t.Helper()
	want := map[string]int64{
		"messages":       messages,
		"chats":          chats,
		"named_contacts": namedContacts,
		"since":          int64(sinceYear),
	}
	if len(counts) != len(want) {
		t.Fatalf("counts = %#v, want 4 headline counts", counts)
	}
	for _, count := range counts {
		if got, ok := want[count.ID]; !ok || count.Value != got {
			t.Fatalf("count %#v not in %#v", count, want)
		}
	}
}

func assertSyncCounts(t *testing.T, counts []control.Count, messages, chats, participants int64) {
	t.Helper()
	want := map[string]int64{
		"messages":     messages,
		"chats":        chats,
		"participants": participants,
	}
	if len(counts) != len(want) {
		t.Fatalf("sync counts = %#v, want 3 headline counts", counts)
	}
	for _, count := range counts {
		if got, ok := want[count.ID]; !ok || count.Value != got {
			t.Fatalf("sync count %#v not in %#v", count, want)
		}
	}
}

func assertRFC3339(t *testing.T, value string) {
	t.Helper()
	if value == "" {
		t.Fatal("time value is empty")
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		t.Fatalf("time %q is not RFC 3339: %v", value, err)
	}
}

func readTestLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(os.Getenv("HOME"), ".imsgcrawl", "logs", "imsgcrawl.log"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertDoctorCheck(t *testing.T, out doctorOutput, id, state, remedy string) {
	t.Helper()
	if len(out.Checks) != 3 {
		t.Fatalf("doctor checks = %#v, want 3", out.Checks)
	}
	for _, check := range out.Checks {
		if check.ID != id {
			continue
		}
		if check.State != state {
			t.Fatalf("check %s state = %s, want %s", id, check.State, state)
		}
		if state == "fail" && check.Remedy == "" {
			t.Fatalf("check %s has no remedy: %#v", id, check)
		}
		if remedy != "" && check.Remedy != remedy {
			t.Fatalf("check %s remedy = %q, want %q", id, check.Remedy, remedy)
		}
		if state == "ok" && check.Remedy != "" {
			t.Fatalf("passing check %s should not have remedy: %#v", id, check)
		}
		return
	}
	t.Fatalf("missing doctor check %q in %#v", id, out.Checks)
}

func assertDoctorJSONNoLogInternals(t *testing.T, out string) {
	t.Helper()
	for _, forbidden := range []string{"run_id", "last_event", "run_failed", "event=", "visibility"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("doctor json leaked %q:\n%s", forbidden, out)
		}
	}
}
