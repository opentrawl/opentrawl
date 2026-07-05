package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
)

func controlManifest() control.Manifest {
	m := control.NewManifest("telecrawl", "Telegram", "telecrawl")
	m.Version = version
	m.Description = "Local-first Telegram archive crawler."
	m.Branding = control.Branding{SymbolName: "paperplane.fill", AccentColor: "#229ed9", BundleIdentifier: "org.telegram.desktop"}
	paths := defaultPaths()
	m.Paths = control.Paths{
		DefaultConfig:   filepath.Join(paths.BaseDir, "backup.toml"),
		DefaultDatabase: paths.DBPath,
		DefaultCache:    paths.CacheDir,
		DefaultLogs:     paths.LogDir,
	}
	m.Capabilities = []string{"metadata", "doctor", "status", "sync", "search", "open", "who", "short_refs", "contacts_export", "backup", "verbose_logs"}
	m.Privacy = control.Privacy{ContainsPrivateMessages: true, ExportsSecrets: false, LocalOnlyScopes: []string{"telegram-desktop", "telegram-macos-postbox", "sqlite", "encrypted-git-backup"}}
	// Every user-facing verb, so `trawl telegram` lists and dispatches them
	// (TRAWL-86). Argv is the canonical shape: binary, the literal subcommand
	// path, UPPERCASE placeholders for user args, then a trailing --json for the
	// verbs that emit it. The verb a user types is Argv minus the binary and the
	// trailing --json. `metadata` stays out — it is the discovery probe, not a
	// namespace verb.
	m.Commands = map[string]control.Command{
		"status":           {Title: "Status", Argv: []string{"telecrawl", "status", "--json"}, JSON: true},
		"sync":             {Title: "Import", Argv: []string{"telecrawl", "import", "--json"}, JSON: true, Mutates: true},
		"doctor":           {Title: "Doctor", Argv: []string{"telecrawl", "doctor", "--json"}, JSON: true},
		"chats":            {Title: "Chats", Argv: []string{"telecrawl", "chats", "--json"}, JSON: true},
		"topics":           {Title: "Topics", Argv: []string{"telecrawl", "topics", "--json"}, JSON: true},
		"messages":         {Title: "Messages", Argv: []string{"telecrawl", "messages", "--json"}, JSON: true},
		"contacts":         {Title: "Contacts", Argv: []string{"telecrawl", "contacts", "--json"}, JSON: true},
		"folders":          {Title: "Folders", Argv: []string{"telecrawl", "folders", "--json"}, JSON: true},
		"search":           {Title: "Search", Argv: []string{"telecrawl", "search", "QUERY", "--json"}, JSON: true},
		"who":              {Title: "Resolve people", Argv: []string{"telecrawl", "who", "NAME", "--json"}, JSON: true},
		"open":             {Title: "Open", Argv: []string{"telecrawl", "open", "REF", "--json"}, JSON: true},
		"contact-export":   {Title: "Export contacts", Argv: []string{"telecrawl", "contacts", "export", "--json"}, JSON: true},
		"backup-init":      {Title: "Init backup", Argv: []string{"telecrawl", "backup", "init", "--json"}, JSON: true, Mutates: true},
		"backup-push":      {Title: "Push backup", Argv: []string{"telecrawl", "backup", "push", "--json"}, JSON: true, Mutates: true},
		"backup-pull":      {Title: "Pull backup", Argv: []string{"telecrawl", "backup", "pull", "--json"}, JSON: true, Mutates: true},
		"backup-status":    {Title: "Backup status", Argv: []string{"telecrawl", "backup", "status", "--json"}, JSON: true},
		"backup-snapshots": {Title: "Backup snapshots", Argv: []string{"telecrawl", "backup", "snapshots", "--json"}, JSON: true},
		"version":          {Title: "Version", Argv: []string{"telecrawl", "version"}},
	}
	return m
}
