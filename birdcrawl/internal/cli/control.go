package cli

import (
	"path/filepath"

	"github.com/openclaw/crawlkit/control"
)

func controlManifest() control.Manifest {
	m := control.NewManifest("birdcrawl", "X", "birdcrawl")
	m.Description = "Local-first X archive crawler."
	m.Version = version
	m.Branding = control.Branding{SymbolName: "text.bubble", AccentColor: "#111111"}
	m.Paths = control.Paths{
		DefaultConfig:   defaultConfigPath(),
		ConfigEnv:       configEnv,
		DefaultDatabase: defaultDBPath(),
		DefaultLogs:     defaultLogDir(),
		DefaultCache:    filepath.Join(defaultBaseDir(), "cache"),
	}
	m.Capabilities = []string{"metadata", "status", "sync", "search", "open", "doctor", "stats", "archive_import"}
	m.Privacy = control.Privacy{
		ContainsPrivateMessages: true,
		ExportsSecrets:          false,
		LocalOnlyScopes:         []string{"x-archive-dump", "sqlite"},
	}
	m.Commands = map[string]control.Command{
		"doctor":  {Title: "Doctor", Argv: []string{"birdcrawl", "--json", "doctor"}, JSON: true},
		"status":  {Title: "Status", Argv: []string{"birdcrawl", "--json", "status"}, JSON: true},
		"sync":    {Title: "Sync", Argv: []string{"birdcrawl", "--json", "sync"}, JSON: true, Mutates: true},
		"search":  {Title: "Search", Argv: []string{"birdcrawl", "--json", "search", "QUERY"}, JSON: true},
		"open":    {Title: "Open", Argv: []string{"birdcrawl", "--json", "open", "birdcrawl:tweet/ID"}, JSON: true},
		"stats":   {Title: "Stats", Argv: []string{"birdcrawl", "--json", "stats"}, JSON: true},
		"import":  {Title: "Import archive", Argv: []string{"birdcrawl", "--json", "import", "archive", "PATH"}, JSON: true, Mutates: true},
		"version": {Title: "Version", Argv: []string{"birdcrawl", "version"}},
	}
	return m
}
