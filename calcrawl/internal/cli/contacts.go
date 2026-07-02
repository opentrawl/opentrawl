package cli

import (
	"errors"
	"fmt"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

func (r *runtime) runContacts(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"contacts"})
	}
	if len(args) == 0 {
		return usageErr(errors.New("usage: calcrawl contacts export"))
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
	fs, err := r.parseNoFlags("contacts export", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("contacts export takes no arguments"))
	}
	st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	contacts, err := st.ExportContacts(r.ctx)
	if err != nil {
		return err
	}
	export := control.ContactExport{Contacts: contacts}
	if err := control.ValidateContactExport(export); err != nil {
		return err
	}
	return r.print(export)
}
