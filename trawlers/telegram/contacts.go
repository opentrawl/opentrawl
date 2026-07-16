package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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

func (c *Crawler) ContactExport(ctx context.Context, req *trawlkit.Request) (*control.ContactExport, error) {
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
		out = append(out, control.Contact{DisplayName: contact.DisplayName, PhoneNumbers: phones, Accounts: accounts})
	}
	return &control.ContactExport{Contacts: out}, nil
}

type contactExport struct {
	Contacts []exportContact `json:"contacts"`
}

type exportContact struct {
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
			if preferContactExportName(contact, current) {
				contact = mergeContactExportIdentifiers(contact, current)
				byKey[key] = contact
			} else {
				byKey[key] = mergeContactExportIdentifiers(current, contact)
			}
		} else {
			byKey[key] = contact
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		contact := byKey[key]
		exported := exportContact{DisplayName: exportContactDisplayName(contact)}
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

func mergeContactExportIdentifiers(base, extra store.Contact) store.Contact {
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

func preferContactExportName(candidate, current store.Contact) bool {
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

func (r *runtime) printContactExport(value contactExport) error {
	for _, contact := range value.Contacts {
		if _, err := fmt.Fprintf(r.stdout, "%s\t%s\n", contact.DisplayName, strings.Join(contactIdentifiers(contact), ", ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(r.stdout, "%s\n", countNoun(len(value.Contacts), "contact", "contacts"))
	return err
}

func contactIdentifiers(contact exportContact) []string {
	identifiers := make([]string, 0, len(contact.PhoneNumbers)+len(contact.Accounts))
	for _, phone := range contact.PhoneNumbers {
		if phone = strings.TrimSpace(phone); phone != "" {
			identifiers = append(identifiers, phone)
		}
	}
	providers := make([]string, 0, len(contact.Accounts))
	for provider := range contact.Accounts {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		values := append([]string(nil), contact.Accounts[provider]...)
		sort.Strings(values)
		for _, value := range values {
			if identifier := accountIdentifier(provider, value); identifier != "" {
				identifiers = append(identifiers, identifier)
			}
		}
	}
	return identifiers
}

func accountIdentifier(provider, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "telegram":
		return "@" + strings.TrimPrefix(value, "@")
	default:
		provider = strings.TrimSpace(provider)
		if provider == "" {
			return value
		}
		return provider + ":" + value
	}
}

func countNoun(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}
