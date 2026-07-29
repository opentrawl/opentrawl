package store

import (
	"encoding/json"
	"strings"
)

const providerNativeSystemMetadataMessageSQLPredicate = `(m.raw_type in (6, 10, 14) or lower(trim(coalesce(m.message_type, ''))) in ('system', 'group_event', 'reaction')) and json_valid(m.text)`

// MessageTextIsProviderNativeSystemMetadata reports whether WhatsApp stored a
// provider-owned structured system record in the message text field.
func MessageTextIsProviderNativeSystemMetadata(message Message) bool {
	if !messageIsProviderNativeSystemRecord(message) {
		return false
	}
	return json.Valid([]byte(strings.TrimSpace(message.Text)))
}

func messageIsProviderNativeSystemRecord(message Message) bool {
	switch message.RawType {
	case 6, 10, 14:
		return true
	}
	switch strings.ToLower(strings.TrimSpace(message.MessageType)) {
	case "system", "group_event", "reaction":
		return true
	default:
		return false
	}
}

func messageTextForFullTextSearchIndex(message Message) string {
	if MessageTextIsProviderNativeSystemMetadata(message) {
		return ""
	}
	return message.Text
}
