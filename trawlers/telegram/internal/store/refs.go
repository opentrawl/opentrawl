package store

import (
	"strconv"
	"strings"
)

const (
	MessageRefPrefix = "telegram:msg/"
	// ChatRefPrefix is the provider-native prefix for a conversation record.
	// The conversations command shows its short alias for messages
	// --conversation. Raw provider identifiers also remain valid.
	ChatRefPrefix = "telegram:chat/"
)

func MessageRef(sourcePK int64) string {
	return MessageRefPrefix + strconv.FormatInt(sourcePK, 10)
}

func ChatRef(jid string) string {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return ""
	}
	return ChatRefPrefix + jid
}

func ChatIDFromRef(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), ChatRefPrefix)
}
