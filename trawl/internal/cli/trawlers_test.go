package cli

import "testing"

func TestRegisteredTrawlerCatalogBuildsWithoutErrors(t *testing.T) {
	snapshot := buildRegisteredTrawlerManifestSnapshot(true)
	if snapshot.registeredTrawlerCatalogConstructionError != nil {
		t.Fatalf("buildRegisteredTrawlerManifestSnapshot returned error: %v", snapshot.registeredTrawlerCatalogConstructionError)
	}
	if len(snapshot.registeredTrawlerCatalogEntries) < len(trawlerFactories) {
		t.Fatalf(
			"catalog entry count = %d, want at least %d",
			len(snapshot.registeredTrawlerCatalogEntries),
			len(trawlerFactories),
		)
	}
	for _, entry := range snapshot.registeredTrawlerCatalogEntries {
		manifest := entry.GetRegisteredTrawlerManifest()
		if manifest.GetTrawlerBranding() == nil {
			t.Fatalf("trawler %q has nil branding", manifest.GetRegisteredTrawler().GetRegisteredTrawlerIdentity())
		}
	}
}
