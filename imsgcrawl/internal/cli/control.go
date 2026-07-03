package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func controlManifest() control.Manifest {
	m := control.NewManifest("imsgcrawl", "iMessage", "imsgcrawl")
	m.Description = "Local-first iMessage archive crawler."
	m.Branding = control.Branding{SymbolName: "message.fill", AccentColor: "#34c759", BundleIdentifier: "com.apple.MobileSMS"}
	m.Paths = control.Paths{
		DefaultDatabase: archive.DefaultPath(),
		DefaultCache:    filepath.Join(defaultBaseDir(), "cache"),
		DefaultLogs:     filepath.Join(defaultBaseDir(), "imsgcrawl", "logs"),
	}
	m.Capabilities = []string{"metadata", "status", "sync", "doctor", "chats", "messages", "search", "who", "short_refs", "open", "contact-export"}
	m.Privacy = control.Privacy{ContainsPrivateMessages: true, ExportsSecrets: false, LocalOnlyScopes: []string{"apple-messages", "sqlite", "contact-handles", "message-archive", "message-text-search"}}
	m.Commands = map[string]control.Command{
		"status":         {Title: "Status", Argv: []string{"imsgcrawl", "status", "--json"}, JSON: true},
		"sync":           {Title: "Sync", Argv: []string{"imsgcrawl", "sync", "--json"}, JSON: true, Mutates: true},
		"doctor":         {Title: "Doctor", Argv: []string{"imsgcrawl", "doctor", "--json"}, JSON: true},
		"chats":          {Title: "Chats", Argv: []string{"imsgcrawl", "chats", "--json"}, JSON: true},
		"messages":       {Title: "Messages", Argv: []string{"imsgcrawl", "messages", "--json"}, JSON: true},
		"who":            {Title: "Who", Argv: []string{"imsgcrawl", "who", "NAME", "--json"}, JSON: true},
		"search":         {Title: "Search", Argv: []string{"imsgcrawl", "search", "QUERY", "--json"}, JSON: true},
		"open":           {Title: "Open", Argv: []string{"imsgcrawl", "open", "REF", "--json"}, JSON: true},
		"contact-export": {Title: "Export contacts", Argv: []string{"imsgcrawl", "contacts", "export", "--json"}, JSON: true},
	}
	return m
}
