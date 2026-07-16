package wacrawl

import (
	"context"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

func (c *Crawler) ContactExport(ctx context.Context, req *trawlkit.Request) (*control.ContactExport, error) {
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(err)
	}
	contacts, err := st.Contacts(ctx)
	if err != nil {
		return nil, err
	}
	return &control.ContactExport{Contacts: exportContacts(contacts)}, nil
}

func exportContacts(contacts []store.Contact) []control.Contact {
	out := make([]control.Contact, 0, len(contacts))
	seen := map[string]struct{}{}
	for _, contact := range contacts {
		name := contactDisplayName(contact)
		phone := strings.TrimSpace(contact.Phone)
		account := strings.TrimSpace(contact.JID)
		if account == "" {
			account = strings.TrimSpace(contact.LID)
		}
		if account == "" {
			account = strings.TrimSpace(contact.Username)
		}
		if name == "" || (phone == "" && account == "") {
			continue
		}
		key := name + "\x00" + phone + "\x00" + account
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		exported := control.Contact{DisplayName: name}
		if phone != "" {
			exported.PhoneNumbers = []string{phone}
		}
		if account != "" {
			exported.Accounts = map[string][]string{"whatsapp": {account}}
		}
		out = append(out, exported)
	}
	return out
}

func contactDisplayName(contact store.Contact) string {
	for _, name := range []string{
		contact.FullName,
		contact.BusinessName,
		strings.TrimSpace(contact.FirstName + " " + contact.LastName),
	} {
		if cleaned := cleanContactName(name, contact); cleaned != "" {
			return cleaned
		}
	}
	return ""
}

func cleanContactName(name string, contact store.Contact) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return ""
	case sameContactText(name, contact.Phone):
		return ""
	case sameContactText(name, contact.JID):
		return ""
	case sameContactText(name, contact.Username):
		return ""
	case sameContactText(name, contact.LID):
		return ""
	case strings.HasPrefix(name, "@"):
		return ""
	case looksLikePhone(name):
		return ""
	default:
		return name
	}
}

func sameContactText(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	return a != "" && b != "" && strings.EqualFold(a, b)
}
