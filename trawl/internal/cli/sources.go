package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/calendar"
	"github.com/opentrawl/opentrawl/gmail"
	contacts "github.com/opentrawl/opentrawl/trawlers/contacts"
	imessage "github.com/opentrawl/opentrawl/trawlers/imessage"
	notes "github.com/opentrawl/opentrawl/trawlers/notes"
	photos "github.com/opentrawl/opentrawl/trawlers/photos"
	telegram "github.com/opentrawl/opentrawl/trawlers/telegram"
	whatsapp "github.com/opentrawl/opentrawl/trawlers/whatsapp"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"github.com/opentrawl/opentrawl/twitter"
)

const allSourcesEnvironmentKey = "OPENTRAWL_ALL_SOURCES"

type crawlerRegistration struct {
	factory  func() trawlkit.Crawler
	beta     bool
	branding control.Branding
}

type registeredCrawler struct {
	crawler      trawlkit.Crawler
	registration crawlerRegistration
}

// crawlerFactories is the single source eligibility and ordering authority.
// Every human command and private app operation consumes registeredCrawlers,
// so beta visibility cannot drift between help, status, search, sync,
// namespaces, crawler wire, and AppWire. The explicit environment override is
// for local development of sources outside the beta promise.
var crawlerFactories = []crawlerRegistration{
	{factory: func() trawlkit.Crawler { return imessage.New() }, beta: true, branding: macAppBranding("message.fill", "#34C759", "com.apple.MobileSMS", "com.apple.MobileSMS")},
	{factory: func() trawlkit.Crawler { return whatsapp.New() }, beta: true, branding: macAppBranding("phone.bubble.fill", "#25D366", "net.whatsapp.WhatsApp", "net.whatsapp.WhatsApp")},
	{factory: func() trawlkit.Crawler { return telegram.New() }, beta: true, branding: macAppBranding("paperplane.fill", "#229ED9", "ru.keepcoder.Telegram", "ru.keepcoder.Telegram")},
	{factory: func() trawlkit.Crawler { return notes.New() }, beta: true, branding: macAppBranding("note.text", "#FFD60A", "com.apple.Notes", "com.apple.mobilenotes")},
	{factory: func() trawlkit.Crawler { return contacts.New() }, beta: true, branding: macAppBranding("person.crop.circle.fill", "#8E8E93", "com.apple.AddressBook", "com.apple.MobileAddressBook")},
	{factory: func() trawlkit.Crawler { return gmail.New() }, branding: appStoreBranding("envelope.fill", "#EA4335", "com.google.Gmail")},
	{factory: func() trawlkit.Crawler { return calendar.New() }, beta: true, branding: macAppBranding("calendar", "#FF3B30", "com.apple.iCal", "com.apple.mobilecal")},
	{factory: func() trawlkit.Crawler { return photos.New() }, branding: macAppBranding("photo.on.rectangle.angled", "#007AFF", "com.apple.Photos", "com.apple.mobileslideshow")},
	{factory: func() trawlkit.Crawler { return twitter.New() }, branding: appStoreBranding("bubble.left.and.bubble.right.fill", "#111111", "com.atebits.Tweetie2")},
}

func macAppBranding(symbolName, accentColor, bundleIdentifier, artworkBundleIdentifier string) control.Branding {
	return control.Branding{
		SymbolName: symbolName, AccentColor: accentColor,
		BundleIdentifier: bundleIdentifier, ArtworkBundleIdentifier: artworkBundleIdentifier,
	}
}

func appStoreBranding(symbolName, accentColor, artworkBundleIdentifier string) control.Branding {
	return control.Branding{SymbolName: symbolName, AccentColor: accentColor, ArtworkBundleIdentifier: artworkBundleIdentifier}
}

func registeredCrawlerEntries() []registeredCrawler {
	sources := make([]registeredCrawler, 0, len(crawlerFactories))
	allSources := strings.TrimSpace(os.Getenv(allSourcesEnvironmentKey)) == "1"
	for _, registration := range crawlerFactories {
		if !registration.beta && !allSources {
			continue
		}
		sources = append(sources, registeredCrawler{
			crawler: registration.factory(), registration: registration,
		})
	}
	return sources
}

func registeredCrawlers() []trawlkit.Crawler {
	entries := registeredCrawlerEntries()
	sources := make([]trawlkit.Crawler, 0, len(entries))
	for _, entry := range entries {
		sources = append(sources, entry.crawler)
	}
	return sources
}

func sourceCatalogEntries() ([]*federationv1.SourceCatalogEntry, error) {
	allSources := strings.TrimSpace(os.Getenv(allSourcesEnvironmentKey)) == "1"
	entries := make([]*federationv1.SourceCatalogEntry, 0, len(crawlerFactories))
	seen := make(map[string]struct{}, len(crawlerFactories))
	for index, registration := range crawlerFactories {
		crawler := registration.factory()
		info := crawler.Info()
		id := strings.TrimSpace(info.ID)
		displayName := strings.TrimSpace(info.DisplayName)
		if err := validateSourcePresentation(id, displayName, registration); err != nil {
			return nil, fmt.Errorf("source catalogue entry %d: %w", index, err)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("source catalogue entry %d: duplicate source id %q", index, id)
		}
		seen[id] = struct{}{}
		releaseState := federationv1.SourceReleaseState_SOURCE_RELEASE_STATE_COMING_SOON
		if registration.beta {
			releaseState = federationv1.SourceReleaseState_SOURCE_RELEASE_STATE_AVAILABLE
		}
		entries = append(entries, &federationv1.SourceCatalogEntry{
			Manifest: &federationv1.SourceManifest{
				SourceId: id, DisplayName: displayName,
				Branding:  sourceBranding(registration.branding),
				Headlines: append([]string(nil), info.Headlines...),
			},
			ReleaseState: releaseState,
			Enabled:      registration.beta || allSources,
		})
	}
	return entries, nil
}

func validateSourcePresentation(id, displayName string, registration crawlerRegistration) error {
	if id == "" {
		return fmt.Errorf("source id is empty")
	}
	if displayName == "" {
		return fmt.Errorf("source %q display name is empty", id)
	}
	branding := registration.branding
	if strings.TrimSpace(branding.SymbolName) == "" {
		return fmt.Errorf("source %q symbol name is empty", id)
	}
	if !validHexColour(branding.AccentColor) {
		return fmt.Errorf("source %q accent colour %q is not #RRGGBB", id, branding.AccentColor)
	}
	if registration.beta && strings.TrimSpace(branding.BundleIdentifier) == "" {
		return fmt.Errorf("available source %q bundle identifier is empty", id)
	}
	if strings.TrimSpace(branding.ArtworkBundleIdentifier) == "" {
		return fmt.Errorf("source %q artwork bundle identifier is empty", id)
	}
	return nil
}

func validHexColour(value string) bool {
	if len(value) != 7 || value[0] != '#' {
		return false
	}
	for _, char := range value[1:] {
		if (char < '0' || char > '9') && (char < 'A' || char > 'F') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func sourceBranding(value control.Branding) *federationv1.Branding {
	return &federationv1.Branding{
		SymbolName: value.SymbolName, AccentColor: value.AccentColor,
		IconPath: value.IconPath, BundleIdentifier: value.BundleIdentifier,
		ArtworkBundleIdentifier: value.ArtworkBundleIdentifier,
	}
}

func ExecuteCrawlerWire(args []string) int {
	return trawlkit.Run(args, registeredCrawlers())
}
