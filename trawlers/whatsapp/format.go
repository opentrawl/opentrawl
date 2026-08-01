package whatsapp

import (
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
)

const (
	messageRefPrefix          = store.MessageRefPrefix
	openWindowEachSide        = 10
	unknownPrivacyParticipant = "unknown participant (privacy id)"
)

type whatsappMessageMediaHumanProjection struct {
	messageMediaContentKind message.MessageMediaContentKind
	messageMediaHumanLabel  string
	messageMediaTitle       string
	messageMediaByteCount   int64
}

func messageRef(message store.Message) string {
	return messageRefPrefix + message.MessageID
}

func messageWhere(message store.Message) string {
	if name := humanDisplayName(message.ChatName); name != "" {
		return name
	}
	if !message.FromMe {
		if name := humanDisplayName(message.SenderName); name != "" {
			return name
		}
	}
	return "WhatsApp conversation"
}

func messageSnippet(message store.Message) string {
	return outputField(messageText(message))
}

func messageText(message store.Message) string {
	if text := outputField(message.Text); text != "" && !messageTextIsKnownProviderMetadata(message, text) {
		return text
	}
	if messageMediaHumanProjection := projectWhatsAppMessageMediaForHumanPresentation(message); messageMediaHumanProjection != nil {
		return "[" + messageMediaHumanProjection.messageMediaHumanLabel + "]"
	}
	return readableMessageType(message)
}

func projectWhatsAppMessageMediaForHumanPresentation(whatsappMessage store.Message) *whatsappMessageMediaHumanProjection {
	messageMediaTitle := safeMediaTitle(whatsappMessage)
	providerMediaType := normalizeMessageKind(whatsappMessage.MediaType)
	if strings.EqualFold(messageMediaTitle, providerMediaType) {
		messageMediaTitle = ""
	}
	if !messageCarriesMedia(whatsappMessage) && providerMediaType == "" && messageMediaTitle == "" {
		return nil
	}
	if providerMediaType == "" {
		providerMediaType = messageKind(whatsappMessage)
	}
	messageMediaHumanProjection := &whatsappMessageMediaHumanProjection{
		messageMediaHumanLabel: "Media",
		messageMediaTitle:      messageMediaTitle,
		messageMediaByteCount:  whatsappMessage.MediaSize,
	}
	switch providerMediaType {
	case "image":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_IMAGE
		messageMediaHumanProjection.messageMediaHumanLabel = "Image"
	case "video":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_VIDEO
		messageMediaHumanProjection.messageMediaHumanLabel = "Video"
	case "audio":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_AUDIO
		messageMediaHumanProjection.messageMediaHumanLabel = "Audio"
	case "document":
		messageMediaHumanProjection.messageMediaContentKind = message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE
		messageMediaHumanProjection.messageMediaHumanLabel = "File"
	case "gif":
		messageMediaHumanProjection.messageMediaHumanLabel = "GIF"
	case "sticker":
		messageMediaHumanProjection.messageMediaHumanLabel = "Sticker"
	case "link":
		messageMediaHumanProjection.messageMediaHumanLabel = "Link"
	}
	return messageMediaHumanProjection
}

func messageTextIsKnownProviderMetadata(message store.Message, text string) bool {
	if store.MessageTextIsProviderNativeSystemMetadata(message) {
		return true
	}
	if providerMediaType := outputField(message.MediaType); providerMediaType != "" && strings.EqualFold(text, providerMediaType) {
		return true
	}
	mediaTitle := outputField(message.MediaTitle)
	if mediaTitle != "" && strings.EqualFold(text, mediaTitle) && safeMediaLabel(mediaTitle) == "" {
		return true
	}
	switch messageKind(message) {
	case "system", "group_event", "reaction":
		return containsProviderInternalMetadataToken(text)
	default:
		return false
	}
}

func safeMediaTitle(message store.Message) string {
	if title := safeMediaLabel(message.MediaTitle); title != "" {
		return title
	}
	return safeMediaFilename(message.MediaPath)
}

func safeMediaFilename(mediaPath string) string {
	mediaPath = strings.TrimSpace(mediaPath)
	if mediaPath == "" {
		return ""
	}
	return safeMediaLabel(filepath.Base(mediaPath))
}

func safeMediaLabel(value string) string {
	value = outputField(value)
	if value == "" || value == "." || value == "/" || value == `\` {
		return ""
	}
	if containsProviderInternalMetadataToken(value) {
		return ""
	}
	return value
}

func readableMessageType(message store.Message) string {
	kind := messageKind(message)
	if kind == "" && (message.RawType != 0 || message.MessageType != "" || message.MediaType != "") {
		return "[unsupported message]"
	}
	if kind == "" {
		return ""
	}
	return "[" + strings.ReplaceAll(kind, "_", " ") + "]"
}

func messageKind(message store.Message) string {
	for _, kind := range []string{message.MediaType, message.MessageType} {
		kind = normalizeMessageKind(kind)
		if kind != "" {
			return kind
		}
	}
	return knownMessageType(message.RawType)
}

func normalizeMessageKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" || numericInternalKind(kind) {
		return ""
	}
	return kind
}

func knownMessageType(raw int) string {
	switch raw {
	case 0:
		return "text"
	case 1:
		return "image"
	case 2:
		return "video"
	case 3:
		return "audio"
	case 4:
		return "location"
	case 5:
		return "contact"
	case 6:
		return "system"
	case 7:
		return "link"
	case 8:
		return "document"
	case 10:
		return "group_event"
	case 11:
		return "gif"
	case 14:
		return "reaction"
	case 15:
		return "sticker"
	case 59:
		return "status_update"
	default:
		return ""
	}
}

func numericInternalKind(kind string) bool {
	for _, prefix := range []string{"type_", "status_"} {
		if suffix, ok := strings.CutPrefix(kind, prefix); ok {
			return allDigits(suffix)
		}
	}
	return false
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func containsProviderInternalMetadataToken(value string) bool {
	for _, field := range strings.Fields(value) {
		for _, candidate := range []string{field, filepath.Base(field)} {
			if providerInternalMetadataToken(candidate) {
				return true
			}
			stem := strings.TrimSuffix(candidate, filepath.Ext(candidate))
			if providerInternalMetadataToken(stem) {
				return true
			}
		}
	}
	return false
}

func messageCarriesMedia(message store.Message) bool {
	switch messageKind(message) {
	case "image", "video", "audio", "document", "gif", "sticker":
		return true
	}
	return message.MediaPath != "" || message.MediaURL != "" || message.MediaSize > 0
}

func providerInternalMetadataToken(value string) bool {
	value = strings.Trim(value, `"'.,;:()[]{}<>`)
	if value == "" {
		return false
	}
	if whatsappProviderIdentifier(value) || uuidLikeProviderToken(value) {
		return true
	}
	return opaqueProviderToken(value)
}

func whatsappProviderIdentifier(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, suffix := range []string{"@lid", "@s.whatsapp.net", "@g.us", "@broadcast", "@newsletter"} {
		if strings.HasSuffix(value, suffix) {
			return true
		}
	}
	return false
}

func uuidLikeProviderToken(value string) bool {
	parts := strings.Split(value, "-")
	if len(parts) != 5 {
		return false
	}
	expectedLengths := [...]int{8, 4, 4, 4, 12}
	for index, part := range parts {
		if len(part) != expectedLengths[index] || !allHexadecimal(part) {
			return false
		}
	}
	return true
}

func opaqueProviderToken(value string) bool {
	if len(value) >= 16 && allHexadecimal(value) {
		return true
	}
	if len(value) < 20 {
		return false
	}
	allHex := true
	allBase64 := true
	hasUppercase := false
	hasLowercase := false
	hasDigit := false
	hasStrongBase64Mark := false
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			allHex = false
		}
		switch {
		case r >= 'A' && r <= 'Z':
			hasUppercase = true
		case r >= 'a' && r <= 'z':
			hasLowercase = true
		case r >= '0' && r <= '9':
			hasDigit = true
		case r == '+', r == '/', r == '_', r == '=':
			hasStrongBase64Mark = true
		case r == '-':
		default:
			allBase64 = false
		}
	}
	if allHex {
		return true
	}
	if !allBase64 {
		return false
	}
	if hasUppercase && !hasLowercase {
		return true
	}
	if hasStrongBase64Mark && (hasUppercase || hasLowercase) {
		return true
	}
	return len(value) >= 40 && (hasDigit || len(value)%4 == 0)
}

func allHexadecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

func humanDisplayName(name string) string {
	name = outputField(name)
	if strings.EqualFold(name, "me") {
		return "me"
	}
	if !store.HumanWhoName(name) || containsProviderInternalMetadataToken(name) {
		return ""
	}
	return name
}

func privacyID(value string) bool {
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(value)), "@lid")
}

func humanParticipantLabel(value string) string {
	value = outputField(value)
	if privacyID(value) {
		return unknownPrivacyParticipant
	}
	return value
}

func resolvedParticipantNames(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = humanDisplayName(value)
		if value == "" || strings.EqualFold(value, "me") {
			continue
		}
		normalizedValue := strings.ToLower(value)
		if _, alreadyIncluded := seen[normalizedValue]; alreadyIncluded {
			continue
		}
		seen[normalizedValue] = struct{}{}
		out = append(out, value)
	}
	return out
}

func conversationParticipantIdentitiesObservedByTrawlerArchive(
	participantIdentities []store.ConversationParticipantIdentity,
) []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive {
	projectedParticipantIdentities := make(
		[]*conversation.ConversationParticipantIdentityObservedByTrawlerArchive,
		0,
		len(participantIdentities),
	)
	for _, participantIdentity := range participantIdentities {
		personDisplayName := humanDisplayName(participantIdentity.PersonDisplayName)
		if strings.EqualFold(personDisplayName, "me") {
			continue
		}
		exactPersonFilterIdentifiers := make(
			[]*person.ExactPersonFilterIdentifier,
			0,
			len(participantIdentity.ExactPersonFilterIdentifiersObservedByTrawlerArchive),
		)
		for _, exactPersonFilterIdentifier := range participantIdentity.ExactPersonFilterIdentifiersObservedByTrawlerArchive {
			exactPersonFilterIdentifierText := strings.TrimSpace(exactPersonFilterIdentifier.GetExactPersonFilterIdentifier())
			if exactPersonFilterIdentifierText != "" {
				exactPersonFilterIdentifiers = append(
					exactPersonFilterIdentifiers,
					&person.ExactPersonFilterIdentifier{ExactPersonFilterIdentifier: exactPersonFilterIdentifierText},
				)
			}
		}
		if len(exactPersonFilterIdentifiers) == 0 {
			continue
		}
		projectedParticipantIdentities = append(
			projectedParticipantIdentities,
			&conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
				PersonDisplayName: personDisplayName,
				ExactPersonFilterIdentifiersObservedByTrawlerArchive: exactPersonFilterIdentifiers,
			},
		)
	}
	return projectedParticipantIdentities
}

func conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
	participantIdentities []store.ConversationParticipantIdentity,
) []string {
	displayNames := make([]string, 0, len(participantIdentities))
	for _, participantIdentity := range conversationParticipantIdentitiesObservedByTrawlerArchive(
		participantIdentities,
	) {
		if displayName := participantIdentity.GetPersonDisplayName(); displayName != "" {
			displayNames = append(displayNames, displayName)
		}
	}
	return displayNames
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
