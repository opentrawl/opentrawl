package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
			JID:       "165355235",
			PeerType:  "user",
			FullName:  "Jef Hellemans",
			FirstName: "Jef",
			LastName:  "Hellemans",
			Username:  "JefHellemans",
		}},
		Chats: []store.Chat{{
			JID:           "165355235",
			Kind:          "user",
			Name:          "Jef Hellemans",
			Username:      "JefHellemans",
			LastMessageAt: now,
			MessageCount:  1,
		}},
		Messages: []store.Message{{
			SourcePK:   1,
			ChatJID:    "165355235",
			ChatName:   "Jef Hellemans",
			MessageID:  "1",
			SenderJID:  "165355235",
			SenderName: "Jef Hellemans",
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
	if len(exported.Contacts) != 1 || exported.Contacts[0].JID != "165355235" || exported.Contacts[0].Username != "JefHellemans" {
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
	if contact.DisplayName != "Jef Hellemans" || len(contact.PhoneNumbers) != 0 || len(contact.Accounts["telegram"]) != 1 || contact.Accounts["telegram"][0] != "JefHellemans" {
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
	for _, want := range []string{"Jef Hellemans\t@JefHellemans", "1 contact"} {
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

func TestUnknownCommandIncludesHelpHint(t *testing.T) {
	stdout, stderr, err := runCLI(t, "nope")
	if err == nil {
		t.Fatalf("unknown command succeeded: stdout=%s stderr=%s", stdout, stderr)
	}
	if got, want := err.Error(), "unknown command \"nope\". Run 'telecrawl --help'."; got != want {
		t.Fatalf("error = %q, want %q", got, want)
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
			{JID: "111", Kind: "user", Name: "account a", LastMessageAt: now, MessageCount: 1},
			{JID: "222", Kind: "user", Name: "account b", LastMessageAt: now, MessageCount: 1},
		},
		Messages: []store.Message{
			{SourcePK: 1, ChatJID: "111", ChatName: "account a", MessageID: "0:1", SenderJID: "10", Timestamp: now, Text: label + " a"},
			{SourcePK: 2, ChatJID: "222", ChatName: "account b", MessageID: "0:1", SenderJID: "20", Timestamp: now, Text: label + " b"},
		},
	}
}
