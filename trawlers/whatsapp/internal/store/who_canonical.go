package store

import (
	"context"
	"strings"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func (s *Store) withCanonicalWhatsAppMessageDisplayNames(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	names, err := s.canonicalWhatsAppMessageDisplayNames(ctx)
	if err != nil {
		return nil, err
	}
	names.applyToMessages(messages)
	return messages, nil
}

func (n canonicalWhatsAppMessageDisplayNames) applyToMessages(messages []Message) {
	for i := range messages {
		messages[i].ChatName = normalizeWhoIdentity(messages[i].ChatName)
		messages[i].SenderName = normalizeWhoIdentity(messages[i].SenderName)
		if !humanWhoName(messages[i].ChatName) {
			if name := n.lookupCanonicalConversationParticipantName(messages[i].ChatJID); humanWhoName(name) {
				messages[i].ChatName = name
			}
		}
		if !messages[i].FromMe {
			if name := n.lookupCanonicalSenderName(messages[i]); name != "" {
				messages[i].SenderName = name
			}
		}
	}
	n.replaceNumericMentionsInMessages(messages)
}

func (s *Store) withCanonicalWhatsAppNumericMentionDisplayNames(ctx context.Context, messages []Message) ([]Message, error) {
	if len(messages) == 0 {
		return messages, nil
	}
	names, err := s.canonicalWhatsAppMessageDisplayNames(ctx)
	if err != nil {
		return nil, err
	}
	names.replaceNumericMentionsInMessages(messages)
	return messages, nil
}

func (n canonicalWhatsAppMessageDisplayNames) replaceNumericMentionsInMessages(messages []Message) {
	for i := range messages {
		messages[i].Text = n.replaceNumericMentionsInText(messages[i].Text)
		messages[i].Snippet = n.replaceNumericMentionsInText(messages[i].Snippet)
		for searchMatchIndex := range messages[i].SearchMatches {
			messages[i].SearchMatches[searchMatchIndex].Runs = n.replaceNumericMentionsInSearchTextRuns(
				messages[i].SearchMatches[searchMatchIndex].Runs,
			)
		}
	}
}

type canonicalWhatsAppMessageDisplayNames struct {
	canonicalSenderNamesByIdentityKey           canonicalSenderNames
	numericMentionHumanNamesByNumericIdentifier map[string]string
}

type canonicalSenderNames map[string]string

func (s *Store) canonicalWhatsAppMessageDisplayNames(ctx context.Context) (canonicalWhatsAppMessageDisplayNames, error) {
	records, err := s.whoCandidateRecords(ctx)
	if err != nil {
		return canonicalWhatsAppMessageDisplayNames{}, err
	}
	return canonicalWhatsAppMessageDisplayNamesFromWhoCandidateRecords(records), nil
}

func canonicalWhatsAppMessageDisplayNamesFromWhoCandidateRecords(records []whoCandidateRecord) canonicalWhatsAppMessageDisplayNames {
	names := canonicalWhatsAppMessageDisplayNames{
		canonicalSenderNamesByIdentityKey:           canonicalSenderNames{},
		numericMentionHumanNamesByNumericIdentifier: map[string]string{},
	}
	for _, record := range records {
		name := normalizeWhoIdentity(record.Who)
		if name == "" {
			continue
		}
		for _, key := range record.ParticipantKeys {
			names.canonicalSenderNamesByIdentityKey.addParticipantKey(key, name)
		}
		for _, identifier := range record.Identifiers {
			names.canonicalSenderNamesByIdentityKey.addIdentifier(identifier, name)
			names.addNumericMentionHumanName(identifier, name)
		}
	}
	return names
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

func (n canonicalWhatsAppMessageDisplayNames) addNumericMentionHumanName(identifier, humanName string) {
	numericIdentifier := canonicalWhatsAppNumericIdentifier(identifier)
	if numericIdentifier == "" || !humanWhoName(humanName) {
		return
	}
	existingHumanName, exists := n.numericMentionHumanNamesByNumericIdentifier[numericIdentifier]
	if !exists {
		n.numericMentionHumanNamesByNumericIdentifier[numericIdentifier] = humanName
		return
	}
	if existingHumanName != "" && strings.EqualFold(existingHumanName, humanName) {
		return
	}
	n.numericMentionHumanNamesByNumericIdentifier[numericIdentifier] = ""
}

func (n canonicalWhatsAppMessageDisplayNames) lookupCanonicalSenderName(message Message) string {
	return n.canonicalSenderNamesByIdentityKey.lookupMessage(message)
}

func (n canonicalWhatsAppMessageDisplayNames) lookupCanonicalConversationParticipantName(chatJID string) string {
	return n.canonicalSenderNamesByIdentityKey[canonicalSenderIdentifierKey(chatJID)]
}

func (n canonicalSenderNames) lookupMessage(message Message) string {
	for _, key := range canonicalSenderLookupKeys(message) {
		if name := n[key]; name != "" {
			return name
		}
	}
	return ""
}

func (n canonicalWhatsAppMessageDisplayNames) replaceNumericMentionsInText(value string) string {
	if value == "" {
		return ""
	}
	runs := n.replaceNumericMentionsInSearchTextRuns([]ckstore.FTS5TextRun{{Text: value}})
	var projectedText strings.Builder
	projectedText.Grow(len(value))
	for _, run := range runs {
		projectedText.WriteString(run.Text)
	}
	return projectedText.String()
}

func (n canonicalWhatsAppMessageDisplayNames) replaceNumericMentionsInSearchTextRuns(runs []ckstore.FTS5TextRun) []ckstore.FTS5TextRun {
	var textRunes []rune
	var matchedRunes []bool
	for _, run := range runs {
		for _, textRune := range run.Text {
			textRunes = append(textRunes, textRune)
			matchedRunes = append(matchedRunes, run.Matched)
		}
	}

	var projectedRuns []ckstore.FTS5TextRun
	for textRuneIndex := 0; textRuneIndex < len(textRunes); {
		numericIdentifierEnd := textRuneIndex + 1
		if textRunes[textRuneIndex] == '@' {
			for numericIdentifierEnd < len(textRunes) && textRunes[numericIdentifierEnd] >= '0' && textRunes[numericIdentifierEnd] <= '9' {
				numericIdentifierEnd++
			}
		}
		if numericIdentifierEnd-textRuneIndex-1 < 5 {
			projectedRuns = appendWhatsAppDisplayTextRun(projectedRuns, string(textRunes[textRuneIndex]), matchedRunes[textRuneIndex])
			textRuneIndex++
			continue
		}

		numericIdentifier := string(textRunes[textRuneIndex+1 : numericIdentifierEnd])
		displayMention := "@someone"
		if humanName := n.numericMentionHumanNamesByNumericIdentifier[numericIdentifier]; humanName != "" {
			displayMention = "@" + humanName
		}
		mentionMatched := false
		for _, matched := range matchedRunes[textRuneIndex:numericIdentifierEnd] {
			mentionMatched = mentionMatched || matched
		}
		projectedRuns = appendWhatsAppDisplayTextRun(projectedRuns, displayMention, mentionMatched)
		textRuneIndex = numericIdentifierEnd
	}
	return projectedRuns
}

func appendWhatsAppDisplayTextRun(runs []ckstore.FTS5TextRun, text string, matched bool) []ckstore.FTS5TextRun {
	if text == "" {
		return runs
	}
	if len(runs) > 0 && runs[len(runs)-1].Matched == matched {
		runs[len(runs)-1].Text += text
		return runs
	}
	return append(runs, ckstore.FTS5TextRun{Text: text, Matched: matched})
}

func canonicalWhatsAppNumericIdentifier(value string) string {
	value = strings.TrimPrefix(normalizeWhoIdentifier(value), "@")
	if separatorIndex := strings.IndexByte(value, '@'); separatorIndex >= 0 {
		value = value[:separatorIndex]
	}
	for _, identifierRune := range value {
		if identifierRune < '0' || identifierRune > '9' {
			return ""
		}
	}
	if len(value) < 5 {
		return ""
	}
	return value
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
