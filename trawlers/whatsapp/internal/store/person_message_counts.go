package store

import (
	"context"
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

func (s *Store) resolveExactPersonFilterParticipantKeys(ctx context.Context, identifiers []*person.ExactPersonFilterIdentifier) ([]string, error) {
	participantKeys := []string{}
	for _, identifier := range identifiers {
		identifierText := normalizeWhoIdentifier(identifier.GetExactPersonFilterIdentifier())
		if identifierText == "" {
			continue
		}
		if strings.EqualFold(identifierText, "me") {
			participantKeys = append(participantKeys, ownerWhoKey)
			continue
		}
		rows, err := s.db.QueryContext(ctx, `select 'jid:' || jid from contacts
where jid = ? collate nocase or phone = ? collate nocase or username = ? collate nocase
or '@' || username = ? collate nocase or lid = ? collate nocase or lid || '@lid' = ? collate nocase`,
			identifierText, identifierText, identifierText, identifierText, identifierText, identifierText)
		if err != nil {
			return nil, err
		}
		matchedContact := false
		for rows.Next() {
			var participantKey string
			if err := rows.Scan(&participantKey); err != nil {
				_ = rows.Close()
				return nil, err
			}
			matchedContact = true
			participantKeys = append(participantKeys, participantKey)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
		if !matchedContact {
			participantKeys = append(participantKeys, "jid:"+identifierText)
		}
	}
	return uniqueStrings(participantKeys), nil
}

func (s *Store) CountPersonMessages(ctx context.Context, identifiers []*person.ExactPersonFilterIdentifier) (uint64, error) {
	participantKeys, err := s.resolveExactPersonFilterParticipantKeys(ctx, identifiers)
	if err != nil || len(participantKeys) == 0 {
		return 0, err
	}
	query := whatsappMessagesInvolvingPersonQuery(whatsappParticipantKeysValuesQuery(participantKeys)) + " select count(distinct source_pk) from messages_involving_person"
	arguments := make([]any, len(participantKeys))
	for index, participantKey := range participantKeys {
		arguments[index] = participantKey
	}
	var messageCount uint64
	err = s.db.QueryRowContext(ctx, query, arguments...).Scan(&messageCount)
	return messageCount, err
}
