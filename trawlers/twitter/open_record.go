package birdcrawl

import (
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	twitteropenv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/twitter/open/v1"
)

func projectOpenRecord(value store.OpenResult, aliases map[string]string, ownerAuthorID string) *twitteropenv1.TwitterRecord {
	_ = aliases
	record := &twitteropenv1.TwitterRecord{
		Ref:                store.TweetRef(value.Tweet.ID),
		Tweet:              projectTweet(value.Tweet, ownerAuthorID),
		Ancestors:          make([]*twitteropenv1.Tweet, 0, len(value.Ancestors)),
		Replies:            make([]*twitteropenv1.Tweet, 0, len(value.Replies)),
		AncestorsTruncated: value.AncestorsTruncated,
		RepliesTruncated:   value.RepliesTruncated,
	}
	for _, ancestor := range value.Ancestors {
		if ancestor.Available {
			record.Ancestors = append(record.Ancestors, projectTweet(ancestor.Tweet, ownerAuthorID))
			continue
		}
		record.Ancestors = append(record.Ancestors, &twitteropenv1.Tweet{
			Ref:         ancestor.Ref,
			Text:        ancestor.Text,
			Unavailable: recordBool(true),
		})
	}
	for _, reply := range value.Replies {
		record.Replies = append(record.Replies, projectTweet(reply, ownerAuthorID))
	}
	return record
}

func projectTweet(value store.Tweet, ownerAuthorID string) *twitteropenv1.Tweet {
	record := &twitteropenv1.Tweet{Ref: store.TweetRef(value.ID), Text: value.Text}
	setOptionalString(&record.Time, formatOptionalTime(value.CreatedAt))
	setOptionalString(&record.Who, humanName(store.DisplayName(value.AuthorName, value.AuthorHandle), value.AuthorID, ownerAuthorID))
	setOptionalString(&record.InReplyTo, canonicalTweetRef(value.InReplyToID))
	if value.LikeCount != 0 {
		record.LikeCount = recordInt64(value.LikeCount)
	}
	if value.RetweetCount != 0 {
		record.RetweetCount = recordInt64(value.RetweetCount)
	}
	if value.ReplyCount != 0 {
		record.ReplyCount = recordInt64(value.ReplyCount)
	}
	setOptionalString(&record.CountsAsOf, formatOptionalTime(value.MetricsFetchedAt))
	setOptionalString(&record.Note, retweetStubNoteForText(value.Text))
	setOptionalString(&record.ConversationId, value.ConversationID)
	setOptionalString(&record.QuotedTweetId, value.QuotedTweetID)
	return record
}

func canonicalTweetRef(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return store.TweetRef(value)
	}
	return ""
}

func setOptionalString(target **string, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*target = &value
	}
}

func recordInt64(value int64) *int64 { return &value }
func recordBool(value bool) *bool    { return &value }
