package archive

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestControlManifest(t *testing.T) {
	paths := Paths{
		ConfigPath: filepath.Join("config", "config.toml"),
		Database:   filepath.Join("data", "photos.sqlite"),
		CacheDir:   "cache",
		LogDir:     "logs",
		ShareDir:   "share",
	}
	manifest := ControlManifest(paths)
	if manifest.ID != "photoscrawl" || manifest.Binary.Name != "photoscrawl" {
		t.Fatalf("identity = %#v", manifest)
	}
	if manifest.DisplayName != "Photos" || manifest.Version != Version || manifest.ContractVersion != ContractVersion {
		t.Fatalf("contract identity = %#v", manifest)
	}
	if manifest.Paths.DefaultDatabase != paths.Database || manifest.Paths.DefaultCache != paths.CacheDir {
		t.Fatalf("paths = %#v", manifest.Paths)
	}
	for _, capability := range []string{"status", "sync", "search", "open", "doctor"} {
		if !slices.Contains(manifest.Capabilities, capability) {
			t.Fatalf("missing capability %q in %#v", capability, manifest.Capabilities)
		}
	}
	if slices.Contains(manifest.Capabilities, "classify") || slices.Contains(manifest.Capabilities, "eval-card") {
		t.Fatalf("internal capability advertised in %#v", manifest.Capabilities)
	}
	if manifest.Commands["search"].Mutates || !manifest.Commands["doctor"].JSON {
		t.Fatalf("command contract = %#v", manifest.Commands)
	}
	if manifest.Privacy.ExportsSecrets || !slices.Contains(manifest.Privacy.LocalOnlyScopes, "apple-photos") {
		t.Fatalf("privacy = %#v", manifest.Privacy)
	}
}
