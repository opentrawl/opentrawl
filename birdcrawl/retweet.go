package birdcrawl

import "strings"

const retweetStubNote = "X archives retweets as a truncated stub; open the original on x.com."

func retweetStubNoteForText(text string) string {
	if strings.HasPrefix(text, "RT @") {
		return retweetStubNote
	}
	return ""
}
