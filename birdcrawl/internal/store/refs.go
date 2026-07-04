package store

import (
	"errors"
	"strings"
)

const TweetRefPrefix = "birdcrawl:tweet/"

func TweetRef(id string) string {
	return TweetRefPrefix + strings.TrimSpace(id)
}

func ParseTweetRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, TweetRefPrefix) {
		return "", errors.New("invalid birdcrawl tweet ref")
	}
	id := strings.TrimPrefix(ref, TweetRefPrefix)
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, " /\t\r\n") {
		return "", errors.New("invalid birdcrawl tweet ref")
	}
	return id, nil
}
