package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/imsgcrawl/internal/messages"
)

func controlManifest() control.Manifest {
	m := control.NewManifest("imsgcrawl", "iMessage Crawl", "imsgcrawl")
	m.Description = "Local-first iMessage archive crawler."
	m.Branding = control.Branding{SymbolName: "message.fill", AccentColor: "#34c759", BundleIdentifier: "com.apple.MobileSMS"}
	m.Paths = control.Paths{
		DefaultDatabase: messages.DefaultChatDBPath(),
		DefaultCache:    filepath.Join(defaultBaseDir(), "cache"),
		DefaultLogs:     filepath.Join(defaultBaseDir(), "logs"),
	}
	m.Capabilities = []string{"metadata", "status", "contact-export"}
	m.Privacy = control.Privacy{ContainsPrivateMessages: true, ExportsSecrets: false, LocalOnlyScopes: []string{"apple-messages", "sqlite", "contact-handles"}}
	m.Commands = map[string]control.Command{
		"status":         {Title: "Status", Argv: []string{"imsgcrawl", "--json", "status"}, JSON: true},
		"contact-export": {Title: "Export contacts", Argv: []string{"imsgcrawl", "--json", "contacts", "export"}, JSON: true},
	}
	return m
}
