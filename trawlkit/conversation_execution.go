package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
)

type typedConversations struct {
	query                                                 ConversationQuery
	response                                              *conversationv1.ConversationListResponse
	localReferenceAliasesByCanonicalConversationReference map[string]string
}

func (operation *typedConversations) execute(
	ctx context.Context,
	trawler Trawler,
	request *TrawlerCommandExecutionRequest,
) error {
	response, err := executeConversations(
		ctx,
		trawler.(ConversationLister),
		request,
		operation.query,
		trawler.RegisteredTrawlerDeclaration().RegisteredTrawlerManifestIdentity,
	)
	if err != nil {
		return err
	}
	localReferenceAliasesByCanonicalConversationReference, err :=
		readAssignedLocalShortReferenceAliasesByCanonicalRecordReference(
			ctx,
			request,
			canonicalConversationRecordReferences(response),
		)
	if err != nil {
		return err
	}
	operation.response = response
	operation.localReferenceAliasesByCanonicalConversationReference =
		localReferenceAliasesByCanonicalConversationReference
	return nil
}

func executeConversations(
	ctx context.Context,
	lister ConversationLister,
	request *TrawlerCommandExecutionRequest,
	query ConversationQuery,
	registeredTrawlerManifestIdentity string,
) (*conversationv1.ConversationListResponse, error) {
	resolvedPersonFilterWasRequested := query.ResolvedPersonMatchFactsFromTrawlers != nil
	var exactPersonFilterIdentifiersObservedByCurrentTrawlerArchive []string
	for _, personMatchFactsFromTrawler := range query.ResolvedPersonMatchFactsFromTrawlers {
		if !strings.EqualFold(
			strings.TrimSpace(personMatchFactsFromTrawler.GetRegisteredTrawlerManifestIdentity()),
			strings.TrimSpace(registeredTrawlerManifestIdentity),
		) {
			continue
		}
		exactPersonFilterIdentifiersObservedByCurrentTrawlerArchive = append(
			exactPersonFilterIdentifiersObservedByCurrentTrawlerArchive,
			personMatchFactsFromTrawler.GetExactPersonFilterIdentifiersObservedByTrawlerArchive()...,
		)
		break
	}
	fetch := query
	fetch.ResolvedPersonMatchFactsFromTrawlers = nil
	if resolvedPersonFilterWasRequested {
		fetch.All = true
		fetch.Limit = 0
	}
	response, err := lister.Conversations(ctx, request, fetch)
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("trawler returned no conversation list response")
	}
	conversationRecords := append(
		[]*conversationv1.ConversationRecord(nil),
		response.GetConversationRecordsNewestFirst()...,
	)
	for conversationRecordIndex, conversationRecord := range conversationRecords {
		if conversationRecord == nil {
			return nil, fmt.Errorf("conversation record %d is missing", conversationRecordIndex)
		}
		if strings.TrimSpace(
			conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		) == "" {
			return nil, fmt.Errorf(
				"conversation record %d canonical conversation record reference is empty",
				conversationRecordIndex,
			)
		}
	}
	if query.Unread {
		conversationRecords = filterUnreadConversations(conversationRecords)
	}
	if resolvedPersonFilterWasRequested {
		conversationRecords = filterConversationsWithExactPersonFilterIdentifiers(
			conversationRecords,
			exactPersonFilterIdentifiersObservedByCurrentTrawlerArchive,
		)
	}
	moreConversationRecordsExist := response.GetMoreConversationRecordsExist()
	if !query.All && query.Limit > 0 && len(conversationRecords) > query.Limit {
		conversationRecords = conversationRecords[:query.Limit]
		moreConversationRecordsExist = true
	}
	return &conversationv1.ConversationListResponse{
		ConversationRecordsNewestFirst: conversationRecords,
		MoreConversationRecordsExist:   moreConversationRecordsExist,
	}, nil
}

func canonicalConversationRecordReferences(
	response *conversationv1.ConversationListResponse,
) []string {
	if response == nil {
		return nil
	}
	canonicalConversationRecordReferences := make(
		[]string,
		0,
		len(response.GetConversationRecordsNewestFirst()),
	)
	for _, conversationRecord := range response.GetConversationRecordsNewestFirst() {
		if conversationRecord == nil {
			continue
		}
		canonicalConversationRecordReference := strings.TrimSpace(
			conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		)
		if canonicalConversationRecordReference != "" {
			canonicalConversationRecordReferences = append(
				canonicalConversationRecordReferences,
				canonicalConversationRecordReference,
			)
		}
	}
	return uniqueStrings(canonicalConversationRecordReferences)
}

func filterUnreadConversations(
	conversationRecords []*conversationv1.ConversationRecord,
) []*conversationv1.ConversationRecord {
	kept := make([]*conversationv1.ConversationRecord, 0, len(conversationRecords))
	for _, conversationRecord := range conversationRecords {
		if conversationRecord != nil &&
			conversationRecord.UnreadMessageCount != nil &&
			conversationRecord.GetUnreadMessageCount() > 0 {
			kept = append(kept, conversationRecord)
		}
	}
	return kept
}
