package twitter

import (
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

const openDMContextRadius = 10

func (r *runtime) loadOpenRecord(canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference) (*open.OpenRecord, error) {
	canonicalRef := trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReference)
	switch {
	case strings.HasPrefix(canonicalRef, store.TweetRefPrefix):
		tweetID, err := store.ParseTweetRef(canonicalRef)
		if err != nil {
			return nil, err
		}
		return r.loadOpenPostByID(tweetID)
	case strings.HasPrefix(canonicalRef, store.DMMessageRefPrefix):
		msgID, err := store.ParseDMMessageRef(canonicalRef)
		if err != nil {
			return nil, err
		}
		return r.loadOpenDirectMessageByID(msgID)
	case strings.HasPrefix(canonicalRef, store.DMConversationRefPrefix):
		convID, err := store.ParseDMConversationRef(canonicalRef)
		if err != nil {
			return nil, err
		}
		return r.loadOpenDirectMessageConversationByID(convID)
	default:
		return nil, r.unknownShortRef()
	}
}

func (r *runtime) loadOpenPostByID(id string) (*open.OpenRecord, error) {
	var value openValue
	err := r.withReadOnlyStore(func(st *store.Store) error {
		result, err := st.OpenTweet(r.ctx, id)
		if errors.Is(err, store.ErrTweetNotFound) {
			return r.contractError("not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPost).Error())
		}
		if err != nil {
			return err
		}
		ownerAuthorID, err := st.OwnerAuthorID(r.ctx)
		if err != nil {
			return err
		}
		quotedTweet, err := st.QuotedTweetOf(r.ctx, result.Tweet)
		if err != nil {
			return err
		}
		value = openValue{result: result, ownerAuthorID: ownerAuthorID, quotedTweet: quotedTweet}
		return nil
	})
	if err != nil {
		return nil, err
	}
	openedTwitterPostRecord := projectOpenRecord(value)
	return &open.OpenRecord{
		RecordTrawler:            r.c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: openedTwitterPostRecord.GetCanonicalTwitterPostRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_TrawlerSpecificOpenedRecordPresentation{
			TrawlerSpecificOpenedRecordPresentation: &open.TrawlerSpecificOpenedRecordPresentation{
				DetailPresentation: projectOpenDetailPresentation(value),
			},
		},
	}, nil
}

func (r *runtime) loadOpenDirectMessageByID(id string) (*open.OpenRecord, error) {
	var window store.DirectMessageOpenWindow
	err := r.withReadOnlyStore(func(st *store.Store) error {
		var err error
		window, err = st.OpenDirectMessageWindow(r.ctx, id, openDMContextRadius)
		if errors.Is(err, store.ErrDirectMessageNotFound) {
			return r.contractError("not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage).Error())
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	contextRecords := make([]*message.MessageRecord, 0, len(window.Messages))
	for _, dm := range window.Messages {
		contextRecords = append(contextRecords, projectDirectMessageRecord(dm))
	}
	participantNames := deduplicateTwitterDisplayNames(window.Participants)
	opened := &message.OpenedMessageRecordWithConversationContext{
		ConversationDisplayName:                      directMessageWhere(window.Target),
		ConversationParticipantDisplayNames:          participantNames,
		ConversationContextMessageRecordsNewestFirst: contextRecords,
		OpenedMessageRecordReference:                 trawlkit.NewCanonicalArchiveRecordReference(store.DMMessageRef(window.Target.ID)),
		OpenedMessageRecordAnchor:                    trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
		EarlierConversationContextMessagesOmitted:    window.BeforeTruncated,
		LaterConversationContextMessagesOmitted:      window.AfterTruncated,
		ConversationRecordReference:                  trawlkit.NewCanonicalArchiveRecordReference(store.DMConversationRef(window.Conversation.ID)),
	}
	return &open.OpenRecord{
		RecordTrawler:            r.c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: opened.OpenedMessageRecordReference,
		TypedOpenedRecord: &open.OpenRecord_OpenedMessageRecordWithConversationContext{
			OpenedMessageRecordWithConversationContext: opened,
		},
	}, nil
}

func (r *runtime) loadOpenDirectMessageConversationByID(id string) (*open.OpenRecord, error) {
	var conv store.DirectMessageConversation
	var participants []store.Profile
	err := r.withReadOnlyStore(func(st *store.Store) error {
		var err error
		conv, participants, err = st.OpenDirectMessageConversationByID(r.ctx, id)
		if errors.Is(err, store.ErrDirectMessageConversationNotFound) {
			return r.contractError("not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounConversation).Error())
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	record := projectDirectMessageConversationRecord(conv, participants)
	return &open.OpenRecord{
		RecordTrawler:            r.c.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: record.GetCanonicalRecordReference(),
		TypedOpenedRecord: &open.OpenRecord_ConversationRecord{
			ConversationRecord: record,
		},
	}, nil
}

func (r *runtime) unknownShortRef() error {
	return r.contractError("unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPost).Error())
}

func deduplicateTwitterDisplayNames(participants []store.Profile) []string {
	participantNames := make([]string, 0, len(participants))
	seenDisplayNames := make(map[string]struct{}, len(participants))
	for _, participant := range participants {
		display := twitterProfileDisplayName(participant.DisplayName, participant.Handle, "")
		if display == "" {
			continue
		}
		key := strings.ToLower(display)
		if _, alreadySeen := seenDisplayNames[key]; alreadySeen {
			continue
		}
		seenDisplayNames[key] = struct{}{}
		participantNames = append(participantNames, display)
	}
	return participantNames
}
