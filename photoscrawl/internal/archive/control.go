package archive

import "github.com/openclaw/crawlkit/control"

const (
	Version         = "dev"
	ContractVersion = 1
)

type Manifest struct {
	control.Manifest
	Version         string `json:"version"`
	ContractVersion int    `json:"contract_version"`
}

func ControlManifest(paths Paths) Manifest {
	manifest := control.NewManifest("photoscrawl", "Photos", "photoscrawl")
	manifest.Description = "Local-first, read-only Apple Photos archive crawler."
	manifest.Branding = control.Branding{
		SymbolName:       "photo.on.rectangle.angled",
		AccentColor:      "#ff2d55",
		BundleIdentifier: "com.apple.Photos",
	}
	manifest.Paths = control.Paths{
		DefaultConfig:   paths.ConfigPath,
		DefaultDatabase: paths.Database,
		DefaultCache:    paths.CacheDir,
		DefaultLogs:     paths.LogDir,
		DefaultShare:    paths.ShareDir,
	}
	manifest.Capabilities = []string{"status", "sync", "search", "open", "doctor"}
	manifest.Privacy = control.Privacy{
		ExportsSecrets: false,
		LocalOnlyScopes: []string{
			"apple-photos",
			"sqlite",
			"media-metadata",
			"location-observations",
			"local-model-observations",
		},
	}
	manifest.Commands = map[string]control.Command{
		"metadata": {Title: "Metadata", Argv: []string{"photoscrawl", "metadata", "--json"}, JSON: true},
		"status":   {Title: "Status", Argv: []string{"photoscrawl", "status", "--json"}, JSON: true},
		"sync":     {Title: "Sync", Argv: []string{"photoscrawl", "sync", "--library", "<path>", "--json"}, JSON: true, Mutates: true},
		"search":   {Title: "Search", Argv: []string{"photoscrawl", "search", "<query>", "--json"}, JSON: true},
		"open":     {Title: "Open", Argv: []string{"photoscrawl", "open", "<ref>", "--json"}, JSON: true},
		"doctor":   {Title: "Doctor", Argv: []string{"photoscrawl", "doctor", "--json"}, JSON: true},
	}
	return Manifest{
		Manifest:        manifest,
		Version:         Version,
		ContractVersion: ContractVersion,
	}
}
