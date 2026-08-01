package cli

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

const registeredTrawlerCommandTimeout = trawlkit.DefaultReadTimeout

// InstalledTrawler is one registered trawler as trawl uses it.
type InstalledTrawler struct {
	RegisteredTrawlerManifest *federation.RegisteredTrawlerManifest
	TrawlerDiscoveryError     error
	Trawler                   trawlkit.Trawler
}

// discoverInstalledTrawlers projects the explicit registrations into the
// installed trawler model.
func discoverInstalledTrawlers(ctx context.Context) []InstalledTrawler {
	_ = ctx
	return buildRegisteredTrawlerManifestSnapshot(false).installedTrawlers
}

func installedTrawlerIdentityText(trawler InstalledTrawler) string {
	return trawlkit.RegisteredTrawlerIdentityText(
		trawler.RegisteredTrawlerManifest.GetRegisteredTrawler(),
	)
}
