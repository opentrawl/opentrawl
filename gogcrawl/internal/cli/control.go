package cli

import (
	"github.com/openclaw/crawlkit/control"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

func controlManifest() control.Manifest {
	paths := archive.DefaultPaths()
	m := control.NewManifest("gogcrawl", "Gmail", "gogcrawl")
	m.Version = version
	m.Description = "Local-first Gmail archive crawler backed by the gog CLI."
	m.Branding = control.Branding{SymbolName: "envelope.fill", AccentColor: "#4285f4"}
	m.Paths = control.Paths{
		DefaultConfig:   paths.ConfigPath,
		DefaultDatabase: paths.DBPath,
		DefaultCache:    paths.CacheDir,
		DefaultLogs:     paths.LogDir,
		DefaultShare:    paths.ShareDir,
	}
	m.Capabilities = []string{"metadata", "status", "sync", "search", "open", "doctor", "contacts_export", "short_refs", "who", "verbose_logs"}
	m.Privacy = control.Privacy{
		ContainsPrivateMessages: true,
		ExportsSecrets:          false,
		LocalOnlyScopes:         []string{"gmail", "google-contacts", "sqlite", "message-archive", "message-text-search"},
	}
	m.Commands = map[string]control.Command{
		"metadata":        {Title: "Metadata", Argv: []string{"gogcrawl", "metadata", "--json"}, JSON: true},
		"status":          {Title: "Status", Argv: []string{"gogcrawl", "status", "--json"}, JSON: true},
		"sync":            {Title: "Sync", Argv: []string{"gogcrawl", "sync", "--json"}, JSON: true, Mutates: true},
		"search":          {Title: "Search", Argv: []string{"gogcrawl", "search", "QUERY", "--json"}, JSON: true},
		"who":             {Title: "Resolve who", Argv: []string{"gogcrawl", "who", "NAME", "--json"}, JSON: true},
		"open":            {Title: "Open", Argv: []string{"gogcrawl", "open", "REF", "--json"}, JSON: true},
		"doctor":          {Title: "Doctor", Argv: []string{"gogcrawl", "doctor", "--json"}, JSON: true},
		"contacts_export": {Title: "Export contacts", Argv: []string{"gogcrawl", "contacts", "export", "--json"}, JSON: true},
	}
	return m
}
