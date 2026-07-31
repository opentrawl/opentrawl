package trawlkit

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlkit/control"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
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
	RegisteredTrawler                *RegisteredTrawlerIdentity
	RegisteredTrawlerCommandName     string
	RegisteredTrawlerAliases         []string
	RegisteredTrawlerDisplayName     string
	RegisteredTrawlerPrivacyBoundary control.Privacy
	// DefaultTrawlerArchivePaths overrides the runner defaults when a trawler
	// owns a non-SQLite archive or an existing archive layout.
	DefaultTrawlerArchivePaths TrawlerArchivePaths
}

type TrawlerConfigurationFilePath string

type TrawlerArchivePaths struct {
	TrawlerArchivePath       string
	TrawlerConfigurationPath TrawlerConfigurationFilePath
	TrawlerLogDirectoryPath  string
}

type TrawlerCommandExecutionRequest struct {
	OpenedTrawlerArchiveStore         *store.Store
	TrawlerArchivePaths               TrawlerArchivePaths
	TrawlerCommandPositionalArguments []string
	TrawlerCommandLog                 *cklog.Run
	ReportTrawlerCommandProgress      func(Progress)
	RequestedRecordAnchor             *RecordAnchorIdentifier
}

type ShortReferenceAssignmentCandidate struct {
	StableRecordReferenceUsedForShortReferenceAssignment       *CanonicalArchiveRecordReference
	CurrentRecordReferenceReturnedWhenShortReferenceIsResolved *CanonicalArchiveRecordReference
}
