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
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	"github.com/opentrawl/opentrawl/twitter"
	"google.golang.org/protobuf/proto"
)

const allTrawlersEnvironmentKey = "OPENTRAWL_ALL_TRAWLERS"

type trawlerRegistration struct {
	factory  func() trawlkit.Trawler
	beta     bool
	branding *federationv1.TrawlerBranding
}

type registeredTrawler struct {
	trawler      trawlkit.Trawler
	registration trawlerRegistration
}

// trawlerFactories is the single trawler eligibility and ordering authority.
// Every human command and private app operation uses registeredTrawlers, so
// beta visibility stays the same in help, operations, namespaces and AppWire.
// The environment override enables local development beyond the beta set.
var trawlerFactories = []trawlerRegistration{
	{factory: func() trawlkit.Trawler { return imessage.New() }, beta: true, branding: macAppBranding("message.fill", "#34C759", "com.apple.MobileSMS", "com.apple.MobileSMS")},
	{factory: func() trawlkit.Trawler { return whatsapp.New() }, beta: true, branding: macAppBranding("phone.bubble.fill", "#25D366", "net.whatsapp.WhatsApp", "net.whatsapp.WhatsApp")},
	{factory: func() trawlkit.Trawler { return telegram.New() }, beta: true, branding: macAppBranding("paperplane.fill", "#229ED9", "ru.keepcoder.Telegram", "ru.keepcoder.Telegram")},
	{factory: func() trawlkit.Trawler { return notes.New() }, beta: true, branding: macAppBranding("note.text", "#FFD60A", "com.apple.Notes", "com.apple.mobilenotes")},
	{factory: func() trawlkit.Trawler { return contacts.New() }, beta: true, branding: macAppBranding("person.crop.circle.fill", "#8E8E93", "com.apple.AddressBook", "com.apple.MobileAddressBook")},
	{factory: func() trawlkit.Trawler { return gmail.New() }, branding: appStoreBranding("envelope.fill", "#EA4335", "com.google.Gmail")},
	{factory: func() trawlkit.Trawler { return calendar.New() }, beta: true, branding: macAppBranding("calendar", "#FF3B30", "com.apple.iCal", "com.apple.mobilecal")},
	{factory: func() trawlkit.Trawler { return photos.New() }, branding: macAppBranding("photo.on.rectangle.angled", "#007AFF", "com.apple.Photos", "com.apple.mobileslideshow")},
	{factory: func() trawlkit.Trawler { return twitter.New() }, branding: appStoreBranding("bubble.left.and.bubble.right.fill", "#111111", "com.atebits.Tweetie2")},
}

func macAppBranding(symbolName, accentColor, bundleIdentifier, artworkBundleIdentifier string) *federationv1.TrawlerBranding {
	return &federationv1.TrawlerBranding{
		SymbolName: symbolName, AccentColor: accentColor,
		BundleIdentifier: bundleIdentifier, ArtworkBundleIdentifier: artworkBundleIdentifier,
	}
}

func appStoreBranding(symbolName, accentColor, artworkBundleIdentifier string) *federationv1.TrawlerBranding {
	return &federationv1.TrawlerBranding{SymbolName: symbolName, AccentColor: accentColor, ArtworkBundleIdentifier: artworkBundleIdentifier}
}

func registeredTrawlerEntries() []registeredTrawler {
	registeredTrawlers := make([]registeredTrawler, 0, len(trawlerFactories))
	allTrawlers := strings.TrimSpace(os.Getenv(allTrawlersEnvironmentKey)) == "1"
	for _, registration := range trawlerFactories {
		if !registration.beta && !allTrawlers {
			continue
		}
		registeredTrawlers = append(registeredTrawlers, registeredTrawler{
			trawler: registration.factory(), registration: registration,
		})
	}
	return registeredTrawlers
}

func registeredTrawlers() []trawlkit.Trawler {
	entries := registeredTrawlerEntries()
	trawlers := make([]trawlkit.Trawler, 0, len(entries))
	for _, entry := range entries {
		trawlers = append(trawlers, entry.trawler)
	}
	return trawlers
}

func registeredTrawlerCatalogEntries() ([]*federationv1.RegisteredTrawlerCatalogEntry, error) {
	allTrawlers := strings.TrimSpace(os.Getenv(allTrawlersEnvironmentKey)) == "1"
	entries := make([]*federationv1.RegisteredTrawlerCatalogEntry, 0, len(trawlerFactories))
	seen := make(map[string]struct{}, len(trawlerFactories))
	for index, registration := range trawlerFactories {
		trawler := registration.factory()
		manifest, err := trawlkit.Manifest(trawler)
		if err != nil {
			return nil, fmt.Errorf("registered trawler catalogue entry %d: %w", index, err)
		}
		id := strings.TrimSpace(manifest.GetRegisteredTrawlerManifestIdentity())
		displayName := strings.TrimSpace(manifest.GetRegisteredTrawlerDisplayName())
		if err := validateTrawlerPresentation(id, displayName, registration); err != nil {
			return nil, fmt.Errorf("registered trawler catalogue entry %d: %w", index, err)
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("registered trawler catalogue entry %d: duplicate manifest identity %q", index, id)
		}
		seen[id] = struct{}{}
		manifest = proto.Clone(manifest).(*federationv1.RegisteredTrawlerManifest)
		manifest.TrawlerBranding = cloneTrawlerBranding(registration.branding)
		releaseState := federationv1.RegisteredTrawlerReleaseState_REGISTERED_TRAWLER_RELEASE_STATE_COMING_SOON
		if registration.beta {
			releaseState = federationv1.RegisteredTrawlerReleaseState_REGISTERED_TRAWLER_RELEASE_STATE_AVAILABLE
		}
		entries = append(entries, &federationv1.RegisteredTrawlerCatalogEntry{
			RegisteredTrawlerManifest:     manifest,
			RegisteredTrawlerReleaseState: releaseState,
			RegisteredTrawlerIsEnabled:    registration.beta || allTrawlers,
		})
	}
	return entries, nil
}

func validateTrawlerPresentation(id, displayName string, registration trawlerRegistration) error {
	if id == "" {
		return fmt.Errorf("registered trawler manifest identity is empty")
	}
	if displayName == "" {
		return fmt.Errorf("registered trawler %q display name is empty", id)
	}
	branding := registration.branding
	if strings.TrimSpace(branding.SymbolName) == "" {
		return fmt.Errorf("registered trawler %q symbol name is empty", id)
	}
	if !validHexColour(branding.AccentColor) {
		return fmt.Errorf("registered trawler %q accent colour %q is not #RRGGBB", id, branding.AccentColor)
	}
	if registration.beta && strings.TrimSpace(branding.BundleIdentifier) == "" {
		return fmt.Errorf("available trawler %q bundle identifier is empty", id)
	}
	if strings.TrimSpace(branding.ArtworkBundleIdentifier) == "" {
		return fmt.Errorf("registered trawler %q artwork bundle identifier is empty", id)
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

func cloneTrawlerBranding(branding *federationv1.TrawlerBranding) *federationv1.TrawlerBranding {
	if branding == nil {
		return nil
	}
	return proto.Clone(branding).(*federationv1.TrawlerBranding)
}

func ExecuteTrawlerWire(args []string) int {
	return trawlkit.ExecuteTrawlerWireChild(args[1:], registeredTrawlers())
}
