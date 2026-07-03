package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

type metadataEnvelope struct {
	SchemaVersion   int                        `json:"schema_version"`
	ContractVersion int                        `json:"contract_version"`
	ID              string                     `json:"id"`
	DisplayName     string                     `json:"display_name"`
	Version         string                     `json:"version"`
	Description     string                     `json:"description,omitempty"`
	Binary          control.Binary             `json:"binary"`
	Branding        control.Branding           `json:"branding"`
	Paths           control.Paths              `json:"paths"`
	Commands        map[string]control.Command `json:"commands"`
	Capabilities    []string                   `json:"capabilities"`
	Privacy         control.Privacy            `json:"privacy"`
}

func controlManifest() metadataEnvelope {
	paths, _ := archive.DefaultPaths()
	return metadataEnvelope{
		SchemaVersion:   1,
		ContractVersion: 1,
		ID:              "gogcrawl",
		DisplayName:     "Gmail",
		Version:         version,
		Description:     "Local-first Gmail archive crawler backed by the gog CLI.",
		Binary:          control.Binary{Name: "gogcrawl"},
		Branding:        control.Branding{SymbolName: "envelope.fill", AccentColor: "#4285f4"},
		Paths: control.Paths{
			DefaultConfig:   paths.ConfigPath,
			DefaultDatabase: archive.DefaultPath(),
			DefaultCache:    filepath.Join(paths.BaseDir, "cache"),
			DefaultLogs:     filepath.Join(paths.BaseDir, "logs"),
			DefaultShare:    filepath.Join(paths.BaseDir, "share"),
		},
		Capabilities: []string{"metadata", "status", "sync", "search", "open", "doctor", "contacts_export", "short_refs", "who"},
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"gmail", "google-contacts", "sqlite", "message-archive", "message-text-search"},
		},
		Commands: map[string]control.Command{
			"metadata":        {Title: "Metadata", Argv: []string{"gogcrawl", "metadata", "--json"}, JSON: true},
			"status":          {Title: "Status", Argv: []string{"gogcrawl", "status", "--json"}, JSON: true},
			"sync":            {Title: "Sync", Argv: []string{"gogcrawl", "sync", "--json"}, JSON: true, Mutates: true},
			"search":          {Title: "Search", Argv: []string{"gogcrawl", "search", "<query>", "--json"}, JSON: true},
			"who":             {Title: "Resolve who", Argv: []string{"gogcrawl", "who", "<name>", "--json"}, JSON: true},
			"open":            {Title: "Open", Argv: []string{"gogcrawl", "open", "<ref>", "--json"}, JSON: true},
			"doctor":          {Title: "Doctor", Argv: []string{"gogcrawl", "doctor", "--json"}, JSON: true},
			"contacts_export": {Title: "Export contacts", Argv: []string{"gogcrawl", "contacts", "export", "--json"}, JSON: true},
		},
	}
}
