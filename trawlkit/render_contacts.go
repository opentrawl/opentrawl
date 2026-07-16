package trawlkit

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func writeContactsText(w io.Writer, value *control.ContactExport) error {
	if value == nil || len(value.Contacts) == 0 {
		_, err := fmt.Fprintln(w, "No contacts.")
		return err
	}
	if _, err := fmt.Fprintf(w, "Contacts: showing %s of %s.\n\n", render.FormatInteger(int64(len(value.Contacts))), render.FormatInteger(int64(len(value.Contacts)))); err != nil {
		return err
	}
	rows := make([][]string, 0, len(value.Contacts))
	for _, contact := range value.Contacts {
		rows = append(rows, []string{contact.DisplayName, strings.Join(contactIdentifiers(contact), ", ")})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}

func contactIdentifiers(contact control.Contact) []string {
	out := append([]string(nil), contact.EmailAddresses...)
	out = append(out, humanPhones(contact.PhoneNumbers)...)
	providers := make([]string, 0, len(contact.Accounts))
	for provider := range contact.Accounts {
		providers = append(providers, provider)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		values := append([]string(nil), contact.Accounts[provider]...)
		sort.Strings(values)
		for _, value := range values {
			out = append(out, provider+":"+value)
		}
	}
	return out
}

func humanPhones(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, render.FormatPhone(value))
	}
	return out
}
