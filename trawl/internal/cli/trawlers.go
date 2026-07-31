package cli

import (
	"errors"
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
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/twitter"
	"google.golang.org/protobuf/proto"
)

const allTrawlersEnvironmentKey = "OPENTRAWL_ALL_TRAWLERS"

type trawlerRegistration struct {
	factory  func() trawlkit.Trawler
	beta     bool
	branding *federation.TrawlerBranding
}

type registeredTrawlerManifestEntry struct {
	trawler                                    trawlkit.Trawler
	registeredTrawlerManifest                  *federation.RegisteredTrawlerManifest
	registeredTrawlerManifestConstructionError error
	registeredTrawlerIsEnabled                 bool
	registeredTrawlerReleaseState              federation.RegisteredTrawlerReleaseState
}

type registeredTrawlerManifestSnapshot struct {
	installedTrawlers                         []InstalledTrawler
	registeredTrawlerCatalogEntries           []*federation.RegisteredTrawlerCatalogEntry
	registeredTrawlerCatalogConstructionError error
}

// trawlerFactories is the single trawler registration and ordering authority.
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

func macAppBranding(symbolName, accentColor, bundleIdentifier, artworkBundleIdentifier string) *federation.TrawlerBranding {
	return &federation.TrawlerBranding{
		SymbolName: symbolName, AccentColor: accentColor,
		BundleIdentifier: bundleIdentifier, ArtworkBundleIdentifier: artworkBundleIdentifier,
	}
}

func appStoreBranding(symbolName, accentColor, artworkBundleIdentifier string) *federation.TrawlerBranding {
	return &federation.TrawlerBranding{SymbolName: symbolName, AccentColor: accentColor, ArtworkBundleIdentifier: artworkBundleIdentifier}
}

func buildRegisteredTrawlerManifestSnapshot(
	includeDisabledTrawlersInRegisteredTrawlerCatalog bool,
) registeredTrawlerManifestSnapshot {
	snapshot := registeredTrawlerManifestSnapshot{
		installedTrawlers:               make([]InstalledTrawler, 0, len(trawlerFactories)),
		registeredTrawlerCatalogEntries: make([]*federation.RegisteredTrawlerCatalogEntry, 0, len(trawlerFactories)),
	}
	allTrawlers := strings.TrimSpace(os.Getenv(allTrawlersEnvironmentKey)) == "1"
	seenRegisteredTrawlerIdentities := make(map[string]struct{}, len(trawlerFactories))
	for registrationIndex, registration := range trawlerFactories {
		registeredTrawlerIsEnabled := registration.beta || allTrawlers
		if !registeredTrawlerIsEnabled && !includeDisabledTrawlersInRegisteredTrawlerCatalog {
			continue
		}
		registeredTrawlerManifestEntry := buildRegisteredTrawlerManifestEntry(registration)
		registeredTrawlerManifestEntry.registeredTrawlerIsEnabled = registeredTrawlerIsEnabled
		registeredTrawlerManifestEntry.registeredTrawlerReleaseState = federation.RegisteredTrawlerReleaseState_REGISTERED_TRAWLER_RELEASE_STATE_COMING_SOON
		if registration.beta {
			registeredTrawlerManifestEntry.registeredTrawlerReleaseState = federation.RegisteredTrawlerReleaseState_REGISTERED_TRAWLER_RELEASE_STATE_AVAILABLE
		}
		if registeredTrawlerManifestEntry.registeredTrawlerManifestConstructionError == nil {
			registeredTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(
				registeredTrawlerManifestEntry.registeredTrawlerManifest.GetRegisteredTrawler(),
			)
			if _, identityAlreadyRegistered := seenRegisteredTrawlerIdentities[registeredTrawlerIdentity]; identityAlreadyRegistered {
				registeredTrawlerManifestEntry.registeredTrawlerManifestConstructionError = fmt.Errorf(
					"duplicate manifest identity %q",
					registeredTrawlerIdentity,
				)
			} else {
				seenRegisteredTrawlerIdentities[registeredTrawlerIdentity] = struct{}{}
			}
		}
		if registeredTrawlerManifestEntry.registeredTrawlerIsEnabled {
			snapshot.installedTrawlers = append(snapshot.installedTrawlers, InstalledTrawler{
				RegisteredTrawlerManifest: registeredTrawlerManifestEntry.registeredTrawlerManifest,
				TrawlerDiscoveryError:     registeredTrawlerManifestEntry.registeredTrawlerManifestConstructionError,
				Trawler:                   registeredTrawlerManifestEntry.trawler,
			})
		}
		if registeredTrawlerManifestEntry.registeredTrawlerManifestConstructionError != nil {
			if snapshot.registeredTrawlerCatalogConstructionError == nil {
				snapshot.registeredTrawlerCatalogConstructionError = fmt.Errorf(
					"registered trawler catalogue entry %d: %w",
					registrationIndex,
					registeredTrawlerManifestEntry.registeredTrawlerManifestConstructionError,
				)
			}
			continue
		}
		snapshot.registeredTrawlerCatalogEntries = append(snapshot.registeredTrawlerCatalogEntries, &federation.RegisteredTrawlerCatalogEntry{
			RegisteredTrawlerManifest:     registeredTrawlerManifestEntry.registeredTrawlerManifest,
			RegisteredTrawlerReleaseState: registeredTrawlerManifestEntry.registeredTrawlerReleaseState,
			RegisteredTrawlerIsEnabled:    registeredTrawlerManifestEntry.registeredTrawlerIsEnabled,
		})
	}
	return snapshot
}

func buildRegisteredTrawlerManifestEntry(registration trawlerRegistration) registeredTrawlerManifestEntry {
	trawler := registration.factory()
	manifest, err := trawlkit.Manifest(trawler)
	if err == nil && manifest == nil {
		err = errors.New("manifest is nil")
	}
	if err == nil {
		registeredTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(manifest.GetRegisteredTrawler())
		if registeredTrawlerIdentity == "" {
			err = errors.New("registered trawler identity is empty")
		} else {
			manifest.RegisteredTrawler.RegisteredTrawlerIdentity = registeredTrawlerIdentity
			err = validateTrawlerPresentation(
				registeredTrawlerIdentity,
				manifest.GetRegisteredTrawlerDisplayName(),
				registration,
			)
		}
	}
	if err == nil {
		manifest.TrawlerBranding = cloneTrawlerBranding(registration.branding)
	}
	return registeredTrawlerManifestEntry{
		trawler:                   trawler,
		registeredTrawlerManifest: manifest,
		registeredTrawlerManifestConstructionError: err,
	}
}

func registeredTrawlers() []trawlkit.Trawler {
	snapshot := buildRegisteredTrawlerManifestSnapshot(false)
	trawlers := make([]trawlkit.Trawler, 0, len(snapshot.installedTrawlers))
	for _, installedTrawler := range snapshot.installedTrawlers {
		if installedTrawler.TrawlerDiscoveryError == nil {
			trawlers = append(trawlers, installedTrawler.Trawler)
		}
	}
	return trawlers
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

func cloneTrawlerBranding(branding *federation.TrawlerBranding) *federation.TrawlerBranding {
	if branding == nil {
		return nil
	}
	return proto.Clone(branding).(*federation.TrawlerBranding)
}

func ExecuteTrawlerWire(args []string) int {
	return trawlkit.ExecuteTrawlerWireChild(args[1:], registeredTrawlers())
}
