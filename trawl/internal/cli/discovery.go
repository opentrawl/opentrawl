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
			sources = append(sources, Source{
				ID:          firstNonEmpty(info.ID, info.Surface),
				Binary:      info.ID,
				Surface:     info.Surface,
				Aliases:     append([]string(nil), info.Aliases...),
				DisplayName: firstNonEmpty(info.DisplayName, info.Surface, info.ID),
				Crawler:     crawler,
				MetadataErr: err,
			})
			continue
		}
		sources = append(sources, Source{
			ID:           manifest.ID,
			Binary:       info.ID,
			Surface:      info.Surface,
			Aliases:      append([]string(nil), info.Aliases...),
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

func crawlkitManifest(source crawlkit.Crawler) (control.Manifest, error) {
	out, err := runCrawlkitCaptured([]string{"metadata", "--json"}, []crawlkit.Crawler{source})
	if err != nil {
		return control.Manifest{}, err
	}
	if out.Code != 0 {
		return control.Manifest{}, fmt.Errorf("metadata failed")
	}
	var manifest control.Manifest
	if err := decodeContractJSON(out.Stdout, &manifest); err != nil {
		return control.Manifest{}, err
	}
	if strings.TrimSpace(manifest.ID) == "" {
		return control.Manifest{}, errors.New("metadata id is empty")
	}
	manifest.ID = strings.TrimSpace(manifest.ID)
	return manifest, nil
}

// sourcesLine renders the compiled-in crawlers as id/surface-name pairs for
// the root --help intro.
func sourcesLine(ctx context.Context) string {
	sources := discoverCrawlers(ctx)
	if len(sources) == 0 {
		return "No crawlers are registered yet."
	}
	pairs := make([]string, 0, len(sources))
	for _, source := range sources {
		alias := sourceAlias(source.DisplayName)
		if alias != "" && alias != source.ID {
			pairs = append(pairs, source.ID+"/"+alias)
			continue
		}
		pairs = append(pairs, source.ID)
	}
	return "Sources go by id or surface name: " + strings.Join(pairs, ", ") + " — trawl status lists yours."
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
