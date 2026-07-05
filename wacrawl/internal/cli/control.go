package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
)

func controlManifest() control.Manifest {
	m := control.NewManifest("wacrawl", "WhatsApp", "wacrawl")
	m.Description = "Local-first WhatsApp Desktop archive crawler."
	m.Branding = control.Branding{SymbolName: "message.fill", AccentColor: "#25d366", BundleIdentifier: "net.whatsapp.WhatsApp"}
	paths := wacrawlPaths()
	m.Paths = control.Paths{
		DefaultConfig:   filepath.Join(paths.BaseDir, "backup.toml"),
		DefaultDatabase: paths.DBPath,
		DefaultCache:    paths.CacheDir,
		DefaultLogs:     paths.LogDir,
	}
	m.Capabilities = []string{"metadata", "doctor", "status", "sync", "search", "open", "sql", "backup", "contacts_export", "who", "short_refs", "verbose_logs"}
	m.Privacy = control.Privacy{ContainsPrivateMessages: true, ExportsSecrets: false, LocalOnlyScopes: []string{"whatsapp-desktop", "sqlite", "encrypted-git-backup"}}
	m.Commands = map[string]control.Command{
		"doctor":         {Title: "Doctor", Argv: []string{"wacrawl", "--json", "doctor"}, JSON: true},
		"status":         {Title: "Status", Argv: []string{"wacrawl", "--json", "status"}, JSON: true},
		"sync":           {Title: "Sync", Argv: []string{"wacrawl", "--json", "sync"}, JSON: true, Mutates: true},
		"search":         {Title: "Search", Argv: []string{"wacrawl", "--json", "search", "QUERY"}, JSON: true},
		"who":            {Title: "Resolve who", Argv: []string{"wacrawl", "--json", "who", "NAME"}, JSON: true},
		"open":           {Title: "Open", Argv: []string{"wacrawl", "--json", "open", "REF"}, JSON: true},
		"sql":            {Title: "SQL", Argv: []string{"wacrawl", "--json", "sql"}, JSON: true},
		"contact-export": {Title: "Export contacts", Argv: []string{"wacrawl", "--json", "contacts", "export"}, JSON: true},
	}
	return m
}
