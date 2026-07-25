package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

const crawlerCommandTimeout = trawlkit.DefaultReadTimeout

// Source is one registered crawler as trawl uses it: the addressable id,
// the surface name a person says out loud, the verbs it exposes, and the
// typed crawler value trawl calls in-process.
type Source struct {
	Manifest     control.Manifest
	ID           string
	Binary       string
	Surface      string
	Aliases      []string
	DisplayName  string
	Headlines    []string
	Capabilities []string
	LogDir       string
	Commands     map[string]control.Command
	MetadataErr  error
	Crawler      trawlkit.Crawler
}

// discoverCrawlers projects the explicit trawlkit registrations into the
// existing trawl Source shape. A crawler whose generated metadata did not
// parse keeps its declared id and carries the error so status can surface it.
func discoverCrawlers(ctx context.Context) []Source {
	_ = ctx
	entries := registeredCrawlerEntries()
	sources := make([]Source, 0, len(entries))
	for _, entry := range entries {
		crawler := entry.crawler
		info := crawler.Info()
		manifest, err := trawlkitManifest(crawler)
		if err == nil {
			err = applySourcePresentation(&manifest, entry.registration)
		}
		if err != nil {
			id := strings.TrimSpace(firstNonEmpty(info.ID, info.Surface))
			manifest := control.NewManifest(id, firstNonEmpty(info.DisplayName, info.Surface, id), "")
			sources = append(sources, Source{
				Manifest:    manifest,
				ID:          manifest.ID,
				Binary:      manifest.Binary.Name,
				Surface:     info.Surface,
				Aliases:     append([]string(nil), manifest.Aliases...),
				DisplayName: manifest.DisplayName,
				Crawler:     crawler,
				MetadataErr: err,
			})
			continue
		}
		manifest = cloneManifest(manifest)
		sources = append(sources, Source{
			Manifest:     manifest,
			ID:           manifest.ID,
			Binary:       manifest.Binary.Name,
			Surface:      info.Surface,
			Aliases:      append([]string(nil), manifest.Aliases...),
			DisplayName:  manifest.DisplayName,
			Headlines:    append([]string(nil), manifest.Headlines...),
			Capabilities: append([]string(nil), manifest.Capabilities...),
			LogDir:       manifest.Paths.DefaultLogs,
			Commands:     cloneCommands(manifest.Commands),
			Crawler:      crawler,
		})
	}
	return sources
}

func applySourcePresentation(manifest *control.Manifest, registration crawlerRegistration) error {
	if manifest == nil {
		return errors.New("manifest is nil")
	}
	if err := validateSourcePresentation(manifest.ID, manifest.DisplayName, registration); err != nil {
		return err
	}
	manifest.Branding = registration.branding
	return nil
}

func trawlkitManifest(source trawlkit.Crawler) (control.Manifest, error) {
	manifest, err := trawlkit.Manifest(source)
	if err != nil {
		return control.Manifest{}, err
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return control.Manifest{}, errors.New("metadata id is empty")
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	return manifest, nil
}
