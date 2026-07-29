package trawlkit

import (
	"context"

	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
	messagev1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message/v1"
	openv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	syncv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/sync/v1"
)

type Trawler interface {
	RegisteredTrawlerDeclaration() RegisteredTrawlerDeclaration
	// Status reports archive counts, the exact last successful sync time and
	// whether current human commands work.
	Status(ctx context.Context, req *TrawlerCommandExecutionRequest) (*statusv1.TrawlerStatusResponse, error)
	TrawlerCommands() []TrawlerCommand
}

type Syncer interface {
	Sync(ctx context.Context, req *TrawlerCommandExecutionRequest) (*syncv1.TrawlerArchiveSyncReport, error)
}

type Searcher interface {
	Search(ctx context.Context, req *TrawlerCommandExecutionRequest, query Query) (*searchv1.TrawlerSearchResponse, error)
}

type WhoMatcher interface {
	Who(ctx context.Context, req *TrawlerCommandExecutionRequest, person string) (*personv1.TrawlerPersonMatchResponse, error)
}

type ConversationLister interface {
	Conversations(ctx context.Context, req *TrawlerCommandExecutionRequest, q ConversationQuery) (*conversationv1.ConversationListResponse, error)
}

type TrawlerMessageLister interface {
	ListMessages(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
		query TrawlerMessageListQuery,
	) (*messagev1.MessageListResponse, error)
}

type RecordOpener interface {
	OpenRecord(ctx context.Context, req *TrawlerCommandExecutionRequest, ref string) (*openv1.OpenRecord, error)
}

type PeopleSnapshotProvider interface {
	PeopleSnapshot(ctx context.Context, req *TrawlerCommandExecutionRequest) (*personv1.TrawlerPeopleSnapshot, error)
}

// PeopleReconciler owns the durable People archive and can replace one
// source's identities with that source's current typed snapshot.
type PeopleReconciler interface {
	ReconcilePeopleSnapshot(ctx context.Context, req *TrawlerCommandExecutionRequest, source string, snapshot *personv1.TrawlerPeopleSnapshot) (*syncv1.TrawlerArchiveSyncReport, error)
}

type ShortReferenceAssignmentProvider interface {
	RecordReferencesForShortReferenceAssignment(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
	) ([]ShortReferenceAssignmentCandidate, error)
}

// ArchivePreparer lets a trawler peek at its archive file and park an
// out-of-date one aside before the harness opens the long-lived write
// connection (req.OpenedTrawlerArchiveStore) the rest of a mutating command runs against.
//
// Implement this when a trawler owns a self-versioned archive with no
// in-place migration path: something else must decide, before req.OpenedTrawlerArchiveStore
// exists, whether the file on disk needs to move aside. The harness calls
// PrepareArchive ahead of opening req.OpenedTrawlerArchiveStore for every storeWrite command, so
// there is no connection yet for the trawler to close or swap out from under
// a command that keeps running against req.OpenedTrawlerArchiveStore after the trawler's own command
// method returns (see assignSourceShortRefs, which runs Sync's req.OpenedTrawlerArchiveStore
// again immediately after Sync itself).
//
// PrepareArchive must not apply schema DDL to a file it might park: doing so
// mutates the very bytes the park is meant to preserve untouched. A trawler
// that has nothing to check can simply not implement this interface.
type ArchivePreparer interface {
	PrepareArchive(ctx context.Context, path string) error
}

type SuccessfullyCompletedArchiveSyncRecorder interface {
	RecordSuccessfullyCompletedArchiveSync(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
	) error
}

// ReadArchivePreparer upgrades or validates a trawler-owned archive before
// the harness opens an optional or read-only request store. It owns and closes
// any connection used for preparation.
type ReadArchivePreparer interface {
	PrepareReadArchive(ctx context.Context, path string) error
}
