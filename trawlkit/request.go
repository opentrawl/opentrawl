package trawlkit

import (
	"context"
	"net/http"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type internalAppRequestKey struct{}

// WithInternalAppRequest marks a request path that is reachable only through
// the embedded Mac app transport.
func WithInternalAppRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalAppRequestKey{}, true)
}

// IsInternalAppRequest reports whether a request came through the embedded
// Mac app transport.
func IsInternalAppRequest(ctx context.Context) bool {
	marked, _ := ctx.Value(internalAppRequestKey{}).(bool)
	return marked
}

type RegisteredTrawlerDeclaration struct {
	SearchMatchesResolvedPersonDisplayNames bool
	RegisteredTrawler                       *RegisteredTrawlerIdentity
	RegisteredTrawlerCommandName            string
	RegisteredTrawlerAliases                []string
	RegisteredTrawlerDisplayName            string
	RegisteredTrawlerBranding               *federation.TrawlerBranding
	SourceFreshnessDependency               federation.SourceFreshnessDependency
	RegisteredTrawlerPrivacyBoundary        control.Privacy
	// DefaultTrawlerArchivePaths overrides the runner defaults when a trawler
	// owns a non-SQLite archive or an existing archive layout.
	DefaultTrawlerArchivePaths TrawlerArchivePaths
}

type TrawlerConfigurationFilePath string

type TrawlerArchivePaths struct {
	TrawlerArchiveDirectoryPath    string
	TrawlerArchivePath             string
	TrawlerConfigurationPath       TrawlerConfigurationFilePath
	TrawlerLogDirectoryPath        string
	TrawlerShortReferenceIndexPath string
	UnifiedArchiveIndexPath        string
}

type TrawlerCommandExecutionRequest struct {
	OpenedTrawlerArchiveStore         *store.Store
	TrawlerArchivePaths               TrawlerArchivePaths
	TrawlerCommandPositionalArguments []string
	TrawlerCommandLog                 *cklog.Run
	ReportTrawlerCommandProgress      func(Progress)
	RequestedRecordAnchor             *RecordAnchorIdentifier
	// PluginHTTPClient is routed through the host loopback CONNECT proxy when
	// running inside a standalone plugin process.
	PluginHTTPClient *http.Client
	// BrokeredSecrets holds approved secret values passed over stdin by the
	// host secret broker for the current standalone plugin invocation.
	BrokeredSecrets map[string][]byte
	// RecordSecretMutation asks the host secret broker to save or delete one
	// approved writable secret key for the current standalone plugin.
	RecordSecretMutation func(secretKey string, secretBytes []byte, deleteSecret bool) error
	// RequestOpenBrowserURL asks the host to open an approved HTTPS sign-in URL.
	RequestOpenBrowserURL           func(url string) error
	preResolvedLocalShortReferences map[string]string
	readOnlyShortReferenceCatalog   []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
	coreShortReferenceStore         *store.Store
	registeredTrawlerIdentityText   string
	registeredTrawlerDisplayName    string
	stateRoot                       string
	replayShortReferences           func(context.Context) error
}

type ShortReferenceAssignmentCandidate struct {
	StableRecordReferenceUsedForShortReferenceAssignment       *CanonicalArchiveRecordReference
	CurrentRecordReferenceReturnedWhenShortReferenceIsResolved *CanonicalArchiveRecordReference
}
