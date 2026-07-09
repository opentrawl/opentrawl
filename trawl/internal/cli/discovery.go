package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
)

const crawlerCommandTimeout = trawlkit.DefaultReadTimeout

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
	Crawler      trawlkit.Crawler
}

// discoverCrawlers projects the explicit trawlkit registrations into the
// existing trawl Source shape. A crawler whose generated metadata did not
// parse keeps its canonical id and carries the error so status and doctor
// can surface it; the id and display name still route through
// canonicalizeSourceID so a crawler that self-reports a pre-rename binary
// name (imsgcrawl, telecrawl, ...) never leaks it into human-facing output.
func discoverCrawlers(ctx context.Context) []Source {
	_ = ctx
	crawlers := registeredCrawlers()
	sources := make([]Source, 0, len(crawlers))
	for _, crawler := range crawlers {
		info := crawler.Info()
		manifest, err := trawlkitManifest(crawler)
		if err != nil {
			id := canonicalizeSourceID(firstNonEmpty(info.ID, info.Surface))
			sources = append(sources, Source{
				ID:          id,
				Binary:      id,
				Surface:     info.Surface,
				Aliases:     trimAliases(info.Aliases),
				DisplayName: firstNonEmpty(info.DisplayName, info.Surface, id),
				Crawler:     crawler,
				MetadataErr: err,
			})
			continue
		}
		sources = append(sources, Source{
			ID:           manifest.ID,
			Binary:       firstNonEmpty(info.ID, manifest.Binary.Name),
			Surface:      info.Surface,
			Aliases:      trimAliases(info.Aliases),
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

// trimAliases keeps only the human aliases a crawler declares (birdcrawl's
// "twitter"): the words a person types and the block renders in parentheses.
// Legacy binary names route through legacyRoutingAliases and never display.
func trimAliases(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if trimmed := strings.TrimSpace(alias); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// legacyRoutingAliases keeps the pre-rename binary names dispatchable
// (`trawl imsgcrawl`, `trawl clawdex`) without displaying them anywhere.
// findSource consults it; the front door and blocks never see it.
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

// canonicalSourceIDs lists every id legacyRoutingAliases knows a pre-rename
// binary name for. canonicalizeSourceID walks this list, so the legacy set
// stays declared in exactly one place.
var canonicalSourceIDs = []string{
	"imessage", "telegram", "whatsapp", "contacts", "photos", "gmail", "calendar", "twitter",
}

// canonicalizeSourceID translates a pre-rename binary name (imsgcrawl,
// telecrawl, wacrawl, clawdex, photoscrawl, gogcrawl, calcrawl, birdcrawl)
// to its canonical source id. Anything else, including an id a crawler
// already reports correctly, passes through unchanged. discoverCrawlers
// calls this so a crawler whose metadata failed to parse and fell back to
// self-reporting a legacy name still surfaces the canonical id — the same
// invariant legacyRoutingAliases already gives command routing.
func canonicalizeSourceID(raw string) string {
	want := strings.ToLower(strings.TrimSpace(raw))
	for _, id := range canonicalSourceIDs {
		if matchesAlias(legacyRoutingAliases(id), want) {
			return id
		}
	}
	return raw
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
