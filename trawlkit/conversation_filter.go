package trawlkit

import (
	"strings"

	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
)

func filterConversationsWithExactPersonFilterIdentifiers(
	items []*conversation.ConversationRecord,
	exactPersonFilterIdentifiers []string,
) []*conversation.ConversationRecord {
	exactPersonFilterIdentifiers = cleanExactPersonFilterIdentifiers(exactPersonFilterIdentifiers)
	kept := make([]*conversation.ConversationRecord, 0, len(items))
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
	item *conversation.ConversationRecord,
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
	item *conversation.ConversationRecord,
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
		[]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive,
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
