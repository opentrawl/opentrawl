package store

import (
	"context"
	"strings"
)

func (s *Store) withCanonicalSenderNames(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	names, err := s.canonicalSenderNames(ctx)
	if err != nil {
		return nil, err
	}
	for i := range messages {
		if messages[i].FromMe {
			continue
		}
		if name := names.lookupMessage(messages[i]); name != "" {
			messages[i].SenderName = name
		}
	}
	return messages, nil
}

type canonicalSenderNames map[string]string

func (s *Store) canonicalSenderNames(ctx context.Context) (canonicalSenderNames, error) {
	records, err := s.whoCandidateRecordsWithoutNameMerge(ctx)
	if err != nil {
		return nil, err
	}
	names := canonicalSenderNames{}
	for _, record := range records {
		name := normalizeWhoIdentity(record.Who)
		if name == "" {
			continue
		}
		for _, key := range record.ParticipantKeys {
			names.addParticipantKey(key, name)
		}
		for _, identifier := range record.Identifiers {
			names.addIdentifier(identifier, name)
		}
	}
	return names, nil
}

func (n canonicalSenderNames) addParticipantKey(value, name string) {
	key := canonicalSenderParticipantKey(value)
	if key == "" || n[key] != "" {
		return
	}
	n[key] = name
}

func (n canonicalSenderNames) addIdentifier(value, name string) {
	key := canonicalSenderIdentifierKey(value)
	if key == "" || n[key] != "" {
		return
	}
	n[key] = name
}

func (n canonicalSenderNames) lookupMessage(message Message) string {
	for _, key := range canonicalSenderLookupKeys(message) {
		if name := n[key]; name != "" {
			return name
		}
	}
	return ""
}

func canonicalSenderLookupKeys(message Message) []string {
	var keys []string
	if senderJID := normalizeWhoIdentifier(message.SenderJID); senderJID != "" {
		keys = append(keys, canonicalSenderIdentifierKey(senderJID))
		keys = append(keys, canonicalSenderParticipantKey("jid:"+senderJID))
	}
	if senderName := normalizeWhoIdentity(message.SenderName); senderName != "" {
		keys = append(keys, canonicalSenderParticipantKey("sender:"+senderName))
	}
	return keys
}

func canonicalSenderParticipantKey(value string) string {
	value = normalizeWhoIdentity(value)
	if value == "" {
		return ""
	}
	return "participant:" + strings.ToLower(value)
}

func canonicalSenderIdentifierKey(value string) string {
	value = normalizeWhoIdentifier(value)
	if value == "" {
		return ""
	}
	return "identifier:" + strings.ToLower(value)
}
