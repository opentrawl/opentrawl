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
	"github.com/opentrawl/opentrawl/trawlkit/render"
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
	OpenRecord(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
		localShortReference *LocalTrawlerShortReference,
	) (*openv1.OpenRecord, error)
}

type TrawlerSpecificOpenedRecordActionBuilder interface {
	BuildTrawlerSpecificOpenedRecordActions(
		openedRecord *openv1.OpenRecord,
	) (render.TrawlerSpecificCommandActions, error)
}

type PeopleSnapshotProvider interface {
	PeopleSnapshot(ctx context.Context, req *TrawlerCommandExecutionRequest) (*personv1.TrawlerPeopleSnapshot, error)
}

// PeopleReconciler owns the durable People archive and can replace one
// source's identities with that source's current typed snapshot.
type PeopleReconciler interface {
	ReconcilePeopleSnapshot(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
		peopleSnapshotTrawler *RegisteredTrawlerIdentity,
		snapshot *personv1.TrawlerPeopleSnapshot,
	) (*syncv1.TrawlerArchiveSyncReport, error)
}

type ShortReferenceAssignmentProvider interface {
	RecordReferencesForShortReferenceAssignment(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
	) ([]ShortReferenceAssignmentCandidate, error)
}

type SuccessfullyCompletedArchiveSyncRecorder interface {
	RecordSuccessfullyCompletedArchiveSync(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
	) error
}
