package twitter

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	"github.com/opentrawl/opentrawl/twitter/internal/store"
	twitteropen "github.com/opentrawl/opentrawl/twitter/proto/trawl/twitter/open"
)

type openValue struct {
	result        store.OpenResult
	ownerAuthorID string
}

func projectOpenRecord(value openValue) *twitteropen.OpenedTwitterPostRecord {
	result, ownerAuthorID := value.result, value.ownerAuthorID
	record := &twitteropen.OpenedTwitterPostRecord{
		CanonicalTwitterPostRecordReference:    store.TweetRef(result.Tweet.ID),
		OpenedTwitterPost:                      projectTwitterPost(result.Tweet, ownerAuthorID),
		AncestorTwitterPostsInOldestFirstOrder: make([]*twitteropen.OpenedTwitterPost, 0, len(result.Ancestors)),
		ReplyTwitterPostsInOldestFirstOrder:    make([]*twitteropen.OpenedTwitterPost, 0, len(result.Replies)),
		EarlierAncestorTwitterPostsAreOmitted:  result.AncestorsTruncated,
		LaterReplyTwitterPostsAreOmitted:       result.RepliesTruncated,
	}
	for _, ancestor := range result.Ancestors {
		if ancestor.Available {
			record.AncestorTwitterPostsInOldestFirstOrder = append(record.AncestorTwitterPostsInOldestFirstOrder, projectTwitterPost(ancestor.Tweet, ownerAuthorID))
			continue
		}
		record.AncestorTwitterPostsInOldestFirstOrder = append(record.AncestorTwitterPostsInOldestFirstOrder, &twitteropen.OpenedTwitterPost{
			CanonicalTwitterPostRecordReference: ancestor.Ref,
			TwitterPostText:                     ancestor.Text,
			TwitterPostIsUnavailable:            recordBool(true),
		})
	}
	for _, reply := range result.Replies {
		record.ReplyTwitterPostsInOldestFirstOrder = append(record.ReplyTwitterPostsInOldestFirstOrder, projectTwitterPost(reply, ownerAuthorID))
	}
	return record
}

func projectTwitterPost(value store.Tweet, ownerAuthorID string) *twitteropen.OpenedTwitterPost {
	record := &twitteropen.OpenedTwitterPost{
		CanonicalTwitterPostRecordReference: store.TweetRef(value.ID),
		TwitterPostText:                     value.Text,
	}
	setOptionalString(&record.TwitterPostCreatedRfc3339Time, formatOptionalTime(value.CreatedAt))
	setOptionalString(&record.TwitterPostAuthorDisplayName, humanName(store.DisplayName(value.AuthorName, value.AuthorHandle), value.AuthorID, ownerAuthorID))
	setOptionalString(&record.RepliedToTwitterPostRecordReference, canonicalTweetRef(value.InReplyToID))
	if value.LikeCount != 0 {
		record.TwitterPostLikeCount = recordInt64(value.LikeCount)
	}
	if value.RetweetCount != 0 {
		record.TwitterPostRepostCount = recordInt64(value.RetweetCount)
	}
	if value.ReplyCount != 0 {
		record.TwitterPostReplyCount = recordInt64(value.ReplyCount)
	}
	setOptionalString(&record.TwitterPostCountsObservedRfc3339Time, formatOptionalTime(value.MetricsFetchedAt))
	setOptionalString(&record.TwitterPostAvailabilityNote, retweetStubNoteForText(value.Text))
	setOptionalString(&record.TwitterConversationIdentifier, value.ConversationID)
	setOptionalString(&record.QuotedTwitterPostIdentifier, value.QuotedTweetID)
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
	title := strings.TrimSpace(record.OpenedTwitterPost.GetTwitterPostAuthorDisplayName())
	if strings.TrimSpace(value.result.Tweet.AuthorName) == "" && strings.TrimSpace(value.result.Tweet.AuthorHandle) == "" {
		title = ""
	}
	if title == "" {
		title = "Post"
	}
	fields := make([]*presentation.TrawlerSpecificCommandDetailPresentationField, 0, 5)
	if exactTime, err := time.Parse(time.RFC3339Nano, record.OpenedTwitterPost.GetTwitterPostCreatedRfc3339Time()); err == nil && !exactTime.IsZero() {
		fields = append(fields, twitterDetailExactTimeField("Time", exactTime))
	}
	if record.OpenedTwitterPost.TwitterPostLikeCount != nil {
		fields = append(fields, twitterDetailUnsignedCountField("Likes", *record.OpenedTwitterPost.TwitterPostLikeCount))
	}
	if record.OpenedTwitterPost.TwitterPostRepostCount != nil {
		fields = append(fields, twitterDetailUnsignedCountField("Reposts", *record.OpenedTwitterPost.TwitterPostRepostCount))
	}
	if record.OpenedTwitterPost.TwitterPostReplyCount != nil {
		fields = append(fields, twitterDetailUnsignedCountField("Replies", *record.OpenedTwitterPost.TwitterPostReplyCount))
	}
	if exactTime, err := time.Parse(time.RFC3339Nano, record.OpenedTwitterPost.GetTwitterPostCountsObservedRfc3339Time()); err == nil && !exactTime.IsZero() {
		fields = append(fields, twitterDetailExactTimeField("Counts as of", exactTime))
	}
	detail := &presentation.TrawlerSpecificCommandDetailPresentation{
		DetailDisplayName:    title,
		FieldsInDisplayOrder: fields,
	}
	if text := strings.TrimSpace(record.OpenedTwitterPost.TwitterPostText); text != "" {
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
