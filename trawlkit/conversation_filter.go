package trawlkit

import (
	"strings"

	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
)

func filterConversationsWithExactPersonFilterIdentifiers(
	items []*conversationv1.ConversationRecord,
	exactPersonFilterIdentifiers []string,
) []*conversationv1.ConversationRecord {
	exactPersonFilterIdentifiers = cleanExactPersonFilterIdentifiers(exactPersonFilterIdentifiers)
	kept := make([]*conversationv1.ConversationRecord, 0, len(items))
	for _, item := range items {
		matchingParticipantIndex := conversationRecordMatchingParticipantIndex(
			item,
			exactPersonFilterIdentifiers,
		)
		if matchingParticipantIndex < 0 {
			continue
		}
		surfaceMatchingConversationParticipantIdentity(item, matchingParticipantIndex)
		kept = append(kept, item)
	}
	return kept
}

func conversationRecordMatchingParticipantIndex(
	item *conversationv1.ConversationRecord,
	exactPersonFilterIdentifiers []string,
) int {
	if item == nil {
		return -1
	}
	for participantIndex, participantIdentity := range item.GetConversationParticipantIdentitiesObservedByTrawlerArchive() {
		for _, observedIdentifier := range participantIdentity.GetExactPersonFilterIdentifiersObservedByTrawlerArchive() {
			for _, resolvedIdentifier := range exactPersonFilterIdentifiers {
				if exactPersonFilterIdentifierMatchesObservedIdentifier(
					resolvedIdentifier,
					observedIdentifier,
				) {
					return participantIndex
				}
			}
		}
	}
	return -1
}

func surfaceMatchingConversationParticipantIdentity(
	item *conversationv1.ConversationRecord,
	matchingParticipantIndex int,
) {
	if matchingParticipantIndex <= 0 {
		return
	}
	participantIdentities := item.GetConversationParticipantIdentitiesObservedByTrawlerArchive()
	if matchingParticipantIndex >= len(participantIdentities) {
		return
	}
	reorderedParticipantIdentities := make(
		[]*conversationv1.ConversationParticipantIdentityObservedByTrawlerArchive,
		0,
		len(participantIdentities),
	)
	reorderedParticipantIdentities = append(
		reorderedParticipantIdentities,
		participantIdentities[matchingParticipantIndex],
	)
	reorderedParticipantIdentities = append(
		reorderedParticipantIdentities,
		participantIdentities[:matchingParticipantIndex]...,
	)
	reorderedParticipantIdentities = append(
		reorderedParticipantIdentities,
		participantIdentities[matchingParticipantIndex+1:]...,
	)
	item.ConversationParticipantIdentitiesObservedByTrawlerArchive = reorderedParticipantIdentities
}

func cleanExactPersonFilterIdentifiers(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, value)
	}
	return out
}

func exactPersonFilterIdentifierMatchesObservedIdentifier(
	resolvedIdentifier string,
	observedIdentifier string,
) bool {
	resolvedIdentifier = strings.TrimSpace(resolvedIdentifier)
	observedIdentifier = strings.TrimSpace(observedIdentifier)
	return resolvedIdentifier != "" && strings.EqualFold(resolvedIdentifier, observedIdentifier)
}
