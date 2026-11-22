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

func (r *runtime) loadOpenRecord(localShortReference *trawlkit.LocalTrawlerShortReference) (*open.OpenRecord, error) {
	canonicalRef, err := r.resolveOpenCanonicalReference(localShortReference)
	if err != nil {
		return nil, err
	}
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
		value = openValue{result: result, ownerAuthorID: ownerAuthorID}
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
	participantNames := make([]string, 0, len(window.Participants))
	for _, p := range window.Participants {
		if display := twitterProfileDisplayName(p.DisplayName, p.Handle, ""); display != "" {
			participantNames = append(participantNames, display)
		}
	}
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

func (r *runtime) resolveOpenCanonicalReference(localShortReference *trawlkit.LocalTrawlerShortReference) (string, error) {
	localShortReferenceText := trawlkit.LocalTrawlerShortReferenceText(localShortReference)
	if !trawlkit.ValidShortRef(localShortReferenceText) {
		return "", r.unknownShortRef()
	}
	matches, err := r.req.ResolveShortReference(r.ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", r.unknownShortRef()
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", r.contractError("ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounPost).Error())
	}
	if err != nil {
		return "", err
	}
	if len(matches) != 1 {
		return "", r.unknownShortRef()
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(matches[0]), nil
}

func (r *runtime) unknownShortRef() error {
	return r.contractError("unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPost).Error())
}
