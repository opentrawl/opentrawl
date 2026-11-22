// Package mail archives Apple Mail's local store: iCloud Mail and every other
// Mail.app account except a Gmail account that the Gmail trawler archives.
package mail

import (
	"context"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/mail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	registeredTrawlerIdentity    = "mail"
	registeredTrawlerDisplayName = "Mail"
)

type Crawler struct{}

var (
	_ trawlkit.Trawler                = (*Crawler)(nil)
	_ trawlkit.Updater                = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.RecordOpener           = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (*Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(registeredTrawlerIdentity),
		RegisteredTrawlerCommandName: registeredTrawlerIdentity,
		RegisteredTrawlerDisplayName: registeredTrawlerDisplayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "The messages Apple Mail keeps on this Mac, its mailbox list and the names of your Mail accounts. It skips Junk and Trash, and a Gmail account that the Gmail trawler already archives.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Updates read only the files Apple Mail has already downloaded.",
		},
	}
}

func (*Crawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (*Crawler) TrawlerCommands() []trawlkit.TrawlerCommand {
	sharedOperations := []federation.SharedTrawlerOperation{
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN,
		federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO,
	}
	commands := make([]trawlkit.TrawlerCommand, 0, len(sharedOperations))
	for _, operation := range sharedOperations {
		commands = append(commands, trawlkit.TrawlerCommand{
			SharedTrawlerOperation:           operation,
			TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandRoutedOnlyByRootSharedCommand,
		})
	}
	return commands
}

func (*Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := archive.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	messageCount, err := archiveStore.CountMessagesStillInAppleMail(ctx)
	if err != nil {
		return response, nil
	}
	completedTime, err := archiveStore.LastSuccessfullyCompletedUpdateTime(ctx)
	if err != nil {
		return response, nil
	}
	newestMessageTime, err := archiveStore.NewestMessageTimeStillInAppleMail(ctx)
	if err != nil {
		return response, nil
	}
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(max(messageCount, 0))},
	}
	if !completedTime.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completedTime)
	}
	if !newestMessageTime.IsZero() {
		trawlerArchiveStatus.NewestArchivedRecordTime = timestamppb.New(newestMessageTime)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (*Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, personQuery string) (*person.TrawlerPersonMatchResponse, error) {
	archiveStore, err := archive.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveUnavailableError(err)
	}
	candidates, err := archiveStore.ResolveMailPersonCandidates(ctx, strings.Join(strings.Fields(personQuery), " "))
	if err != nil {
		return nil, err
	}
	matches := make([]*person.TrawlerPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		match := &person.TrawlerPersonMatchCandidate{
			PersonDisplayName: candidate.PersonDisplayName,
			PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{
				trawlkit.NewPersonMatchFactsFromTrawler(
					trawlkit.NewRegisteredTrawlerIdentity(registeredTrawlerIdentity),
					candidate.PersonIdentifiers,
					candidate.PersonDisplayName,
				),
			},
			MessageCountInvolvingPerson: uint64(max(candidate.MessageCountInvolvingPerson, 0)),
		}
		if candidate.LatestMessageTime.After(time.Unix(0, 0)) {
			match.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.LatestMessageTime)
		}
		matches = append(matches, match)
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: matches}, nil
}

func (*Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error) {
	archiveStore, err := archive.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveUnavailableError(err)
	}
	contacts, err := archiveStore.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: contacts}, nil
}
