package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/gog"
)

func (r *runtime) runContacts(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"contacts"})
	}
	if len(args) == 0 {
		return usageErr(errors.New("usage: gogcrawl contacts export"))
	}
	switch args[0] {
	case "export":
		return r.runContactsExport(args[1:])
	default:
		return usageErr(fmt.Errorf("unknown contacts command %q", args[0]))
	}
}

func (r *runtime) runContactsExport(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"contacts", "export"})
	}
	if len(args) != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	contacts, err := r.exportContacts()
	if err != nil {
		return commandErr("gog_contacts_failed", "gog could not list Google contacts", "run gog auth list --check --plain, then gog login <email> if auth is invalid", err)
	}
	export := control.ContactExport{Contacts: contacts}
	if err := control.ValidateContactExport(export); err != nil {
		return err
	}
	return r.print(export)
}

func (r *runtime) exportContacts() ([]control.Contact, error) {
	var out []control.Contact
	pageToken := ""
	for {
		page, err := r.gog.Contacts(r.ctx, gog.DefaultPageSize, pageToken)
		if err != nil {
			return nil, err
		}
		for _, contact := range page.Contacts {
			name := strings.TrimSpace(contact.Name)
			phone := strings.TrimSpace(contact.Phone)
			if name == "" || phone == "" {
				continue
			}
			out = append(out, control.Contact{DisplayName: name, PhoneNumbers: []string{phone}})
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		pageToken = page.NextPageToken
	}
}
