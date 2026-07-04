package cli

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "telecrawl-test-home-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestStoreImportResultUpsertsReturnedAccountScopedChats(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	full := accountScopedImportResult("old")
	if err := storeImportResult(ctx, st, &full, ""); err != nil {
		t.Fatal(err)
	}
	partial := accountScopedImportResult("new")
	if err := storeImportResult(ctx, st, &partial, "100"); err != nil {
		t.Fatal(err)
	}

	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Chats != 2 || status.Messages != 2 {
		t.Fatalf("status = chats %d messages %d, want 2/2", status.Chats, status.Messages)
	}
	messages, err := st.Messages(ctx, store.MessageFilter{Limit: 10, Asc: true})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{messages[0].Text, messages[1].Text}
	want := []string{"new a", "new b"}
	if !slices.Equal(got, want) {
		t.Fatalf("messages = %v, want %v", got, want)
	}
}

func TestStoreImportResultPersistsGroupParticipants(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Unix(1_800_000_000, 0).UTC()
	result := telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		Contacts: []store.Contact{
			{JID: "600", FullName: "Group Member", FirstName: "Group"},
			{JID: "700", FullName: "Other Sender", FirstName: "Other"},
		},
		Chats: []store.Chat{{JID: "500", Kind: "group", Name: "team room", LastMessageAt: now.Add(time.Minute), MessageCount: 2}},
		Participants: []store.GroupParticipant{
			{GroupJID: "500", UserJID: "600", ContactName: "Group Member", FirstName: "Group", IsActive: true},
		},
		Messages: []store.Message{
			{SourcePK: 1, ChatJID: "500", ChatName: "team room", MessageID: "1", SenderJID: "700", SenderName: "Other Sender", Timestamp: now, Text: "member needle from other"},
			{SourcePK: 2, ChatJID: "500", ChatName: "team room", MessageID: "2", SenderJID: "600", SenderName: "Group Member", Timestamp: now.Add(time.Minute), Text: "member needle from group member"},
		},
	}
	if err := storeImportResult(ctx, st, &result, ""); err != nil {
		t.Fatal(err)
	}

	exported, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Participants) != 1 || exported.Participants[0].GroupJID != "500" || exported.Participants[0].UserJID != "600" {
		t.Fatalf("participants = %#v, want persisted group member", exported.Participants)
	}
	messages, err := st.Search(ctx, store.MessageFilter{Query: "needle", Who: "Group Member", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 {
		t.Fatalf("search --who group member = %d messages, want 2", len(messages))
	}
}

func TestStoreImportResultPersistsTelegramUserContactsForExport(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Unix(1_800_000_000, 0).UTC()
	result := telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "tdata", StartedAt: now, FinishedAt: now},
		Contacts: []store.Contact{{
			JID:       "100000235",
			PeerType:  "user",
			FullName:  "Alex Example",
			FirstName: "Mif",
			LastName:  "Example",
			Username:  "AlexExample",
		}},
		Chats: []store.Chat{{
			JID:           "100000235",
			Kind:          "chat",
			Name:          "Alex Example",
			Username:      "AlexExample",
			LastMessageAt: now,
			MessageCount:  1,
		}},
		Messages: []store.Message{{
			SourcePK:   1,
			ChatJID:    "100000235",
			ChatName:   "Alex Example",
			MessageID:  "1",
			SenderJID:  "100000235",
			SenderName: "Alex Example",
			Timestamp:  now,
			Text:       "telegram contact evidence",
		}},
	}
	if err := storeImportResult(ctx, st, &result, ""); err != nil {
		t.Fatal(err)
	}

	exported, err := st.ExportAll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(exported.Contacts) != 1 || exported.Contacts[0].JID != "100000235" || exported.Contacts[0].Username != "AlexExample" {
		t.Fatalf("stored contacts = %#v, want Telegram user contact", exported.Contacts)
	}

	var out, errOut bytes.Buffer
	err = Run(ctx, []string{"--json", "--db", db, "contacts", "export"}, &out, &errOut)
	if err != nil {
		t.Fatalf("contacts export: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Contacts []struct {
			DisplayName  string              `json:"display_name"`
			PhoneNumbers []string            `json:"phone_numbers"`
			Accounts     map[string][]string `json:"accounts"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", out.String(), err)
	}
	if len(payload.Contacts) != 1 {
		t.Fatalf("contacts = %#v, want one exported Telegram contact", payload.Contacts)
	}
	contact := payload.Contacts[0]
	if contact.DisplayName != "Alex Example" || len(contact.PhoneNumbers) != 0 || len(contact.Accounts["telegram"]) != 1 || contact.Accounts["telegram"][0] != "AlexExample" {
		t.Fatalf("exported contact = %#v, want Telegram account without invented phone", contact)
	}

	out.Reset()
	errOut.Reset()
	err = Run(ctx, []string{"--db", db, "contacts", "export"}, &out, &errOut)
	if err != nil {
		t.Fatalf("contacts export text: %v stderr=%s", err, errOut.String())
	}
	text := out.String()
	if strings.Contains(text, "{") || strings.Contains(text, `"contacts"`) {
		t.Fatalf("contacts export text looked like JSON:\n%s", text)
	}
	for _, want := range []string{"Alex Example\t@AlexExample", "1 contact"} {
		if !strings.Contains(text, want) {
			t.Fatalf("contacts export text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "identifier") {
		t.Fatalf("contacts export text must show identifiers, not counts:\n%s", text)
	}
}

func TestImportResultForChatFiltersContacts(t *testing.T) {
	result := accountScopedImportResult("filtered")
	partial := importResultForChat(result, "111")

	got := make([]string, 0, len(partial.Contacts))
	for _, contact := range partial.Contacts {
		got = append(got, contact.JID)
	}
	want := []string{"111", "10"}
	if !slices.Equal(got, want) {
		t.Fatalf("contacts = %v, want %v", got, want)
	}
}

func TestContactsExportUsesContractShapeAndSkipsUnsafeNames(t *testing.T) {
	ctx := context.Background()
	db := filepath.Join(t.TempDir(), "telecrawl.db")
	st, err := store.Open(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	contacts := make([]store.Contact, 0, 104)
	messages := make([]store.Message, 0, 104)
	addContact := func(contact store.Contact, withEvidence bool) {
		contacts = append(contacts, contact)
		if !withEvidence {
			return
		}
		messages = append(messages, store.Message{
			SourcePK:  int64(len(messages) + 1),
			ChatJID:   contact.JID,
			MessageID: fmt.Sprintf("msg-%d", len(messages)+1),
			Timestamp: time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC),
			Text:      "contact evidence",
		})
	}
	for i := 0; i < 101; i++ {
		addContact(store.Contact{
			JID:      "safe-" + string(rune('a'+(i%26))) + "-" + string(rune('a'+((i/26)%26))),
			Phone:    fmt.Sprintf("+155501%05d", i),
			FullName: "Safe Person",
		}, true)
	}
	addContact(store.Contact{JID: "first-last", Phone: "+15559990001", FirstName: "First", LastName: "Last"}, true)
	addContact(store.Contact{JID: "first-last-duplicate", Phone: "+15559990001", FirstName: "First", LastName: "Last"}, true)
	addContact(store.Contact{JID: "recent-short", Phone: "+15559990008", FullName: "Recent", UpdatedAt: time.Unix(200, 0).UTC()}, true)
	addContact(store.Contact{JID: "older-richer", Phone: "+15559990008", FullName: "Older Richer Name", UpdatedAt: time.Unix(100, 0).UTC()}, true)
	addContact(store.Contact{JID: "equal-short", Phone: "+15559990009", FullName: "Sam"}, true)
	addContact(store.Contact{JID: "equal-richer", Phone: "+15559990009", FullName: "Sam Example"}, true)
	addContact(store.Contact{JID: "username-only", Phone: "+15559990002", Username: "handle", FullName: "@handle"}, true)
	addContact(store.Contact{JID: "bare-username-only", Phone: "+15559990006", Username: "handle", FullName: "Handle"}, true)
	addContact(store.Contact{JID: "phone-only", Phone: "+15559990003", FullName: "+15559990003"}, true)
	addContact(store.Contact{JID: "jid-only", Phone: "+15559990004", FullName: "jid-only"}, true)
	addContact(store.Contact{JID: "blank-name", Phone: "+15559990005"}, true)
	addContact(store.Contact{JID: "no-phone", FullName: "No Phone"}, true)
	addContact(store.Contact{JID: "short-phone-person", Phone: "12345", FullName: "Short Phone Person"}, true)
	addContact(store.Contact{JID: "telegram-service", Phone: "42777", FullName: "Telegram", FirstName: "Telegram"}, true)
	addContact(store.Contact{JID: "stale-peer", Phone: "+15559990007", FullName: "Stale Peer"}, false)
	if err := st.ReplaceAll(ctx, store.ImportStats{}, contacts, nil, nil, nil, nil, nil, messages); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	err = Run(ctx, []string{"--json", "--db", db, "contacts", "export"}, &out, &errOut)
	if err != nil {
		t.Fatalf("contacts export: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Contacts []struct {
			DisplayName  string              `json:"display_name"`
			PhoneNumbers []string            `json:"phone_numbers"`
			Accounts     map[string][]string `json:"accounts"`
			JID          string              `json:"jid"`
			Username     string              `json:"username"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json = %s err=%v", out.String(), err)
	}
	assertContactExportKeys(t, out.Bytes())
	if len(payload.Contacts) != 107 {
		t.Fatalf("contacts = %d, want 107", len(payload.Contacts))
	}
	var sawFirstLast, sawShortPhonePerson, sawRecent, sawRicherEqual, sawUsernameOnly, sawBareUsernameOnly bool
	firstLastCount := 0
	for _, contact := range payload.Contacts {
		if contact.DisplayName == "First Last" {
			sawFirstLast = true
			if contact.PhoneNumbers[0] == "+15559990001" {
				firstLastCount++
			}
		}
		if contact.DisplayName == "Recent" && contact.PhoneNumbers[0] == "+15559990008" {
			sawRecent = true
		}
		if contact.DisplayName == "Sam Example" && contact.PhoneNumbers[0] == "+15559990009" {
			sawRicherEqual = true
		}
		if contact.DisplayName == "Short Phone Person" && contact.PhoneNumbers[0] == "12345" {
			sawShortPhonePerson = true
		}
		if contact.DisplayName == "handle" && len(contact.Accounts["telegram"]) == 1 && contact.Accounts["telegram"][0] == "handle" {
			switch contact.PhoneNumbers[0] {
			case "+15559990002":
				sawUsernameOnly = true
			case "+15559990006":
				sawBareUsernameOnly = true
			}
		}
		if contact.DisplayName == "" || len(contact.PhoneNumbers) != 1 {
			t.Fatalf("bad contact = %#v", contact)
		}
		if contact.JID != "" || contact.Username != "" {
			t.Fatalf("leaked source fields = %#v", contact)
		}
		if strings.HasPrefix(contact.DisplayName, "@") || strings.HasPrefix(contact.DisplayName, "+") || contact.DisplayName == "jid-only" {
			t.Fatalf("unsafe display name exported: %#v", contact)
		}
		if contact.DisplayName == "Handle" || contact.PhoneNumbers[0] == "42777" {
			t.Fatalf("unsafe contact exported: %#v", contact)
		}
		if contact.DisplayName == "Stale Peer" {
			t.Fatalf("stale contact without conversation evidence exported: %#v", contact)
		}
		if contact.DisplayName == "Older Richer Name" || contact.DisplayName == "Sam" {
			t.Fatalf("wrong duplicate contact name exported: %#v", contact)
		}
	}
	if !sawFirstLast {
		t.Fatalf("missing composed first/last name: %#v", payload.Contacts)
	}
	if firstLastCount != 1 {
		t.Fatalf("first/last duplicate count = %d, want 1", firstLastCount)
	}
	if !sawShortPhonePerson {
		t.Fatalf("missing short phone person: %#v", payload.Contacts)
	}
	if !sawRecent {
		t.Fatalf("missing newer duplicate contact name: %#v", payload.Contacts)
	}
	if !sawRicherEqual {
		t.Fatalf("missing richer equal-time contact name: %#v", payload.Contacts)
	}
	if !sawUsernameOnly || !sawBareUsernameOnly {
		t.Fatalf("missing username-backed contacts: %#v", payload.Contacts)
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
		identifiers := 0
		for key := range contact {
			switch key {
			case "display_name":
			case "phone_numbers", "accounts":
				identifiers++
			default:
				t.Fatalf("contact keys = %#v, unexpected %q", contact, key)
			}
		}
		if identifiers == 0 {
			t.Fatalf("contact keys = %#v, missing identifiers", contact)
		}
	}
}

func TestMetadataAdvertisesContactExport(t *testing.T) {
	manifest := controlManifest()
	command, ok := manifest.Commands["contact-export"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if command.Mutates || !command.JSON {
		t.Fatalf("contact-export command = %#v", command)
	}
	want := []string{"telecrawl", "--json", "contacts", "export"}
	if !slices.Equal(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
	openCommand, ok := manifest.Commands["open"]
	if !ok {
		t.Fatalf("commands = %#v, want open", manifest.Commands)
	}
	if openCommand.Mutates || !openCommand.JSON {
		t.Fatalf("open command = %#v", openCommand)
	}
	if !slices.Contains(manifest.Capabilities, "open") {
		t.Fatalf("capabilities = %#v, want open", manifest.Capabilities)
	}
	if !slices.Contains(manifest.Capabilities, "who") {
		t.Fatalf("capabilities = %#v, want who", manifest.Capabilities)
	}
	if !slices.Contains(manifest.Capabilities, "verbose_logs") {
		t.Fatalf("capabilities = %#v, want verbose_logs", manifest.Capabilities)
	}
	if manifest.Paths.DefaultLogs != defaultLogDir() {
		t.Fatalf("default logs = %q, want %q", manifest.Paths.DefaultLogs, defaultLogDir())
	}
}

func TestVerboseLogsWriteFileAndStreamToStderr(t *testing.T) {
	clearTestLog(t)
	logPath := filepath.Join(defaultLogDir(), telecrawlLogFileName)

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

func TestImportVerboseLogsPhaseTimings(t *testing.T) {
	clearTestLog(t)
	source := makePostboxFixture(t)
	db := filepath.Join(t.TempDir(), "telecrawl.db")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"-vv", "--db", db, "--json", "import", "--path", source, "--dialogs-limit", "1", "--messages-limit", "1"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("import -vv error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	logText := readTestLog(t)
	for _, want := range []string{
		"import_done: messages=1",
		"chats=1",
		"media_messages=1",
		"import_phase: source=telegram",
		"import_ms=",
		"write_ms=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("import log missing %q:\n%s", want, logText)
		}
	}
	if !strings.Contains(stderr.String(), "import_done: messages=1") || !strings.Contains(stderr.String(), "import_phase: source=telegram") {
		t.Fatalf("-vv stderr missing import log lines:\n%s", stderr.String())
	}
}

func TestStoreImportResultPreservesArchivedMediaOnReimport(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Unix(1_800_000_000, 0).UTC()
	archivedPath := filepath.Join(t.TempDir(), "media", "abc")
	if err := os.MkdirAll(filepath.Dir(archivedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivedPath, []byte("archived"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		Chats: []store.Chat{{JID: "100", Kind: "chat", Name: "saved media", LastMessageAt: now, MessageCount: 1}},
		Messages: []store.Message{{
			SourcePK:  9,
			ChatJID:   "100",
			ChatName:  "saved media",
			MessageID: "0:9",
			Timestamp: now,
			MediaType: "photo",
			MediaPath: archivedPath,
			MediaSize: 123,
		}},
	}
	if err := storeImportResult(ctx, st, &first, ""); err != nil {
		t.Fatal(err)
	}

	second := telegramdesktop.ImportResult{
		Stats: first.Stats,
		Chats: first.Chats,
		Messages: []store.Message{{
			SourcePK:  9,
			ChatJID:   "100",
			ChatName:  "saved media",
			MessageID: "0:9",
			Timestamp: now,
		}},
	}
	if err := storeImportResult(ctx, st, &second, ""); err != nil {
		t.Fatal(err)
	}
	if second.Stats.MediaMessages != 1 || second.Stats.MediaFiles != 1 || second.Stats.MediaBytes != 123 {
		t.Fatalf("refreshed stats = %+v, want preserved media stats", second.Stats)
	}

	messages, err := st.Messages(ctx, store.MessageFilter{HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].MediaPath != archivedPath || messages[0].MediaSize != 123 {
		t.Fatalf("media ref = path %q size %d, want %q/123", messages[0].MediaPath, messages[0].MediaSize, archivedPath)
	}
	if messages[0].MediaType != "photo" {
		t.Fatalf("media type = %q, want preserved photo", messages[0].MediaType)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.MediaMessages != 1 {
		t.Fatalf("media_messages = %d, want 1", status.MediaMessages)
	}

	otherSource := telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "other-postbox", StartedAt: now, FinishedAt: now},
		Chats: first.Chats,
		Messages: []store.Message{{
			SourcePK:  9,
			ChatJID:   "100",
			ChatName:  "saved media",
			MessageID: "0:9",
			Timestamp: now,
			MediaType: "photo",
		}},
	}
	if err := storeImportResult(ctx, st, &otherSource, ""); err != nil {
		t.Fatal(err)
	}
	messages, err = st.Messages(ctx, store.MessageFilter{HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages after source switch = %d, want 1", len(messages))
	}
	if messages[0].MediaPath != "" || messages[0].MediaSize != 0 {
		t.Fatalf("media ref crossed source boundary: path %q size %d", messages[0].MediaPath, messages[0].MediaSize)
	}
}

func TestPrintImportStatsIncludesMediaArchiveStats(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0).UTC()
	r := &runtime{stdout: &out}

	if err := r.print(store.ImportStats{
		SourcePath:    "postbox",
		DBPath:        "/tmp/telecrawl.db",
		Chats:         2,
		Messages:      3,
		MediaMessages: 2,
		MediaFiles:    1,
		MediaBytes:    1234,
		StartedAt:     now,
		FinishedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"media_files: 1\n", "media_bytes: 1234\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "remote_media_downloads:") || strings.Contains(out.String(), "remote_media_missing:") {
		t.Fatalf("zero remote media stats should be omitted:\n%s", out.String())
	}
}

func TestPrintImportStatsIncludesRemoteMediaWhenUsed(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0).UTC()
	r := &runtime{stdout: &out}

	if err := r.print(store.ImportStats{
		SourcePath:             "postbox",
		DBPath:                 "/tmp/telecrawl.db",
		RemoteMediaCandidates:  4,
		RemoteMediaAttempted:   3,
		RemoteMediaDownloads:   2,
		RemoteMediaMissing:     1,
		RemoteMediaUnavailable: 1,
		RemoteMediaTimeouts:    0,
		RemoteMediaErrors:      0,
		StartedAt:              now,
		FinishedAt:             now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"remote_media_candidates: 4\n",
		"remote_media_attempted: 3\n",
		"remote_media_downloads: 2\n",
		"remote_media_missing: 1\n",
		"remote_media_unavailable: 1\n",
		"remote_media_timeouts: 0\n",
		"remote_media_errors: 0\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestPrintImportStatsIncludesRemoteMediaDiagnosticsWithoutDownloads(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0).UTC()
	r := &runtime{stdout: &out}

	if err := r.print(store.ImportStats{
		SourcePath:             "postbox",
		DBPath:                 "/tmp/telecrawl.db",
		RemoteMediaCandidates:  4,
		RemoteMediaAttempted:   4,
		RemoteMediaUnavailable: 4,
		StartedAt:              now,
		FinishedAt:             now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"remote_media_candidates: 4\n",
		"remote_media_attempted: 4\n",
		"remote_media_downloads: 0\n",
		"remote_media_missing: 0\n",
		"remote_media_unavailable: 4\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestUsageDocumentsMediaFetchOptIn(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	printUsage(&out)
	if !strings.Contains(out.String(), "--fetch-media") {
		t.Fatalf("usage should document media fetch opt-in:\n%s", out.String())
	}
}

func accountScopedImportResult(label string) telegramdesktop.ImportResult {
	now := time.Unix(1_800_000_000, 0).UTC()
	return telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		Contacts: []store.Contact{
			{JID: "111", FullName: "Account A"},
			{JID: "10", FullName: "Sender A"},
			{JID: "222", FullName: "Account B"},
			{JID: "20", FullName: "Sender B"},
			{JID: "999", FullName: "Unrelated"},
		},
		Chats: []store.Chat{
			{JID: "111", Kind: "chat", Name: "account a", LastMessageAt: now, MessageCount: 1},
			{JID: "222", Kind: "chat", Name: "account b", LastMessageAt: now, MessageCount: 1},
		},
		Messages: []store.Message{
			{SourcePK: 1, ChatJID: "111", ChatName: "account a", MessageID: "0:1", SenderJID: "10", Timestamp: now, Text: label + " a"},
			{SourcePK: 2, ChatJID: "222", ChatName: "account b", MessageID: "0:1", SenderJID: "20", Timestamp: now, Text: label + " b"},
		},
	}
}

func readTestLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(defaultLogDir(), telecrawlLogFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func clearTestLog(t *testing.T) {
	t.Helper()
	path := filepath.Join(defaultLogDir(), telecrawlLogFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func makePostboxFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	lane := filepath.Join(root, "stable")
	account := filepath.Join(lane, "account-123")
	dbDir := filepath.Join(account, "postbox", "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyAndSalt := make([]byte, 48)
	for i := range keyAndSalt {
		keyAndSalt[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(lane, ".tempkeyEncrypted"), encryptedTempKeyFixture(t, []byte("no-matter-key"), keyAndSalt), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureDB := filepath.Join("..", "telegramdesktop", "postbox", "testdata", "sqlcipher_v4.db")
	if err := copyFile(filepath.Join(dbDir, "db_sqlite"), fixtureDB); err != nil {
		t.Fatal(err)
	}
	return root
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func encryptedTempKeyFixture(t *testing.T, passcode []byte, keyAndSalt []byte) []byte {
	t.Helper()
	plain := make([]byte, 64)
	copy(plain, keyAndSalt)
	binary.LittleEndian.PutUint32(plain[48:52], uint32(tempkeyMurmur3(keyAndSalt)))
	digest := sha512.Sum512(passcode)
	block, err := aes.NewCipher(digest[:32])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, digest[48:]).CryptBlocks(out, plain)
	return out
}

func tempkeyMurmur3(data []byte) int32 {
	const seed uint32 = 0xf7ca7fd2
	const c1 uint32 = 0xcc9e2d51
	const c2 uint32 = 0x1b873593
	length := len(data)
	hash := seed
	i := 0
	for ; i+4 <= length; i += 4 {
		k := binary.LittleEndian.Uint32(data[i : i+4])
		k *= c1
		k = (k << 15) | (k >> 17)
		k *= c2
		hash ^= k
		hash = (hash << 13) | (hash >> 19)
		hash = hash*5 + 0xe6546b64
	}
	var k uint32
	switch length & 3 {
	case 3:
		k ^= uint32(data[i+2]) << 16
		fallthrough
	case 2:
		k ^= uint32(data[i+1]) << 8
		fallthrough
	case 1:
		k ^= uint32(data[i])
		k *= c1
		k = (k << 15) | (k >> 17)
		k *= c2
		hash ^= k
	}
	hash ^= uint32(length)
	hash ^= hash >> 16
	hash *= 0x85ebca6b
	hash ^= hash >> 13
	hash *= 0xc2b2ae35
	hash ^= hash >> 16
	return int32(hash)
}
