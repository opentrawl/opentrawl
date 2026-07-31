package trawlkit

import (
	"context"
	"errors"
	"fmt"
	"strings"

	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
)

type executeTrawlerConversationListOperation struct {
	query                                                      ConversationQuery
	response                                                   *conversationv1.ConversationListResponse
	localShortReferencesByCanonicalConversationRecordReference []CanonicalArchiveRecordReferenceWithLocalTrawlerShortReference
}

func (operation *executeTrawlerConversationListOperation) execute(
	ctx context.Context,
	trawler Trawler,
	request *TrawlerCommandExecutionRequest,
) error {
	response, err := executeConversations(
		ctx,
		trawler.(ConversationLister),
		request,
		operation.query,
		trawler.RegisteredTrawlerDeclaration().RegisteredTrawler,
	)
	if err != nil {
		return err
	}
	localShortReferencesByCanonicalConversationRecordReference, err :=
		readAssignedLocalShortReferencesByCanonicalRecordReference(
			ctx,
			request,
			canonicalConversationRecordReferences(response),
		)
	if err != nil {
		return err
	}
	operation.response = response
	operation.localShortReferencesByCanonicalConversationRecordReference =
		localShortReferencesByCanonicalConversationRecordReference
	return nil
}

func executeConversations(
	ctx context.Context,
	lister ConversationLister,
	request *TrawlerCommandExecutionRequest,
	query ConversationQuery,
	registeredTrawler *RegisteredTrawlerIdentity,
) (*conversationv1.ConversationListResponse, error) {
	resolvedPersonFilterWasRequested := query.ResolvedPersonMatchFactsFromTrawlers != nil
	var exactPersonFilterIdentifiersObservedByCurrentTrawlerArchive []string
	for _, personMatchFactsFromTrawler := range query.ResolvedPersonMatchFactsFromTrawlers {
		if !strings.EqualFold(
			RegisteredTrawlerIdentityText(personMatchFactsFromTrawler.GetRegisteredTrawler()),
			RegisteredTrawlerIdentityText(registeredTrawler),
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
		if CanonicalArchiveRecordReferenceText(conversationRecord.GetCanonicalRecordReference()) == "" {
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
) []*CanonicalArchiveRecordReference {
	if response == nil {
		return nil
	}
	canonicalConversationRecordReferences := make(
		[]*CanonicalArchiveRecordReference,
		0,
		len(response.GetConversationRecordsNewestFirst()),
	)
	for _, conversationRecord := range response.GetConversationRecordsNewestFirst() {
		if conversationRecord == nil {
			continue
		}
		canonicalConversationRecordReference := conversationRecord.GetCanonicalRecordReference()
		if CanonicalArchiveRecordReferenceText(canonicalConversationRecordReference) != "" {
			canonicalConversationRecordReferences = append(
				canonicalConversationRecordReferences,
				canonicalConversationRecordReference,
			)
		}
	}
	return canonicalConversationRecordReferences
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
