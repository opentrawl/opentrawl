package imessage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	appID   = "imessage"
	display = "iMessage"
)

type Crawler struct{}

var (
	_ trawlkit.Trawler                = (*Crawler)(nil)
	_ trawlkit.Updater                = (*Crawler)(nil)
	_ trawlkit.Searcher               = (*Crawler)(nil)
	_ trawlkit.WhoMatcher             = (*Crawler)(nil)
	_ trawlkit.ConversationLister     = (*Crawler)(nil)
	_ trawlkit.TrawlerMessageLister   = (*Crawler)(nil)
	_ trawlkit.PeopleSnapshotProvider = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(appID),
		RegisteredTrawlerCommandName: "imessage",
		RegisteredTrawlerDisplayName: display,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Messages' local database and Apple Contacts, which it uses to put names to message participants. This includes messages, conversations and information about attachments.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Updates use only local data.",
		},
	}
}

func (*Crawler) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return response, nil
	}
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.Messages)},
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: uint64(archiveStatus.Chats)},
	}
	if completedAt := parseArchiveTime(archiveStatus.LastUpdateAt); !completedAt.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(completedAt)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = archiveStatus.Messages == 0 ||
		archiveStatus.ArchiveContainsMessageAvailableThroughConversationCommands
	return response, nil
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	options := archive.SearchOptions{
		Limit:     query.Limit,
		After:     appleDateOrZero(query.After),
		HasAfter:  !query.After.IsZero(),
		Before:    appleDateOrZero(query.Before),
		HasBefore: !query.Before.IsZero(),
	}
	if strings.TrimSpace(query.Who) != "" {
		candidate, err := resolveArchiveWho(ctx, st, query.Who)
		if err != nil {
			return nil, err
		}
		options.Who = &candidate
	}
	page, err := st.SearchPage(ctx, query.Text, options)
	if err != nil {
		return nil, err
	}
	searchMatches := make([]*search.TrawlerSearchMatch, 0, len(page.Items))
	for _, item := range page.Items {
		searchMatch, err := searchMatch(item)
		if err != nil {
			return nil, err
		}
		searchMatches = append(searchMatches, searchMatch)
	}
	return &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: searchMatches,
		TotalSearchMatches:                 uint64(page.Total),
		MoreSearchMatchesExist:             page.Total > int64(len(searchMatches)),
	}, nil
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, personQuery string) (*person.TrawlerPersonMatchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolution, err := st.ResolveWho(ctx, personQuery)
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonMatchCandidate, 0, len(resolution.Candidates))
	for _, candidate := range resolution.Candidates {
		out = append(out, whoCandidate(candidate))
	}
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func (c *Crawler) PeopleSnapshot(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*person.TrawlerPeopleSnapshot, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	contacts, err := st.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &person.TrawlerPeopleSnapshot{TrawlerPersonIdentities: contacts}, nil
}

func resolveArchiveWho(ctx context.Context, st *archive.Store, who string) (archive.WhoCandidate, error) {
	resolution, err := st.ResolveWho(ctx, who)
	if err != nil {
		return archive.WhoCandidate{}, err
	}
	candidate, ok := resolution.FilterCandidate()
	if !ok {
		return archive.WhoCandidate{}, fmt.Errorf("resolved who %q was not unique", who)
	}
	return candidate, nil
}

func whoCandidate(candidate archive.WhoCandidate) *person.TrawlerPersonMatchCandidate {
	personMatchCandidate := &person.TrawlerPersonMatchCandidate{
		PersonDisplayName: strings.Join(strings.Fields(candidate.Who), " "),
		PersonMatchFactsFromTrawlers: []*person.PersonMatchFactsFromTrawler{{
			RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity(appID),
			ExactPersonFilterIdentifiersObservedByTrawlerArchive: append([]string(nil), candidate.Identifiers...),
			PersonDisplayNamesObservedByTrawlerArchive:           []string{candidate.Who},
		}},
	}
	if latestMatchingArchiveRecordTime := parseArchiveTime(candidate.LastSeen); !latestMatchingArchiveRecordTime.IsZero() {
		personMatchCandidate.LatestMatchingArchiveRecordTime = timestamppb.New(latestMatchingArchiveRecordTime)
	}
	if candidate.Messages > 0 {
		personMatchCandidate.MessageCountInvolvingPerson = uint64(candidate.Messages)
	}
	return personMatchCandidate
}

func searchMatch(item archive.SearchResult) (*search.TrawlerSearchMatch, error) {
	messageTime := parseArchiveTime(item.Time)
	if messageTime.IsZero() && strings.TrimSpace(item.Time) != "" {
		return nil, fmt.Errorf("parse message time %q", item.Time)
	}
	searchMatchPresentation := &search.SearchMatchPresentation{
		MatchingRecordKindDisplayName: "message",
	}
	if !messageTime.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(messageTime)},
		}
	}
	if senderDisplayName := messageSearchSenderDisplayIdentity(item.FromMe, item.SenderLabel); senderDisplayName != "" {
		searchMatchPresentation.PeopleRelatedToMatchingRecord = append(
			searchMatchPresentation.PeopleRelatedToMatchingRecord,
			trawlkit.NewPersonRelatedToSearchMatchingRecord(
				senderDisplayName,
				person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
			),
		)
	}
	if item.FromMe {
		for _, recipientDisplayName := range messageSearchRecipientDisplayIdentities(item) {
			searchMatchPresentation.PeopleRelatedToMatchingRecord = append(
				searchMatchPresentation.PeopleRelatedToMatchingRecord,
				trawlkit.NewPersonRelatedToSearchMatchingRecord(
					recipientDisplayName,
					person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
				),
			)
		}
	} else {
		searchMatchPresentation.PeopleRelatedToMatchingRecord = append(
			searchMatchPresentation.PeopleRelatedToMatchingRecord,
			trawlkit.NewPersonRelatedToSearchMatchingRecord(
				"me",
				person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
			),
		)
	}
	if conversationName := messageSearchConversationName(item); conversationName != "" {
		searchMatchPresentation.DigitalContainerNamesNearestToBroadest = []string{conversationName}
	}
	for _, searchMatch := range item.Matches {
		matchingMessageText := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(
			"Message",
			searchMatch.Runs,
		)
		if matchingMessageText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = append(
				searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder,
				matchingMessageText,
			)
		}
	}
	if len(searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder) == 0 {
		if matchingMessageText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(
			"Message",
			outputField(searchSnippet(item)),
		); matchingMessageText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{matchingMessageText}
		}
	}
	return &search.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
			archive.MessageRef(item.MessageID),
		),
		RecordAnchor:            trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		SearchMatchPresentation: searchMatchPresentation,
	}, nil
}

func messageSearchSenderDisplayIdentity(messageIsFromMe bool, senderLabel string) string {
	if messageIsFromMe {
		return "me"
	}
	return humanParticipantDisplayIdentity(senderLabel)
}

func messageSearchRecipientDisplayIdentities(item archive.SearchResult) []string {
	chat := archive.ChatSummary{
		ChatID:                            item.ChatID,
		Title:                             item.ChatTitle,
		Kind:                              item.ChatKind,
		ParticipantCount:                  item.ChatParticipantCount,
		ConversationParticipantIdentities: item.ChatConversationParticipantIdentities,
	}
	return conversationParticipantDisplayIdentities(chat)
}

func messageSearchConversationName(item archive.SearchResult) string {
	chat := archive.ChatSummary{
		ChatID:                            item.ChatID,
		Title:                             item.ChatTitle,
		Kind:                              item.ChatKind,
		ParticipantCount:                  item.ChatParticipantCount,
		ConversationParticipantIdentities: item.ChatConversationParticipantIdentities,
	}
	if title := conversationListTitle(chat); title != "" {
		return outputField(title)
	}
	if chat.Kind == "group" {
		return ""
	}
	participantDisplayIdentities := conversationParticipantDisplayIdentities(chat)
	if len(participantDisplayIdentities) == 1 {
		return outputField(participantDisplayIdentities[0])
	}
	return ""
}

func appleDateOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return archive.AppleDateFromTime(t)
}
