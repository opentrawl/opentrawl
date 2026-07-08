package archive

import "strings"

const (
	MessageRefPrefix       = "imessage:msg/"
	LegacyMessageRefPrefix = "imsgcrawl:msg/"
)

func MessageRef(messageID string) string {
	return MessageRefPrefix + strings.TrimSpace(messageID)
}
