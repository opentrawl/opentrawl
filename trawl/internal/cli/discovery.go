package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"google.golang.org/protobuf/proto"
)

const crawlerCommandTimeout = trawlkit.DefaultReadTimeout

// InstalledTrawler is one registered trawler as trawl uses it.
type InstalledTrawler struct {
	RegisteredTrawlerManifest                   *federationv1.RegisteredTrawlerManifest
	RegisteredTrawlerManifestIdentity           string
	RegisteredTrawlerCommandName                string
	RegisteredTrawlerDisplayName                string
	RegisteredTrawlerAliases                    []string
	TrawlerCommandNamesShownInBareTrawlOverview []string
	TrawlerDiscoveryError                       error
	Trawler                                     trawlkit.Trawler
}

// discoverInstalledTrawlers projects the explicit registrations into the
// installed trawler model.
func discoverInstalledTrawlers(ctx context.Context) []InstalledTrawler {
	_ = ctx
	entries := registeredTrawlerEntries()
	installedTrawlers := make([]InstalledTrawler, 0, len(entries))
	for _, entry := range entries {
		trawler := entry.trawler
		declaration := trawler.RegisteredTrawlerDeclaration()
		manifest, err := trawlkitManifest(trawler)
		if err == nil {
			err = applyTrawlerPresentation(manifest, entry.registration)
		}
		if err != nil {
			id := strings.TrimSpace(firstNonEmpty(declaration.RegisteredTrawlerManifestIdentity, declaration.RegisteredTrawlerCommandName))
			manifest := &federationv1.RegisteredTrawlerManifest{
				RegisteredTrawlerManifestIdentity:           id,
				RegisteredTrawlerCommandName:                strings.TrimSpace(declaration.RegisteredTrawlerCommandName),
				RegisteredTrawlerDisplayName:                firstNonEmpty(declaration.RegisteredTrawlerDisplayName, declaration.RegisteredTrawlerCommandName, id),
				RegisteredTrawlerAliases:                    append([]string(nil), declaration.RegisteredTrawlerAliases...),
				TrawlerCommandNamesShownInBareTrawlOverview: append([]string(nil), declaration.TrawlerCommandNamesShownInBareTrawlOverview...),
				TrawlerBranding:                             cloneTrawlerBranding(entry.registration.branding),
			}
			installedTrawlers = append(installedTrawlers, InstalledTrawler{
				RegisteredTrawlerManifest:         manifest,
				RegisteredTrawlerManifestIdentity: manifest.GetRegisteredTrawlerManifestIdentity(),
				RegisteredTrawlerCommandName:      declaration.RegisteredTrawlerCommandName,
				RegisteredTrawlerAliases:          append([]string(nil), manifest.GetRegisteredTrawlerAliases()...),
				RegisteredTrawlerDisplayName:      manifest.GetRegisteredTrawlerDisplayName(),
				Trawler:                           trawler,
				TrawlerDiscoveryError:             err,
			})
			continue
		}
		manifest = proto.Clone(manifest).(*federationv1.RegisteredTrawlerManifest)
		installedTrawlers = append(installedTrawlers, InstalledTrawler{
			RegisteredTrawlerManifest:                   manifest,
			RegisteredTrawlerManifestIdentity:           manifest.GetRegisteredTrawlerManifestIdentity(),
			RegisteredTrawlerCommandName:                manifest.GetRegisteredTrawlerCommandName(),
			RegisteredTrawlerAliases:                    append([]string(nil), manifest.GetRegisteredTrawlerAliases()...),
			RegisteredTrawlerDisplayName:                manifest.GetRegisteredTrawlerDisplayName(),
			TrawlerCommandNamesShownInBareTrawlOverview: append([]string(nil), manifest.GetTrawlerCommandNamesShownInBareTrawlOverview()...),
			Trawler: trawler,
		})
	}
	return installedTrawlers
}

func applyTrawlerPresentation(manifest *federationv1.RegisteredTrawlerManifest, registration trawlerRegistration) error {
	if manifest == nil {
		return errors.New("manifest is nil")
	}
	if err := validateTrawlerPresentation(manifest.GetRegisteredTrawlerManifestIdentity(), manifest.GetRegisteredTrawlerDisplayName(), registration); err != nil {
		return err
	}
	manifest.TrawlerBranding = cloneTrawlerBranding(registration.branding)
	return nil
}

func trawlkitManifest(trawler trawlkit.Trawler) (*federationv1.RegisteredTrawlerManifest, error) {
	manifest, err := trawlkit.Manifest(trawler)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(manifest.GetRegisteredTrawlerManifestIdentity()) == "" {
		return nil, errors.New("registered trawler manifest identity is empty")
	}
	manifest.RegisteredTrawlerManifestIdentity = strings.TrimSpace(manifest.GetRegisteredTrawlerManifestIdentity())
	return manifest, nil
}
