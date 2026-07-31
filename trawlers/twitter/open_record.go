package twitter

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	twitteropen "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/source/twitter/open"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
)

type openValue struct {
	result        store.OpenResult
	ownerAuthorID string
}

func projectOpenRecord(value openValue) *twitteropen.TwitterRecord {
	result, ownerAuthorID := value.result, value.ownerAuthorID
	record := &twitteropen.TwitterRecord{
		Ref:                store.TweetRef(result.Tweet.ID),
		Tweet:              projectTweet(result.Tweet, ownerAuthorID),
		Ancestors:          make([]*twitteropen.Tweet, 0, len(result.Ancestors)),
		Replies:            make([]*twitteropen.Tweet, 0, len(result.Replies)),
		AncestorsTruncated: result.AncestorsTruncated,
		RepliesTruncated:   result.RepliesTruncated,
	}
	for _, ancestor := range result.Ancestors {
		if ancestor.Available {
			record.Ancestors = append(record.Ancestors, projectTweet(ancestor.Tweet, ownerAuthorID))
			continue
		}
		record.Ancestors = append(record.Ancestors, &twitteropen.Tweet{
			Ref:         ancestor.Ref,
			Text:        ancestor.Text,
			Unavailable: recordBool(true),
		})
	}
	for _, reply := range result.Replies {
		record.Replies = append(record.Replies, projectTweet(reply, ownerAuthorID))
	}
	return record
}

func projectTweet(value store.Tweet, ownerAuthorID string) *twitteropen.Tweet {
	record := &twitteropen.Tweet{Ref: store.TweetRef(value.ID), Text: value.Text}
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

func projectOpenDetailPresentation(value openValue) *presentation.TrawlerSpecificCommandDetailPresentation {
	record := projectOpenRecord(value)
	title := strings.TrimSpace(record.Tweet.GetWho())
	if strings.TrimSpace(value.result.Tweet.AuthorName) == "" && strings.TrimSpace(value.result.Tweet.AuthorHandle) == "" {
		title = ""
	}
	if title == "" {
		title = "Post"
	}
	fields := make([]*presentation.TrawlerSpecificCommandDetailPresentationField, 0, 5)
	if exactTime, err := time.Parse(time.RFC3339Nano, record.Tweet.GetTime()); err == nil && !exactTime.IsZero() {
		fields = append(fields, twitterDetailExactTimeField("Time", exactTime))
	}
	if record.Tweet.LikeCount != nil {
		fields = append(fields, twitterDetailUnsignedCountField("Likes", *record.Tweet.LikeCount))
	}
	if record.Tweet.RetweetCount != nil {
		fields = append(fields, twitterDetailUnsignedCountField("Reposts", *record.Tweet.RetweetCount))
	}
	if record.Tweet.ReplyCount != nil {
		fields = append(fields, twitterDetailUnsignedCountField("Replies", *record.Tweet.ReplyCount))
	}
	if exactTime, err := time.Parse(time.RFC3339Nano, record.Tweet.GetCountsAsOf()); err == nil && !exactTime.IsZero() {
		fields = append(fields, twitterDetailExactTimeField("Counts as of", exactTime))
	}
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:    title,
		FieldsInDisplayOrder: fields,
	}
	if text := strings.TrimSpace(record.Tweet.Text); text != "" {
		detail.Body = &presentation.TrawlerSpecificCommandDetailPresentation_BodyText{BodyText: text}
		detail.BodyAnchor = trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID)
	} else {
		detail.DetailDisplayNameAnchor = trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID)
	}
	return detail
}

func twitterDetailExactTimeField(
	fieldDisplayName string,
	exactTime time.Time,
) *presentation.TrawlerSpecificCommandDetailPresentationField {
	return &presentation.TrawlerSpecificCommandDetailPresentationField{
		FieldDisplayName: fieldDisplayName,
		FieldValue:       twitterPresentationExactTimeValue(exactTime),
	}
}
