package imessage

import (
	"net/mail"
	"strings"
	"time"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const objectReplacementCharacter = "\uFFFC"

func humanParticipantDisplayIdentity(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" || strings.EqualFold(value, "them") {
		return ""
	}
	if phoneNumber, ok := humanParticipantPhoneContactPoint(value); ok {
		return render.HumanIdentity(phoneNumber)
	}
	if emailAddress, ok := humanParticipantEmailContactPoint(value); ok {
		return emailAddress
	}
	if isMachineGeneratedIMessageIdentifier(value) || isHandleLikeTitle(value) {
		return ""
	}
	return value
}

func searchSnippet(item archive.SearchResult) string {
	if snippet := strings.TrimSpace(item.Snippet); snippet != "" {
		return displayMessageText(snippet, item.HasAttachments)
	}
	return searchText(item)
}

func searchText(item archive.SearchResult) string {
	if item.Text != "" {
		return displayMessageText(item.Text, item.HasAttachments)
	}
	if item.Snippet != "" {
		return displayMessageText(item.Snippet, item.HasAttachments)
	}
	if item.HasAttachments {
		return "(attachment)"
	}
	return ""
}

func displayMessageText(text string, hasAttachments bool) string {
	if hasAttachments && strings.TrimSpace(strings.ReplaceAll(text, objectReplacementCharacter, "")) == "" {
		return "(attachment)"
	}
	return strings.ReplaceAll(text, objectReplacementCharacter, "[attachment]")
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func conversationDisplayName(chat archive.ChatSummary) string {
	title := strings.TrimSpace(chat.Title)
	conversationParticipantDisplayIdentityPreview := conversationParticipantDisplayIdentityPreview(chat)
	if chat.Kind != "group" && conversationParticipantDisplayIdentityPreview == "me" {
		return "me"
	}
	if title != "" && !isMachineGeneratedIMessageIdentifier(title) && !isHandleLikeTitle(title) {
		return title
	}
	if chat.Kind == "group" {
		if conversationParticipantDisplayIdentityPreview != "" {
			return "group with " + conversationParticipantDisplayIdentityPreview
		}
		return "group conversation"
	}
	if conversationParticipantDisplayIdentityPreview != "" {
		return conversationParticipantDisplayIdentityPreview
	}
	return "direct conversation"
}

func messageRecordConversationDisplayName(chat archive.ChatSummary) string {
	title := strings.TrimSpace(chat.Title)
	if title != "" && !isMachineGeneratedIMessageIdentifier(title) && !isHandleLikeTitle(title) {
		return title
	}
	if conversationParticipantDisplayIdentityPreview := conversationParticipantDisplayIdentityPreview(chat); conversationParticipantDisplayIdentityPreview != "" {
		return conversationParticipantDisplayIdentityPreview
	}
	return conversationDisplayName(chat)
}

// conversationListTitle returns only a stored subject that a person would use
// as a conversation title. Participants identify an untitled conversation.
func conversationListTitle(conversation archive.ChatSummary) string {
	title := strings.TrimSpace(conversation.Title)
	if title == "" || isMachineGeneratedIMessageIdentifier(title) || isHandleLikeTitle(title) {
		return ""
	}
	return title
}

func conversationParticipantDisplayIdentities(conversation archive.ChatSummary) []string {
	if len(conversation.ConversationParticipantIdentities) == 0 {
		return nil
	}
	displayIdentities := make([]string, 0, len(conversation.ConversationParticipantIdentities))
	for _, participantIdentity := range conversation.ConversationParticipantIdentities {
		if displayIdentity := humanParticipantDisplayIdentity(participantIdentity.PersonDisplayName); displayIdentity != "" {
			displayIdentities = append(displayIdentities, displayIdentity)
		}
	}
	return displayIdentities
}

func isMachineGeneratedIMessageIdentifier(title string) bool {
	title = strings.ToLower(strings.TrimSpace(title))
	if strings.HasPrefix(title, "urn:") {
		return true
	}
	providerNativeChatIdentifierParts := strings.Split(title, ";")
	if len(providerNativeChatIdentifierParts) >= 3 &&
		(providerNativeChatIdentifierParts[1] == "+" || providerNativeChatIdentifierParts[1] == "-") {
		return true
	}
	if len(title) >= 8 && strings.HasPrefix(title, "chat") && allRunes(title[4:], unicode.IsDigit) {
		return true
	}
	identifierWithoutHyphens := strings.ReplaceAll(title, "-", "")
	if len(identifierWithoutHyphens) >= 16 && allRunes(identifierWithoutHyphens, isHexRune) {
		return true
	}
	return false
}

func allRunes(value string, match func(rune) bool) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !match(r) {
			return false
		}
	}
	return true
}

func isHexRune(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')
}

func isHandleLikeTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	lowerTitle := strings.ToLower(title)
	if strings.Contains(title, "@") ||
		strings.HasPrefix(lowerTitle, "mailto:") ||
		strings.HasPrefix(lowerTitle, "tel:") {
		return true
	}
	return looksPhoneLikeTitle(title)
}

func humanParticipantPhoneContactPoint(value string) (string, bool) {
	phoneNumber := strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(phoneNumber), "tel:") {
		phoneNumber = strings.TrimSpace(phoneNumber[len("tel:"):])
	}
	if !looksPhoneLikeTitle(phoneNumber) {
		return "", false
	}
	digitCount := 0
	for _, character := range phoneNumber {
		if character >= '0' && character <= '9' {
			digitCount++
		}
	}
	if digitCount < 8 || digitCount > 15 {
		return "", false
	}
	return phoneNumber, true
}

func humanParticipantEmailContactPoint(value string) (string, bool) {
	emailAddress := strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(emailAddress), "mailto:") {
		emailAddress = strings.TrimSpace(emailAddress[len("mailto:"):])
	}
	parsedAddress, err := mail.ParseAddress(emailAddress)
	if err != nil || parsedAddress.Name != "" || !strings.EqualFold(parsedAddress.Address, emailAddress) {
		return "", false
	}
	return emailAddress, true
}

func looksPhoneLikeTitle(value string) bool {
	hasDigit := false
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+', r == ' ', r == '\t', r == '(', r == ')', r == '-', r == '.':
			continue
		default:
			return false
		}
	}
	return hasDigit
}

func conversationParticipantDisplayIdentityPreview(conversation archive.ChatSummary) string {
	conversationParticipantDisplayNames := conversationParticipantDisplayIdentities(conversation)
	if len(conversationParticipantDisplayNames) == 0 {
		return ""
	}
	knownConversationParticipantCount := uint64(len(conversationParticipantDisplayNames))
	if conversation.ParticipantCount > int64(knownConversationParticipantCount) {
		knownConversationParticipantCount = uint64(conversation.ParticipantCount)
	}
	return render.ConversationParticipantDisplayNamesPreviewForHumanOutput(
		conversationParticipantDisplayNames,
		knownConversationParticipantCount,
	)
}

func parseArchiveTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return t
}
