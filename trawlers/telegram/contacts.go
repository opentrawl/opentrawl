package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/flags"
)

func (c *Crawler) runContacts(ctx context.Context, req *trawlkit.Request) error {
	r := c.handler(ctx, req)
	if len(req.Args) != 0 {
		return usageErr(errors.New("contacts takes flags only"))
	}
	n, err := flags.Limit(c.contacts.Limit, c.contacts.LimitSet)
	if err != nil {
		return usageErr(err)
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		contacts, err := st.ListContacts(r.ctx, n)
		if err != nil {
			return err
		}
		if r.json {
			return r.print(contactJSONRows(contacts))
		}
		total, err := st.CountContacts(r.ctx)
		if err != nil {
			return err
		}
		return r.print(contactsEnvelope{Contacts: contacts, Total: total})
	})
}

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.Request) (*control.PeopleSnapshot, error) {
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	contacts, err := st.ListContacts(ctx, 0)
	if err != nil {
		return nil, err
	}
	exported := exportContacts(contacts)
	out := make([]control.Contact, 0, len(exported))
	for _, contact := range exported {
		var phones []string
		for _, phone := range contact.PhoneNumbers {
			if phone = strings.TrimSpace(phone); phone != "" {
				phones = append(phones, phone)
			}
		}
		accounts := make(map[string][]string, len(contact.Accounts))
		for provider, values := range contact.Accounts {
			accounts[provider] = append([]string(nil), values...)
		}
		if len(phones) == 0 && len(accounts) == 0 {
			continue
		}
		out = append(out, control.Contact{SourceID: contact.SourceID, DisplayName: contact.DisplayName, PhoneNumbers: phones, Accounts: accounts})
	}
	return &control.PeopleSnapshot{Contacts: out}, nil
}

type exportContact struct {
	SourceID     string              `json:"-"`
	DisplayName  string              `json:"display_name"`
	PhoneNumbers []string            `json:"phone_numbers,omitempty"`
	Accounts     map[string][]string `json:"accounts,omitempty"`
}

func exportContacts(contacts []store.Contact) []exportContact {
	out := make([]exportContact, 0, len(contacts))
	byKey := map[string]store.Contact{}
	keys := make([]string, 0, len(contacts))
	for _, contact := range contacts {
		if isTelegramServiceContact(contact) || !isExportablePeerContact(contact) {
			continue
		}
		name := exportContactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		username := cleanTelegramUsername(contact.Username)
		if name == "" || (phone == "" && username == "") {
			continue
		}
		key := exportContactKey(phone, username)
		if current, ok := byKey[key]; ok {
			stableJID := stableTelegramContactJID(current.JID, contact.JID)
			if preferPeopleSnapshotName(contact, current) {
				contact = mergePeopleSnapshotIdentifiers(contact, current)
				contact.JID = stableJID
				byKey[key] = contact
			} else {
				current = mergePeopleSnapshotIdentifiers(current, contact)
				current.JID = stableJID
				byKey[key] = current
			}
		} else {
			byKey[key] = contact
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		contact := byKey[key]
		exported := exportContact{SourceID: strings.TrimSpace(contact.JID), DisplayName: exportContactDisplayName(contact)}
		if phone := strings.TrimSpace(contact.Phone); phone != "" {
			exported.PhoneNumbers = []string{phone}
		}
		if username := cleanTelegramUsername(contact.Username); username != "" {
			exported.Accounts = map[string][]string{"telegram": {username}}
		}
		out = append(out, exported)
	}
	return out
}

func stableTelegramContactJID(left, right string) string {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right != "" && right < left {
		return right
	}
	return left
}

func mergePeopleSnapshotIdentifiers(base, extra store.Contact) store.Contact {
	if strings.TrimSpace(base.Phone) == "" {
		base.Phone = strings.TrimSpace(extra.Phone)
	}
	if strings.TrimSpace(base.Username) == "" {
		base.Username = strings.TrimSpace(extra.Username)
	}
	return base
}

func isExportablePeerContact(contact store.Contact) bool {
	peerType := strings.TrimSpace(contact.PeerType)
	return peerType == "" || peerType == "user"
}

func exportContactKey(phone, username string) string {
	if phone != "" {
		return "phone:" + phone
	}
	return "telegram:" + strings.ToLower(username)
}

func exportContactDisplayName(contact store.Contact) string {
	if name := contactDisplayName(contact); name != "" {
		return name
	}
	return cleanTelegramUsername(contact.Username)
}

func cleanTelegramUsername(username string) string {
	return strings.TrimSpace(strings.TrimPrefix(username, "@"))
}

func preferPeopleSnapshotName(candidate, current store.Contact) bool {
	if candidate.UpdatedAt.After(current.UpdatedAt) {
		return true
	}
	if current.UpdatedAt.After(candidate.UpdatedAt) {
		return false
	}
	return len([]rune(contactDisplayName(candidate))) > len([]rune(contactDisplayName(current)))
}

func contactDisplayName(contact store.Contact) string {
	return store.ContactDisplayName(contact)
}

func sameContactText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}

func isTelegramServiceContact(contact store.Contact) bool {
	return strings.TrimSpace(contact.Phone) == "42777" &&
		sameContactText(contact.FullName, "Telegram") &&
		sameContactText(contact.FirstName, "Telegram") &&
		strings.TrimSpace(contact.LastName) == "" &&
		strings.TrimSpace(contact.Username) == ""
}
