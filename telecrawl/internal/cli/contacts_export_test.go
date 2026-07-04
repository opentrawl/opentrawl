package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
)

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
