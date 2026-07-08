package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
)

const crawlerCommandTimeout = crawlkit.DefaultReadTimeout

// Source is one registered crawler as trawl uses it: the addressable id,
// the surface name a person says out loud, the verbs it exposes, and the
// typed crawler value trawl calls in-process.
type Source struct {
	ID           string
	Binary       string
	Surface      string
	Aliases      []string
	DisplayName  string
	Description  string
	Capabilities []string
	LogDir       string
	Commands     map[string]control.Command
	MetadataErr  error
	Crawler      crawlkit.Crawler
}

// discoverCrawlers projects the explicit crawlkit registrations into the
// existing trawl Source shape. A crawler whose generated metadata did not
// parse keeps its id and carries the error so status and doctor can surface it.
func discoverCrawlers(ctx context.Context) []Source {
	_ = ctx
	crawlers := registeredCrawlers()
	sources := make([]Source, 0, len(crawlers))
	for _, crawler := range crawlers {
		info := crawler.Info()
		manifest, err := crawlkitManifest(crawler)
		if err != nil {
			id := firstNonEmpty(info.ID, info.Surface)
			sources = append(sources, Source{
				ID:          id,
				Binary:      id,
				Surface:     info.Surface,
				Aliases:     sourceAliases(info.Aliases, id),
				DisplayName: firstNonEmpty(info.DisplayName, info.Surface, info.ID),
				Crawler:     crawler,
				MetadataErr: err,
			})
			continue
		}
		sources = append(sources, Source{
			ID:           manifest.ID,
			Binary:       firstNonEmpty(info.ID, manifest.Binary.Name),
			Surface:      info.Surface,
			Aliases:      sourceAliases(info.Aliases, manifest.ID),
			DisplayName:  manifest.DisplayName,
			Description:  manifest.Description,
			Capabilities: manifest.Capabilities,
			LogDir:       manifest.Paths.DefaultLogs,
			Commands:     manifest.Commands,
			Crawler:      crawler,
		})
	}
	return sources
}

func sourceAliases(current []string, id string) []string {
	aliases := append([]string(nil), current...)
	for _, alias := range legacyRoutingAliases(strings.TrimSpace(id)) {
		aliases = appendUniqueAlias(aliases, alias)
	}
	return aliases
}

func appendUniqueAlias(aliases []string, alias string) []string {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return aliases
	}
	for _, existing := range aliases {
		if strings.EqualFold(strings.TrimSpace(existing), alias) {
			return aliases
		}
	}
	return append(aliases, alias)
}

func legacyRoutingAliases(id string) []string {
	switch id {
	case "imessage":
		return []string{"imsgcrawl"}
	case "telegram":
		return []string{"telecrawl"}
	case "whatsapp":
		return []string{"wacrawl"}
	case "contacts":
		return []string{"clawdex"}
	case "photos":
		return []string{"photoscrawl"}
	case "gmail":
		return []string{"gogcrawl"}
	case "calendar":
		return []string{"calcrawl"}
	case "twitter":
		return []string{"birdcrawl"}
	default:
		return nil
	}
}

func crawlkitManifest(source crawlkit.Crawler) (control.Manifest, error) {
	manifest, err := crawlkit.Manifest(source)
	if err != nil {
		return control.Manifest{}, err
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return control.Manifest{}, errors.New("metadata id is empty")
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	return manifest, nil
}

// sourcesLine renders the compiled-in crawlers in the words people type.
func sourcesLine(ctx context.Context) string {
	sources := discoverCrawlers(ctx)
	if len(sources) == 0 {
		return "No crawlers are registered yet."
	}
	names := make([]string, 0, len(sources))
	for _, source := range sources {
		names = append(names, sourceHumanName(source))
	}
	return "Sources: " + strings.Join(names, ", ") + ". Run trawl status to see yours."
}

type crawlerCommandError struct {
	command string
	err     error
}

func (e crawlerCommandError) Error() string {
	return fmt.Sprintf("%s failed", e.command)
}

func (e crawlerCommandError) Unwrap() error {
	return e.err
}
