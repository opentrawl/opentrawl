package birdcrawl

import (
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
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

func projectOpenPresentation(value store.OpenResult, aliases map[string]string, ownerAuthorID string) *presentationv1.PresentationDocument {
	record := projectOpenRecord(value, aliases, ownerAuthorID)
	title := strings.TrimSpace(record.Tweet.GetWho())
	if strings.TrimSpace(value.Tweet.AuthorName) == "" && strings.TrimSpace(value.Tweet.AuthorHandle) == "" {
		title = ""
	}
	if title == "" {
		title = "Post"
	}
	fields := []*presentationv1.Field{{Label: "Ref", Display: record.Ref}}
	appendPresentationField(&fields, "Time", record.Tweet.GetTime())
	if record.Tweet.LikeCount != nil {
		fields = append(fields, &presentationv1.Field{Label: "Likes", Display: strconv.FormatInt(*record.Tweet.LikeCount, 10)})
	}
	if record.Tweet.RetweetCount != nil {
		fields = append(fields, &presentationv1.Field{Label: "Reposts", Display: strconv.FormatInt(*record.Tweet.RetweetCount, 10)})
	}
	if record.Tweet.ReplyCount != nil {
		fields = append(fields, &presentationv1.Field{Label: "Replies", Display: strconv.FormatInt(*record.Tweet.ReplyCount, 10)})
	}
	appendPresentationField(&fields, "Counts as of", record.Tweet.GetCountsAsOf())
	blocks := []*presentationv1.Block{{Content: &presentationv1.Block_Fields{Fields: &presentationv1.FieldGroup{Fields: fields}}}}
	if text := strings.TrimSpace(record.Tweet.Text); text != "" {
		blocks = append(blocks, &presentationv1.Block{Content: &presentationv1.Block_Prose{Prose: &presentationv1.Prose{Text: text}}})
	}
	blocks = append(blocks,
		&presentationv1.Block{Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: "Ancestors"}}},
		presentationTweetTable(record.Ancestors),
		&presentationv1.Block{Content: &presentationv1.Block_Heading{Heading: &presentationv1.Heading{Text: "Replies"}}},
		presentationTweetTable(record.Replies),
	)
	document := &presentationv1.PresentationDocument{Title: title, Blocks: blocks}
	if record.AncestorsTruncated {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Earlier conversation context is truncated."})
	}
	if record.RepliesTruncated {
		document.Facts = append(document.Facts, &presentationv1.Fact{Kind: presentationv1.Fact_KIND_TRUNCATION, Message: "Replies are truncated."})
	}
	return document
}

func appendPresentationField(fields *[]*presentationv1.Field, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		*fields = append(*fields, &presentationv1.Field{Label: label, Display: value})
	}
}

func presentationTweetTable(tweets []*twitteropenv1.Tweet) *presentationv1.Block {
	rows := make([]*presentationv1.Row, 0, len(tweets))
	for _, tweet := range tweets {
		rows = append(rows, &presentationv1.Row{Role: presentationv1.Row_ROLE_NORMAL, Cells: []*presentationv1.Cell{{Display: tweet.GetTime()}, {Display: tweet.GetWho()}, {Display: tweet.Text}}})
	}
	return &presentationv1.Block{Content: &presentationv1.Block_Table{Table: &presentationv1.Table{Columns: []string{"Time", "From", "Text"}, Rows: rows}}}
}
