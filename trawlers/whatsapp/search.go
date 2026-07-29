package whatsapp

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	filter := store.MessageFilter{
		Query: strings.TrimSpace(query.Text),
		Limit: query.Limit,
	}
	if !query.After.IsZero() {
		filter.After = &query.After
	}
	if !query.Before.IsZero() {
		filter.Before = &query.Before
	}
	if strings.TrimSpace(query.Who) != "" {
		keys, err := resolveWhoKeys(ctx, st, query.Who)
		if err != nil {
			return nil, err
		}
		filter.Who = query.Who
		filter.WhoKeys = keys
	}
	total, err := st.SearchCount(ctx, filter)
	if err != nil {
		return nil, err
	}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		return nil, err
	}
	searchMatches := make([]*searchv1.TrawlerSearchMatch, 0, len(messages))
	for _, message := range messages {
		searchMatches = append(searchMatches, whatsappMessageSearchMatch(message))
	}
	return &searchv1.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: searchMatches,
		TotalSearchMatches:                 uint64(total),
		MoreSearchMatchesExist:             query.Limit > 0 && len(messages) < total,
	}, nil
}

func whatsappMessageSearchMatch(message store.Message) *searchv1.TrawlerSearchMatch {
	peopleRelatedToMessage := []*personv1.PersonRelatedToArchiveRecord{{
		PersonDisplayName:         "me",
		PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
	}}
	if message.FromMe {
		peopleRelatedToMessage[0].PersonRoleInArchiveRecord = personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER
		if message.ChatKind == "dm" {
			if recipientDisplayName := whatsappMessageSearchConversationName(message); recipientDisplayName != "" {
				peopleRelatedToMessage = append(peopleRelatedToMessage, &personv1.PersonRelatedToArchiveRecord{
					PersonDisplayName:         recipientDisplayName,
					PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT,
				})
			}
		}
	} else if senderDisplayName := whatsappMessageSearchSenderDisplayName(message); senderDisplayName != "" {
		peopleRelatedToMessage = append(peopleRelatedToMessage, &personv1.PersonRelatedToArchiveRecord{
			PersonDisplayName:         senderDisplayName,
			PersonRoleInArchiveRecord: personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		})
	}
	searchMatchPresentation := &searchv1.SearchMatchPresentation{
		MatchingRecordKindDisplayName: "message",
		MatchingRecordDisplayName:     whatsappMessageHumanMediaTitle(message),
		PeopleRelatedToMatchingRecord: peopleRelatedToMessage,
	}
	if !message.Timestamp.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(message.Timestamp)},
		}
	}
	if conversationName := whatsappMessageSearchConversationName(message); whatsappMessageSearchKeepsConversationName(message, conversationName) {
		searchMatchPresentation.DigitalContainerNamesNearestToBroadest = []string{conversationName}
	}
	for _, messageSearchMatch := range message.SearchMatches {
		if messageSearchMatch.Field != "Message" && messageSearchMatch.Field != "Media" {
			continue
		}
		matchingRecordTextField := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(
			messageSearchMatch.Field,
			messageSearchMatch.Runs,
		)
		if matchingRecordTextField != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = append(
				searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder,
				matchingRecordTextField,
			)
		}
	}
	if len(searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder) == 0 && len(message.SearchMatches) == 0 {
		if matchingMessageText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(
			"Message",
			messageSnippet(message),
		); matchingMessageText != nil {
			searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = []*searchv1.SearchMatchTextField{matchingMessageText}
		}
	}
	return &searchv1.TrawlerSearchMatch{
		CanonicalMatchingRecordReferenceForGloballyRoutableTrawlLinkAssignment: messageRef(message),
		MatchingRecordAnchorIdentifier:                                         trawlkit.MatchAnchorID,
		SearchMatchPresentation:                                                searchMatchPresentation,
	}
}

func whatsappMessageSearchKeepsConversationName(message store.Message, conversationName string) bool {
	if conversationName == "" {
		return false
	}
	if message.ChatKind != "dm" {
		return true
	}
	otherPerson := whatsappMessageSearchSenderDisplayName(message)
	if message.FromMe {
		otherPerson = conversationName
	}
	return !strings.EqualFold(conversationName, otherPerson)
}

func whatsappMessageSearchSenderDisplayName(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	return humanDisplayName(message.SenderName)
}

func whatsappMessageSearchConversationName(message store.Message) string {
	if conversationName := humanDisplayName(message.ChatName); conversationName != "" {
		return conversationName
	}
	if !message.FromMe {
		return humanDisplayName(message.SenderName)
	}
	return ""
}

func whatsappMessageHumanMediaTitle(message store.Message) string {
	for _, mediaTitleCandidate := range []string{
		safeMediaLabel(message.MediaTitle),
		safeMediaFilename(message.MediaPath),
	} {
		if mediaTitleCandidate == "" ||
			strings.Contains(mediaTitleCandidate, "://") ||
			strings.EqualFold(mediaTitleCandidate, outputField(message.MediaType)) {
			continue
		}
		return mediaTitleCandidate
	}
	return ""
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, person string) (*personv1.TrawlerPersonMatchResponse, error) {
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolution, err := st.ResolveWho(ctx, person)
	if err != nil {
		return nil, err
	}
	out := make([]*personv1.TrawlerPersonMatchCandidate, 0, len(resolution.Candidates))
	for _, candidate := range resolution.Candidates {
		out = append(out, whoCandidate(candidate))
	}
	return &personv1.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func resolveWhoKeys(ctx context.Context, st *store.Store, value string) ([]string, error) {
	resolution, err := st.ResolveWhoIdentifier(ctx, value)
	if err != nil {
		return nil, err
	}
	if len(resolution.Candidates) == 0 {
		resolution, err = st.ResolveWho(ctx, value)
		if err != nil {
			return nil, err
		}
	}
	if len(resolution.Candidates) != 1 || resolution.OnlyCloseSpellingMatch() {
		return []string{}, nil
	}
	return append([]string(nil), resolution.ParticipantKeys...), nil
}

func whoCandidate(candidate store.WhoCandidate) *personv1.TrawlerPersonMatchCandidate {
	personMatchCandidate := &personv1.TrawlerPersonMatchCandidate{
		PersonDisplayName: humanParticipantLabel(outputField(candidate.Who)),
		PersonMatchFactsFromTrawlers: []*personv1.PersonMatchFactsFromTrawler{{
			RegisteredTrawlerManifestIdentity:                    "whatsapp",
			ExactPersonFilterIdentifiersObservedByTrawlerArchive: append([]string(nil), candidate.Identifiers...),
			PersonDisplayNamesObservedByTrawlerArchive:           []string{candidate.Who},
		}},
	}
	if !candidate.LastSeen.IsZero() {
		personMatchCandidate.LatestMatchingArchiveRecordTime = timestamppb.New(candidate.LastSeen)
	}
	if candidate.Messages > 0 {
		personMatchCandidate.MessageCountInvolvingPerson = uint64(candidate.Messages)
	}
	return personMatchCandidate
}
