package store

import (
	"errors"
	"strings"
)

const (
	TweetRefPrefix          = "twitter:tweet/"
	DMMessageRefPrefix      = "twitter:dm/"
	DMConversationRefPrefix = "twitter:dm_conversation/"
)

func TweetRef(id string) string {
	return TweetRefPrefix + strings.TrimSpace(id)
}

func ParseTweetRef(ref string) (string, error) {
	return parsePrefixedTwitterRef(ref, TweetRefPrefix, "invalid twitter tweet ref")
}

func DMMessageRef(id string) string {
	return DMMessageRefPrefix + strings.TrimSpace(id)
}

func ParseDMMessageRef(ref string) (string, error) {
	return parsePrefixedTwitterRef(ref, DMMessageRefPrefix, "invalid twitter direct message ref")
}

func DMConversationRef(id string) string {
	return DMConversationRefPrefix + strings.TrimSpace(id)
}

func ParseDMConversationRef(ref string) (string, error) {
	return parsePrefixedTwitterRef(ref, DMConversationRefPrefix, "invalid twitter direct message conversation ref")
}

func parsePrefixedTwitterRef(ref, prefix, errMessage string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return "", errors.New(errMessage)
	}
	id := strings.TrimPrefix(ref, prefix)
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, " /\t\r\n") {
		return "", errors.New(errMessage)
	}
	return id, nil
}
