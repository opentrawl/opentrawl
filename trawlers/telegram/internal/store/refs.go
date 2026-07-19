package store

import (
	"strconv"
	"strings"
)

const (
	MessageRefPrefix = "telegram:msg/"
	// ChatRefPrefix names a chat the same way a message ref names a message:
	// the source-scoped handle a reader copies from the chats table into
	// messages --chat. The raw chat id keeps working; the prefix is stripped.
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
