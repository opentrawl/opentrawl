package cli

import (
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
		DefaultCache:    birdcrawlPaths().CacheDir,
	}
	m.Capabilities = []string{"metadata", "status", "sync", "search", "open", "doctor", "stats", "archive_import", "short_refs"}
	m.Privacy = control.Privacy{
		ContainsPrivateMessages: true,
		ExportsSecrets:          false,
		LocalOnlyScopes:         []string{"x-archive-dump", "sqlite"},
	}
	m.Commands = map[string]control.Command{
		"doctor":    {Title: "Doctor", Argv: []string{"birdcrawl", "doctor", "--json"}, JSON: true},
		"status":    {Title: "Status", Argv: []string{"birdcrawl", "status", "--json"}, JSON: true},
		"sync":      {Title: "Sync", Argv: []string{"birdcrawl", "sync", "--json"}, JSON: true, Mutates: true},
		"tweets":    {Title: "Tweets", Argv: []string{"birdcrawl", "tweets", "--json"}, JSON: true},
		"bookmarks": {Title: "Bookmarks", Argv: []string{"birdcrawl", "bookmarks", "--json"}, JSON: true},
		"likes":     {Title: "Likes", Argv: []string{"birdcrawl", "likes", "--json"}, JSON: true},
		"mentions":  {Title: "Mentions", Argv: []string{"birdcrawl", "mentions", "--json"}, JSON: true},
		"search":    {Title: "Search", Argv: []string{"birdcrawl", "search", "QUERY", "--json"}, JSON: true},
		"open":      {Title: "Open", Argv: []string{"birdcrawl", "open", "REF", "--json"}, JSON: true},
		"stats":     {Title: "Stats", Argv: []string{"birdcrawl", "stats", "--json"}, JSON: true},
		"import":    {Title: "Import archive", Argv: []string{"birdcrawl", "import", "archive", "PATH", "--json"}, JSON: true, Mutates: true},
		"version":   {Title: "Version", Argv: []string{"birdcrawl", "version"}},
	}
	return m
}
