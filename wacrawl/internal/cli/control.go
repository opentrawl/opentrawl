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
	m.Capabilities = []string{"metadata", "doctor", "status", "sync", "search", "open", "backup", "contacts_export", "who", "short_refs", "verbose_logs"}
	m.Privacy = control.Privacy{ContainsPrivateMessages: true, ExportsSecrets: false, LocalOnlyScopes: []string{"whatsapp-desktop", "sqlite", "encrypted-git-backup"}}
	// Argv is the child command a caller runs: binary, the literal subcommand
	// path, UPPERCASE placeholder args, then a trailing --json. trawl derives
	// the verb a user types from this (argv minus binary and trailing --json),
	// so --json must be last and placeholders must be UPPERCASE. metadata and
	// backup stay out: metadata is the machine discovery probe, backup is an
	// operational verb, not a query namespace (matches the imsgcrawl pilot).
	m.Commands = map[string]control.Command{
		"status":         {Title: "Status", Argv: []string{"wacrawl", "status", "--json"}, JSON: true},
		"sync":           {Title: "Sync", Argv: []string{"wacrawl", "sync", "--json"}, JSON: true, Mutates: true},
		"doctor":         {Title: "Doctor", Argv: []string{"wacrawl", "doctor", "--json"}, JSON: true},
		"chats":          {Title: "Chats", Argv: []string{"wacrawl", "chats", "--json"}, JSON: true},
		"unread":         {Title: "Unread chats", Argv: []string{"wacrawl", "unread", "--json"}, JSON: true},
		"messages":       {Title: "Messages", Argv: []string{"wacrawl", "messages", "--json"}, JSON: true},
		"search":         {Title: "Search", Argv: []string{"wacrawl", "search", "QUERY", "--json"}, JSON: true},
		"who":            {Title: "Resolve who", Argv: []string{"wacrawl", "who", "NAME", "--json"}, JSON: true},
		"open":           {Title: "Open", Argv: []string{"wacrawl", "open", "REF", "--json"}, JSON: true},
		"contact-export": {Title: "Export contacts", Argv: []string{"wacrawl", "contacts", "export", "--json"}, JSON: true},
	}
	return m
}
