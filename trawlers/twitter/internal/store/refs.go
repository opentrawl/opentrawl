package store

import (
	"errors"
	"strings"
)

const (
	TweetRefPrefix       = "twitter:tweet/"
	LegacyTweetRefPrefix = "birdcrawl:tweet/"
)

func TweetRef(id string) string {
	return TweetRefPrefix + strings.TrimSpace(id)
}

func ParseTweetRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	prefix := TweetRefPrefix
	if !strings.HasPrefix(ref, prefix) {
		if !strings.HasPrefix(ref, LegacyTweetRefPrefix) {
			return "", errors.New("invalid twitter tweet ref")
		}
		prefix = LegacyTweetRefPrefix
	}
	id := strings.TrimPrefix(ref, prefix)
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, " /\t\r\n") {
		return "", errors.New("invalid twitter tweet ref")
	}
	return id, nil
}
