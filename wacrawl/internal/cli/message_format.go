package cli

import (
	"path/filepath"
	"strings"

	"github.com/openclaw/wacrawl/internal/store"
)

func messageRef(message store.Message) string {
	return messageRefPrefix + message.MessageID
}

func messageRefs(messages []store.Message) []string {
	refs := make([]string, 0, len(messages))
	for _, message := range messages {
		refs = append(refs, messageRef(message))
	}
	return refs
}

func messageWho(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	if name := humanDisplayName(message.SenderName); name != "" {
		return name
	}
	if identifier := senderIdentifier(message.SenderName); identifier != "" {
		return identifier
	}
	if identifier := senderIdentifier(message.SenderJID); identifier != "" {
		return identifier
	}
	return "Unknown sender"
}

func messageWhere(message store.Message) string {
	if name := humanDisplayName(message.ChatName); name != "" {
		return name
	}
	return "Unknown chat"
}

func messageSnippet(message store.Message) string {
	if snippet := outputField(message.Snippet); snippet != "" && !containsOpaqueMediaReference(message, snippet) {
		return snippet
	}
	return outputField(messageText(message))
}

func messageText(message store.Message) string {
	if text := outputField(message.Text); text != "" && !containsOpaqueMediaReference(message, text) {
		return text
	}
	if !messageCarriesMedia(message) {
		if title := safeMediaTitle(message); title != "" {
			return title
		}
	}
	return readableMessageType(message)
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
	for _, field := range strings.Fields(value) {
		if opaqueMediaToken(field) {
			return ""
		}
		stem := strings.TrimSuffix(field, filepath.Ext(field))
		if opaqueMediaToken(stem) {
			return ""
		}
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

func containsOpaqueMediaReference(message store.Message, value string) bool {
	if !messageCarriesMedia(message) {
		return false
	}
	for _, field := range strings.Fields(value) {
		if opaqueMediaToken(field) {
			return true
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

func opaqueMediaToken(value string) bool {
	value = strings.Trim(value, `"'.,;:()[]{}<>`)
	if len(value) < 40 {
		return false
	}
	allHex := true
	allBase64 := true
	hasBase64Mark := false
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			allHex = false
		}
		switch {
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '+', r == '/', r == '_', r == '-', r == '=':
			hasBase64Mark = true
		default:
			allBase64 = false
		}
	}
	return allHex || (allBase64 && (hasBase64Mark || len(value)%4 == 0))
}

func humanDisplayName(name string) string {
	name = outputField(name)
	if strings.EqualFold(name, "me") {
		return "me"
	}
	if !store.HumanWhoName(name) {
		return ""
	}
	return name
}

func senderIdentifier(value string) string {
	value = outputField(value)
	if value == "" {
		return ""
	}
	if looksLikePhone(value) || strings.Contains(value, "@") {
		return value
	}
	return ""
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
