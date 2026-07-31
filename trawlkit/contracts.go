package trawlkit

import (
	"context"

	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type Trawler interface {
	RegisteredTrawlerDeclaration() RegisteredTrawlerDeclaration
	LoadTrawlerConfiguration(TrawlerConfigurationFilePath) error
	// Status reports archive counts, the exact last successful update time and
	// whether current human commands work.
	Status(ctx context.Context, req *TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error)
	TrawlerCommands() []TrawlerCommand
}

type Updateer interface {
	Update(ctx context.Context, req *TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error)
}

type Searcher interface {
	Search(ctx context.Context, req *TrawlerCommandExecutionRequest, query Query) (*search.TrawlerSearchResponse, error)
}

type WhoMatcher interface {
	Who(ctx context.Context, req *TrawlerCommandExecutionRequest, person string) (*person.TrawlerPersonMatchResponse, error)
}

type ConversationLister interface {
	Conversations(ctx context.Context, req *TrawlerCommandExecutionRequest, q ConversationQuery) (*conversation.ConversationListResponse, error)
}

type TrawlerMessageLister interface {
	ListMessages(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
		query TrawlerMessageListQuery,
	) (*message.MessageListResponse, error)
}

type RecordOpener interface {
	OpenRecord(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
		localShortReference *LocalTrawlerShortReference,
	) (*open.OpenRecord, error)
}

type TrawlerSpecificOpenedRecordActionBuilder interface {
	BuildTrawlerSpecificOpenedRecordActions(
		openedRecord *open.OpenRecord,
	) (render.TrawlerSpecificCommandActions, error)
}

type PeopleSnapshotProvider interface {
	PeopleSnapshot(ctx context.Context, req *TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error)
}

// PeopleReconciler owns the durable People archive and can replace one
// source's identities with that source's current typed snapshot.
type PeopleReconciler interface {
	ReconcilePeopleSnapshot(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
		peopleSnapshotTrawler *RegisteredTrawlerIdentity,
		snapshot *person.TrawlerPeopleSnapshot,
	) (*update.TrawlerArchiveUpdateReport, error)
}

type SuccessfullyCompletedArchiveUpdateRecorder interface {
	RecordSuccessfullyCompletedArchiveUpdate(
		ctx context.Context,
		req *TrawlerCommandExecutionRequest,
	) error
}
