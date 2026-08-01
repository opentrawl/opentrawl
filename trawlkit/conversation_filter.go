package trawlkit

import (
	"strings"

	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

func filterConversationsWithExactPersonFilterIdentifiers(
	items []*conversation.ConversationRecord,
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
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
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
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

func cleanExactPersonFilterIdentifiers(
	values []*person.ExactPersonFilterIdentifier,
) []*person.ExactPersonFilterIdentifier {
	seen := map[string]bool{}
	out := make([]*person.ExactPersonFilterIdentifier, 0, len(values))
	for _, value := range values {
		exactPersonFilterIdentifier := strings.TrimSpace(value.GetExactPersonFilterIdentifier())
		key := strings.ToLower(exactPersonFilterIdentifier)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, &person.ExactPersonFilterIdentifier{
			ExactPersonFilterIdentifier: exactPersonFilterIdentifier,
		})
	}
	return out
}

func exactPersonFilterIdentifierMatchesObservedIdentifier(
	resolvedIdentifier *person.ExactPersonFilterIdentifier,
	observedIdentifier *person.ExactPersonFilterIdentifier,
) bool {
	resolvedIdentifierText := strings.TrimSpace(resolvedIdentifier.GetExactPersonFilterIdentifier())
	observedIdentifierText := strings.TrimSpace(observedIdentifier.GetExactPersonFilterIdentifier())
	return resolvedIdentifierText != "" && strings.EqualFold(resolvedIdentifierText, observedIdentifierText)
}
