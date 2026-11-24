package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/calendar"
	"github.com/opentrawl/opentrawl/gmail"
	contacts "github.com/opentrawl/opentrawl/trawlers/contacts"
	imessage "github.com/opentrawl/opentrawl/trawlers/imessage"
	mail "github.com/opentrawl/opentrawl/trawlers/mail"
	money "github.com/opentrawl/opentrawl/trawlers/money"
	notes "github.com/opentrawl/opentrawl/trawlers/notes"
	photos "github.com/opentrawl/opentrawl/trawlers/photos"
	telegram "github.com/opentrawl/opentrawl/trawlers/telegram"
	whatsapp "github.com/opentrawl/opentrawl/trawlers/whatsapp"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/pluginruntime"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	twitter "github.com/opentrawl/opentrawl/twitter"
	"google.golang.org/protobuf/proto"
)

const allTrawlersEnvironmentKey = "OPENTRAWL_ALL_TRAWLERS"

type trawlerRegistration struct {
	factory func() trawlkit.Trawler
	beta    bool
	// Temporary fallback only for trawlers whose declarations have not yet moved
	// branding and freshness metadata into RegisteredTrawlerDeclaration.
	fallbackBranding                  *federation.TrawlerBranding
	fallbackSourceFreshnessDependency federation.SourceFreshnessDependency
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
	{factory: func() trawlkit.Trawler { return imessage.New() }, beta: true},
	{factory: func() trawlkit.Trawler { return contacts.New() }, beta: true},
	{factory: func() trawlkit.Trawler { return mail.New() }, beta: true},
	{factory: func() trawlkit.Trawler { return gmail.New() }, beta: true},
	{factory: func() trawlkit.Trawler { return calendar.New() }, beta: true},
	{factory: func() trawlkit.Trawler { return photos.New() }, beta: true, fallbackBranding: macAppBranding("photo.on.rectangle.angled", "#007AFF", "com.apple.Photos", "com.apple.mobileslideshow"), fallbackSourceFreshnessDependency: federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_NONE},
}

// bundledMultiCallPlugins lists first-party plugins compiled into the single
// multi-call trawl binary. Each plugin still runs out-of-process inside the
// exact same Seatbelt sandbox, capability policy, secret broker, and loopback
// HTTP allowlist proxy as standalone external plugins.
var bundledMultiCallPlugins = []pluginruntime.BundledMultiCallPluginRegistration{
	{PluginIdentifier: "money", Factory: func() trawlkit.Trawler { return money.New() }},
	{PluginIdentifier: "notes", Factory: func() trawlkit.Trawler { return notes.New() }},
	{PluginIdentifier: "telegram", Factory: func() trawlkit.Trawler { return telegram.New() }},
	{PluginIdentifier: "twitter", Factory: func() trawlkit.Trawler { return twitter.New() }},
	{PluginIdentifier: "whatsapp", Factory: func() trawlkit.Trawler { return whatsapp.New() }},
}

func macAppBranding(symbolName, accentColor, bundleIdentifier, artworkBundleIdentifier string) *federation.TrawlerBranding {
	return &federation.TrawlerBranding{
		SymbolName: symbolName, AccentColor: accentColor,
		BundleIdentifier: bundleIdentifier, ArtworkBundleIdentifier: artworkBundleIdentifier,
	}
}

func buildRegisteredTrawlerManifestSnapshot(
	includeDisabledTrawlersInRegisteredTrawlerCatalog bool,
) registeredTrawlerManifestSnapshot {
	return buildRegisteredTrawlerManifestSnapshotForContext(context.Background(), "", includeDisabledTrawlersInRegisteredTrawlerCatalog)
}

func buildRegisteredTrawlerManifestSnapshotForContext(
	ctx context.Context,
	stateRoot string,
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
	appendBundledMultiCallPluginsToSnapshot(stateRoot, seenRegisteredTrawlerIdentities, &snapshot)
	appendDiscoveredExternalPluginsToSnapshot(ctx, stateRoot, seenRegisteredTrawlerIdentities, &snapshot)
	return snapshot
}

func appendBundledMultiCallPluginsToSnapshot(
	stateRoot string,
	seenIdentities map[string]struct{},
	snapshot *registeredTrawlerManifestSnapshot,
) {
	bundled := pluginruntime.LoadBundledMultiCallPlugins(stateRoot, bundledMultiCallPlugins, false)
	for _, plugin := range bundled {
		if id := strings.TrimSpace(plugin.PluginIdentifier); id != "" {
			seenIdentities[id] = struct{}{}
		}
	}
	appendDiscoveredPluginsToSnapshot(bundled, snapshot)
}

func appendDiscoveredExternalPluginsToSnapshot(
	ctx context.Context,
	stateRoot string,
	builtInIdentities map[string]struct{},
	snapshot *registeredTrawlerManifestSnapshot,
) {
	discovered := pluginruntime.DiscoverExternalPlugins(ctx, stateRoot, builtInIdentities)
	appendDiscoveredPluginsToSnapshot(discovered, snapshot)
}

func appendDiscoveredPluginsToSnapshot(
	discovered []pluginruntime.DiscoveredExternalPlugin,
	snapshot *registeredTrawlerManifestSnapshot,
) {
	for _, plugin := range discovered {
		manifest := plugin.Manifest.GetRegisteredTrawlerManifest()
		discoveryErr := plugin.DiscoveryError
		if discoveryErr == nil && manifest == nil {
			discoveryErr = errors.New("external plugin manifest is nil")
		}
		if discoveryErr == nil {
			manifest = proto.Clone(manifest).(*federation.RegisteredTrawlerManifest)
			id := trawlkit.RegisteredTrawlerIdentityText(manifest.GetRegisteredTrawler())
			discoveryErr = validateTrawlerPresentation(
				id,
				manifest.GetRegisteredTrawlerDisplayName(),
				manifest.GetTrawlerBranding(),
				manifest.GetSourceFreshnessDependency(),
			)
		} else if manifest == nil && strings.TrimSpace(plugin.PluginIdentifier) != "" {
			id := strings.TrimSpace(plugin.PluginIdentifier)
			manifest = &federation.RegisteredTrawlerManifest{
				RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(id),
				RegisteredTrawlerCommandName: id,
				RegisteredTrawlerDisplayName: id,
			}
		}
		snapshot.installedTrawlers = append(snapshot.installedTrawlers, InstalledTrawler{
			RegisteredTrawlerManifest: manifest,
			TrawlerDiscoveryError:     discoveryErr,
			Trawler:                   plugin.Trawler,
		})
		if discoveryErr != nil {
			continue
		}
		snapshot.registeredTrawlerCatalogEntries = append(snapshot.registeredTrawlerCatalogEntries, &federation.RegisteredTrawlerCatalogEntry{
			RegisteredTrawlerManifest:     manifest,
			RegisteredTrawlerReleaseState: federation.RegisteredTrawlerReleaseState_REGISTERED_TRAWLER_RELEASE_STATE_AVAILABLE,
			RegisteredTrawlerIsEnabled:    true,
		})
	}
}

func buildRegisteredTrawlerManifestEntry(registration trawlerRegistration) registeredTrawlerManifestEntry {
	trawler := registration.factory()
	manifest, err := trawlkit.Manifest(trawler)
	if err == nil && manifest == nil {
		err = errors.New("manifest is nil")
	}
	if err == nil {
		if manifest.GetTrawlerBranding() == nil && registration.fallbackBranding != nil {
			manifest.TrawlerBranding = cloneTrawlerBranding(registration.fallbackBranding)
		}
		if manifest.GetSourceFreshnessDependency() == federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_UNSPECIFIED &&
			registration.fallbackSourceFreshnessDependency != federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_UNSPECIFIED {
			manifest.SourceFreshnessDependency = registration.fallbackSourceFreshnessDependency
		}
		registeredTrawlerIdentity := trawlkit.RegisteredTrawlerIdentityText(manifest.GetRegisteredTrawler())
		if registeredTrawlerIdentity == "" {
			err = errors.New("registered trawler identity is empty")
		} else {
			manifest.RegisteredTrawler.RegisteredTrawlerIdentity = registeredTrawlerIdentity
			err = validateTrawlerPresentation(
				registeredTrawlerIdentity,
				manifest.GetRegisteredTrawlerDisplayName(),
				manifest.GetTrawlerBranding(),
				manifest.GetSourceFreshnessDependency(),
			)
		}
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

func validateTrawlerPresentation(
	id, displayName string,
	branding *federation.TrawlerBranding,
	sourceFreshnessDependency federation.SourceFreshnessDependency,
) error {
	if id == "" {
		return fmt.Errorf("registered trawler manifest identity is empty")
	}
	if displayName == "" {
		return fmt.Errorf("registered trawler %q display name is empty", id)
	}
	if branding == nil || strings.TrimSpace(branding.SymbolName) == "" {
		return fmt.Errorf("registered trawler %q symbol name is empty", id)
	}
	if !validHexColour(branding.AccentColor) {
		return fmt.Errorf("registered trawler %q accent colour %q is not #RRGGBB", id, branding.AccentColor)
	}
	switch sourceFreshnessDependency {
	case federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_NONE:
	case federation.SourceFreshnessDependency_SOURCE_FRESHNESS_DEPENDENCY_SOURCE_APP_RUNNING:
		if strings.TrimSpace(branding.GetBundleIdentifier()) == "" {
			return fmt.Errorf("registered trawler %q requires source app running but has no bundle identifier", id)
		}
	default:
		return fmt.Errorf("registered trawler %q has unspecified source freshness dependency", id)
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

// ExecuteBundledPluginWorker runs one bundled first-party plugin as a sandboxed
// protobuf worker process invoked via trawlkit.HiddenBundledPluginWorkerSubcommand.
func ExecuteBundledPluginWorker(args []string) int {
	if len(args) != 1 {
		return pluginruntime.ServeBundledPluginWorkerError(
			fmt.Errorf("bundled plugin worker requires one plugin identifier, got %d arguments", len(args)),
		)
	}
	pluginID := strings.TrimSpace(args[0])
	for _, registration := range bundledMultiCallPlugins {
		if registration.PluginIdentifier == pluginID {
			return pluginruntime.ServePluginExecutable(registration.Factory())
		}
	}
	return pluginruntime.ServeBundledPluginWorkerError(fmt.Errorf("unknown bundled plugin %q", pluginID))
}
