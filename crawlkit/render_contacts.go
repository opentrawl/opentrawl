package crawlkit

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
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
		rows = append(rows, []string{contact.DisplayName, strings.Join(humanPhones(contact.PhoneNumbers), ", ")})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "name", Wrap: true},
		{Header: "phone"},
	}, rows)
}

func humanPhones(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, render.FormatPhone(value))
	}
	return out
}
