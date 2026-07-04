package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/clawdex/internal/contactexport"
)

func printContactExportText(w io.Writer, export contactexport.ContactExport) error {
	width := textOutputWidth(w)
	rows := make([][]string, 0, len(export.Contacts))
	for _, contact := range export.Contacts {
		rows = append(rows, []string{
			contact.DisplayName,
			countNoun(contactIdentifierCount(contact), "identifier", "identifiers"),
			countNoun(len(contact.Addresses), "address", "addresses"),
		})
	}
	if err := renderTextTable(w, contactExportTableColumns(width), rows); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "%s\n", countNoun(len(export.Contacts), "contact", "contacts"))
	return err
}

func contactExportTableColumns(width int) []textColumn {
	whoWidth := 30
	identifierWidth := 14
	if width < 90 {
		whoWidth = 24
	}
	addressWidth := textColumnWidth(width, whoWidth, identifierWidth)
	return []textColumn{
		{header: "WHO", width: whoWidth, wrap: true},
		{header: "IDENTIFIERS", width: identifierWidth},
		{header: "ADDRESSES", width: addressWidth, wrap: true},
	}
}

func contactIdentifierCount(contact contactexport.Contact) int {
	return len(contact.PhoneNumbers) +
		len(contact.Emails) +
		contactAccountValueCount(contact.Accounts) +
		contactAccountValueCount(contact.Handles)
}

func contactAccountValueCount(accounts map[string][]string) int {
	count := 0
	for _, values := range accounts {
		for _, value := range values {
			if strings.TrimSpace(value) != "" {
				count++
			}
		}
	}
	return count
}
