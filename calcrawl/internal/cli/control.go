package cli

import (
	"errors"
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

const contractVersion = 1

type manifestOutput struct {
	control.Manifest
	ContractVersion      int    `json:"contract_version"`
	Version              string `json:"version"`
	ArchiveSchemaVersion int    `json:"archive_schema_version"`
}

func (r *runtime) runMetadata(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"metadata"})
	}
	fs, err := r.parseNoFlags("metadata", args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("metadata takes no arguments"))
	}
	return r.print(controlManifest())
}

func controlManifest() manifestOutput {
	m := control.NewManifest(archive.AppID, archive.DisplayName, "calcrawl")
	m.Description = "Local-first Apple Calendar archive crawler."
	m.Branding = control.Branding{SymbolName: "calendar", AccentColor: "#ff9500", BundleIdentifier: "com.apple.iCal"}
	m.Paths = control.Paths{
		DefaultDatabase: archive.DefaultPath(),
		DefaultCache:    filepath.Join(defaultBaseDir(), "cache"),
		DefaultLogs:     filepath.Join(defaultBaseDir(), "logs"),
	}
	m.Capabilities = []string{"metadata", "status", "sync", "search", "open", "doctor", "contacts_export", "who", "short_refs"}
	m.Privacy = control.Privacy{
		ContainsPrivateMessages: true,
		ExportsSecrets:          false,
		LocalOnlyScopes:         []string{"apple-calendar", "sqlite", "calendar-event-search", "contact-export"},
	}
	m.Commands = map[string]control.Command{
		"metadata":        {Title: "Metadata", Argv: []string{"calcrawl", "metadata", "--json"}, JSON: true},
		"status":          {Title: "Status", Argv: []string{"calcrawl", "status", "--json"}, JSON: true},
		"sync":            {Title: "Sync", Argv: []string{"calcrawl", "sync", "--json"}, JSON: true, Mutates: true},
		"search":          {Title: "Search", Argv: []string{"calcrawl", "search", "QUERY", "--json"}, JSON: true},
		"open":            {Title: "Open", Argv: []string{"calcrawl", "open", "REF", "--json"}, JSON: true},
		"doctor":          {Title: "Doctor", Argv: []string{"calcrawl", "doctor", "--json"}, JSON: true},
		"contacts_export": {Title: "Export contacts", Argv: []string{"calcrawl", "contacts", "export", "--json"}, JSON: true},
	}
	return manifestOutput{
		Manifest:             m,
		ContractVersion:      contractVersion,
		Version:              version,
		ArchiveSchemaVersion: archive.SchemaVersion,
	}
}
