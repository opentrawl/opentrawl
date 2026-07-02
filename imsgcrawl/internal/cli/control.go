package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func controlManifest() control.Manifest {
	m := control.NewManifest("imsgcrawl", "iMessage Crawl", "imsgcrawl")
	m.Description = "Local-first iMessage archive crawler."
	m.Branding = control.Branding{SymbolName: "message.fill", AccentColor: "#34c759", BundleIdentifier: "com.apple.MobileSMS"}
	m.Paths = control.Paths{
		DefaultDatabase: archive.DefaultPath(),
		DefaultCache:    filepath.Join(defaultBaseDir(), "cache"),
		DefaultLogs:     filepath.Join(defaultBaseDir(), "logs"),
	}
	m.Capabilities = []string{"metadata", "status", "sync", "chats", "messages", "search", "contact-export"}
	m.Privacy = control.Privacy{ContainsPrivateMessages: true, ExportsSecrets: false, LocalOnlyScopes: []string{"apple-messages", "sqlite", "contact-handles", "message-archive", "message-text-search"}}
	m.Commands = map[string]control.Command{
		"status":         {Title: "Status", Argv: []string{"imsgcrawl", "--json", "status"}, JSON: true},
		"sync":           {Title: "Sync", Argv: []string{"imsgcrawl", "--json", "sync"}, JSON: true, Mutates: true},
		"chats":          {Title: "Chats", Argv: []string{"imsgcrawl", "--json", "chats"}, JSON: true},
		"messages":       {Title: "Messages", Argv: []string{"imsgcrawl", "--json", "messages"}, JSON: true},
		"search":         {Title: "Search", Argv: []string{"imsgcrawl", "--json", "search"}, JSON: true},
		"contact-export": {Title: "Export contacts", Argv: []string{"imsgcrawl", "--json", "contacts", "export"}, JSON: true},
	}
	return m
}
