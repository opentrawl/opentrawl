package telegram

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	r := c.handler(ctx, req)
	filter, err := c.searchFilter(query)
	if err != nil {
		return nil, err
	}
	filter.ChatJID, err = req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			c.search.LocalConversationShortReferenceAcceptedBySelectedTrawler,
		),
		store.ChatRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageErr(errors.New("The link is for a message, not a conversation."))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandErr(1, "not_found", errors.New("No conversation has that link."))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, usageErr(errors.New("More than one conversation has that link."))
	}
	if err != nil {
		return nil, err
	}
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	_, err = r.resolveSearchWhoFilter(st, &filter)
	if err != nil {
		return nil, err
	}
	messages, err := st.Search(ctx, filter)
	if err != nil {
		return nil, err
	}
	total, err := st.CountSearch(ctx, filter)
	if err != nil {
		return nil, err
	}
	searchMatches := make([]*searchv1.TrawlerSearchMatch, 0, len(messages))
	outgoingGroupRecipientDisplayNamesByConversation := map[string][]string{}
	for _, message := range messages {
		peopleRelatedToMessage, err := telegramMessagePeople(
			ctx,
			st,
			message,
			outgoingGroupRecipientDisplayNamesByConversation,
		)
		if err != nil {
			return nil, err
		}
		searchMatches = append(searchMatches, telegramMessageSearchMatch(message, peopleRelatedToMessage))
	}
	return &searchv1.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: searchMatches,
		TotalSearchMatches:                 uint64(total),
		MoreSearchMatchesExist:             total > len(messages),
	}, nil
}

func (c *Crawler) searchFilter(query trawlkit.Query) (store.MessageFilter, error) {
	if c.search.FromMe && c.search.FromThem {
		return store.MessageFilter{}, usageErr(errors.New("--from-me and --from-them cannot be used together."))
	}
	filter := store.MessageFilter{
		Query:    strings.Join(strings.Fields(query.Text), " "),
		Sender:   strings.TrimSpace(c.search.Sender),
		Who:      normalizeWords(query.Who),
		Limit:    query.Limit,
		HasMedia: c.search.HasMedia,
		Pinned:   c.search.Pinned,
		Asc:      c.search.Asc,
	}
	if !query.After.IsZero() {
		after := query.After
		filter.After = &after
	}
	if !query.Before.IsZero() {
		before := query.Before
		filter.Before = &before
	}
	if c.search.FromMe || c.search.FromThem {
		fromMe := c.search.FromMe
		filter.FromMe = &fromMe
	}
	return filter, nil
}

func telegramMessageSearchMatch(
	message store.Message,
	peopleRelatedToMessage []*personv1.PersonRelatedToArchiveRecord,
) *searchv1.TrawlerSearchMatch {
	searchMatchPresentation := &searchv1.SearchMatchPresentation{
		MatchingRecordKindDisplayName: "message",
		MatchingRecordDisplayName:     telegramMessageHumanMediaTitle(message),
		PeopleRelatedToMatchingRecord: peopleRelatedToMessage,
	}
	if !message.Timestamp.IsZero() {
		searchMatchPresentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(message.Timestamp)},
		}
	}
	searchMatchPresentation.DigitalContainerNamesNearestToBroadest = telegramMessageSearchDigitalContainerNames(message)
	searchMatchPresentation.SearchMatchTextFieldsInDisplayOrder = telegramMessageSearchMatchingRecordTextFields(message)
	return &searchv1.TrawlerSearchMatch{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(messageRef(message.SourcePK)),
		RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		SearchMatchPresentation:  searchMatchPresentation,
	}
}

func telegramMessageSearchDigitalContainerNames(message store.Message) []string {
	containerNames := []string{}
	if topicName := humanTelegramName(message.TopicTitle); topicName != "" {
		containerNames = append(containerNames, topicName)
	}
	conversationName := telegramMessageSearchConversationName(message)
	if conversationName == "" {
		return containerNames
	}
	if message.ChatKind != "user" {
		return append(containerNames, conversationName)
	}
	otherPerson := telegramMessageSearchSenderDisplayName(message)
	if message.FromMe {
		otherPerson = conversationName
	}
	if !strings.EqualFold(conversationName, otherPerson) {
		containerNames = append(containerNames, conversationName)
	}
	return containerNames
}

func telegramMessageSearchSenderDisplayName(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	return humanTelegramName(message.SenderName)
}

func telegramMessageSearchConversationName(message store.Message) string {
	return humanTelegramName(message.ChatName)
}

func telegramMessageHumanMediaTitle(message store.Message) string {
	mediaTitle := outputField(message.MediaTitle)
	if mediaTitle == "" ||
		strings.EqualFold(mediaTitle, outputField(message.MediaType)) ||
		strings.Contains(mediaTitle, "://") ||
		telegramMediaTitleContainsOpaqueProviderIdentifier(mediaTitle) {
		return ""
	}
	return mediaTitle
}

func telegramMediaTitleContainsOpaqueProviderIdentifier(mediaTitle string) bool {
	for _, mediaTitleToken := range strings.Fields(mediaTitle) {
		mediaTitleToken = strings.Trim(mediaTitleToken, `"'.,;:()[]{}<>`)
		if telegramMediaTitleTokenIsOpaqueProviderIdentifier(mediaTitleToken) ||
			telegramMediaTitleTokenIsOpaqueProviderIdentifier(strings.TrimSuffix(mediaTitleToken, filepath.Ext(mediaTitleToken))) {
			return true
		}
	}
	return false
}

func telegramMediaTitleTokenIsOpaqueProviderIdentifier(mediaTitleToken string) bool {
	if len(mediaTitleToken) < 40 {
		return false
	}
	allHexadecimal := true
	allBase64Characters := true
	hasBase64Punctuation := false
	for _, character := range mediaTitleToken {
		if (character < '0' || character > '9') &&
			(character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			allHexadecimal = false
		}
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		case character == '+', character == '/', character == '_', character == '-', character == '=':
			hasBase64Punctuation = true
		default:
			allBase64Characters = false
		}
	}
	return allHexadecimal || (allBase64Characters && (hasBase64Punctuation || len(mediaTitleToken)%4 == 0))
}

func telegramMessageSearchMatchingRecordTextFields(message store.Message) []*searchv1.SearchMatchTextField {
	matchingRecordTextFields := make(
		[]*searchv1.SearchMatchTextField,
		0,
		len(message.SearchMatches),
	)
	for _, messageSearchMatch := range message.SearchMatches {
		if messageSearchMatch.Field != "Message" && messageSearchMatch.Field != "Media" {
			continue
		}
		matchingRecordTextField := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns(
			messageSearchMatch.Field,
			messageSearchMatch.Runs,
		)
		if matchingRecordTextField != nil {
			matchingRecordTextFields = append(matchingRecordTextFields, matchingRecordTextField)
		}
	}
	if len(matchingRecordTextFields) > 0 {
		return matchingRecordTextFields
	}
	if len(message.SearchMatches) > 0 {
		return nil
	}
	if matchingMessageText := trawlkit.NewSearchMatchTextFieldWithoutSearchQueryMatch(
		"Message",
		outputField(messageSnippet(message)),
	); matchingMessageText != nil {
		return []*searchv1.SearchMatchTextField{matchingMessageText}
	}
	return nil
}

func (r *runtime) resolveSearchWhoFilter(st *store.Store, filter *store.MessageFilter) (*store.WhoCandidate, error) {
	if strings.TrimSpace(filter.Who) == "" {
		return nil, nil
	}
	candidates, err := st.ResolveWho(r.ctx, filter.Who)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, r.unknownWhoError(filter.Who)
	}
	if len(candidates) > 1 {
		return nil, r.ambiguousWhoError(filter.Who)
	}
	candidate := candidates[0]
	if candidate.MatchedOnlyByCloseSpelling() {
		return nil, r.unknownWhoError(filter.Who)
	}
	filter.WhoParticipants = candidate.Participants
	filter.WhoResolved = true
	return &candidate, nil
}
