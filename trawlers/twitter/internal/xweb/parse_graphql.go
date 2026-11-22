package xweb

import (
	"encoding/json"
	"html"
	"strconv"
	"strings"
	"time"
)

const legacyTwitterTimeLayout = "Mon Jan 02 15:04:05 -0700 2006"

// WebTweet represents one normalized tweet decoded from an X web GraphQL
// timeline response.
type WebTweet struct {
	ID             string
	CreatedAt      time.Time
	AuthorID       string
	AuthorHandle   string
	AuthorName     string
	Text           string
	InReplyToID    string
	ConversationID string
	QuotedTweetID  string
	LikeCount      int64
	RetweetCount   int64
	ReplyCount     int64
	ViewCount      int64
	QuoteCount     int64
	BookmarkCount  int64
	HasMedia       bool
	RawJSON        string
}

// WebProfile represents one X user profile observed in a tweet or DM payload.
type WebProfile struct {
	AuthorID    string
	Handle      string
	DisplayName string
}

// WebTimelinePage holds one page of tweets returned by an X web GraphQL
// timeline endpoint along with its pagination cursor and rate-limit snapshot.
type WebTimelinePage struct {
	Tweets     []WebTweet
	Profiles   []WebProfile
	NextCursor string
	RateLimit  RateLimitObservation
}

type graphqlTimelineInstruction struct {
	Type    string                 `json:"type"`
	Entries []graphqlTimelineEntry `json:"entries"`
	Entry   *graphqlTimelineEntry  `json:"entry"`
}

type graphqlTimelineEntry struct {
	EntryID string                      `json:"entryId"`
	Content graphqlTimelineEntryContent `json:"content"`
}

type graphqlTimelineEntryContent struct {
	EntryType   string                           `json:"entryType"`
	CursorType  string                           `json:"cursorType"`
	Value       string                           `json:"value"`
	ItemContent *graphqlTimelineItemContent      `json:"itemContent"`
	Item        *graphqlTimelineNestedItemHolder `json:"item"`
	Items       []graphqlTimelineModuleItem      `json:"items"`
}

type graphqlTimelineModuleItem struct {
	Item        *graphqlTimelineNestedItemHolder `json:"item"`
	ItemContent *graphqlTimelineItemContent      `json:"itemContent"`
	Content     *graphqlTimelineNestedItemHolder `json:"content"`
}

type graphqlTimelineNestedItemHolder struct {
	ItemContent *graphqlTimelineItemContent `json:"itemContent"`
}

type graphqlTimelineItemContent struct {
	TweetResults *graphqlTweetResultsHolder `json:"tweet_results"`
}

type graphqlTweetResultsHolder struct {
	Result json.RawMessage `json:"result"`
}

type rawGraphQLTweetResult struct {
	TypeName string          `json:"__typename"`
	RestID   string          `json:"rest_id"`
	Tweet    json.RawMessage `json:"tweet"`
	Core     *struct {
		UserResults *struct {
			Result *rawGraphQLUserResult `json:"result"`
		} `json:"user_results"`
	} `json:"core"`
	Views *struct {
		Count string `json:"count"`
	} `json:"views"`
	NoteTweet *struct {
		NoteTweetResults *struct {
			Result *struct {
				Text string `json:"text"`
			} `json:"result"`
		} `json:"note_tweet_results"`
	} `json:"note_tweet"`
	Article *struct {
		Title          string `json:"title"`
		PlainText      string `json:"plain_text"`
		PreviewText    string `json:"preview_text"`
		ArticleResults *struct {
			Result *struct {
				Title       string `json:"title"`
				PlainText   string `json:"plain_text"`
				PreviewText string `json:"preview_text"`
			} `json:"result"`
		} `json:"article_results"`
	} `json:"article"`
	QuotedStatusResult *struct {
		Result json.RawMessage `json:"result"`
	} `json:"quoted_status_result"`
	Legacy *rawGraphQLTweetLegacy `json:"legacy"`
}

type rawGraphQLUserResult struct {
	TypeName string                `json:"__typename"`
	RestID   string                `json:"rest_id"`
	User     *rawGraphQLUserResult `json:"user"`
	Core     *struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
	} `json:"core"`
	Legacy *struct {
		ScreenName string `json:"screen_name"`
		Name       string `json:"name"`
	} `json:"legacy"`
}

type rawGraphQLTweetLegacy struct {
	IDStr             string              `json:"id_str"`
	CreatedAt         string              `json:"created_at"`
	FullText          string              `json:"full_text"`
	UserIDStr         string              `json:"user_id_str"`
	ConversationIDStr string              `json:"conversation_id_str"`
	InReplyToStatusID string              `json:"in_reply_to_status_id_str"`
	QuotedStatusIDStr string              `json:"quoted_status_id_str"`
	FavoriteCount     int64               `json:"favorite_count"`
	RetweetCount      int64               `json:"retweet_count"`
	ReplyCount        int64               `json:"reply_count"`
	QuoteCount        int64               `json:"quote_count"`
	BookmarkCount     int64               `json:"bookmark_count"`
	Entities          *rawGraphQLEntities `json:"entities"`
	ExtendedEntities  *rawGraphQLEntities `json:"extended_entities"`
}

type rawGraphQLEntities struct {
	Media []json.RawMessage `json:"media"`
}

func parseTimelineInstructions(instructions []graphqlTimelineInstruction) ([]WebTweet, []WebProfile, string) {
	var tweets []WebTweet
	var profiles []WebProfile
	seenTweets := map[string]struct{}{}
	seenProfiles := map[string]struct{}{}
	nextCursor := ""

	addProfile := func(p WebProfile) {
		if strings.TrimSpace(p.AuthorID) == "" {
			return
		}
		if _, exists := seenProfiles[p.AuthorID]; exists {
			return
		}
		seenProfiles[p.AuthorID] = struct{}{}
		profiles = append(profiles, p)
	}

	collectEntry := func(entry graphqlTimelineEntry) {
		if entry.Content.CursorType == "Bottom" && strings.TrimSpace(entry.Content.Value) != "" && nextCursor == "" {
			nextCursor = strings.TrimSpace(entry.Content.Value)
		}
		for _, rawResult := range collectRawTweetResults(entry.Content) {
			tweet, profile, ok := decodeGraphQLTweet(rawResult)
			if !ok {
				continue
			}
			if _, exists := seenTweets[tweet.ID]; exists {
				continue
			}
			seenTweets[tweet.ID] = struct{}{}
			tweets = append(tweets, tweet)
			addProfile(profile)
		}
	}

	for _, instruction := range instructions {
		if instruction.Entry != nil {
			collectEntry(*instruction.Entry)
		}
		for _, entry := range instruction.Entries {
			collectEntry(entry)
		}
	}
	return tweets, profiles, nextCursor
}

func collectRawTweetResults(content graphqlTimelineEntryContent) []json.RawMessage {
	var out []json.RawMessage
	appendHolder := func(holder *graphqlTimelineItemContent) {
		if holder != nil && holder.TweetResults != nil && len(holder.TweetResults.Result) > 0 {
			out = append(out, holder.TweetResults.Result)
		}
	}
	appendHolder(content.ItemContent)
	if content.Item != nil {
		appendHolder(content.Item.ItemContent)
	}
	for _, moduleItem := range content.Items {
		appendHolder(moduleItem.ItemContent)
		if moduleItem.Item != nil {
			appendHolder(moduleItem.Item.ItemContent)
		}
		if moduleItem.Content != nil {
			appendHolder(moduleItem.Content.ItemContent)
		}
	}
	return out
}

func decodeGraphQLTweet(raw json.RawMessage) (WebTweet, WebProfile, bool) {
	unwrappedRaw, parsed, ok := unwrapGraphQLTweet(raw, 0)
	if !ok {
		return WebTweet{}, WebProfile{}, false
	}
	id := strings.TrimSpace(parsed.RestID)
	if id == "" && parsed.Legacy != nil {
		id = strings.TrimSpace(parsed.Legacy.IDStr)
	}
	if id == "" {
		return WebTweet{}, WebProfile{}, false
	}
	text := extractGraphQLTweetText(parsed)
	if strings.TrimSpace(text) == "" {
		return WebTweet{}, WebProfile{}, false
	}
	var authorID, authorHandle, authorName string
	if parsed.Core != nil && parsed.Core.UserResults != nil && parsed.Core.UserResults.Result != nil {
		user := unwrapGraphQLUser(parsed.Core.UserResults.Result)
		if user != nil {
			authorID = strings.TrimSpace(user.RestID)
			if user.Core != nil {
				authorHandle = strings.TrimPrefix(strings.TrimSpace(user.Core.ScreenName), "@")
				authorName = strings.TrimSpace(user.Core.Name)
			}
			if user.Legacy != nil {
				if authorHandle == "" {
					authorHandle = strings.TrimPrefix(strings.TrimSpace(user.Legacy.ScreenName), "@")
				}
				if authorName == "" {
					authorName = strings.TrimSpace(user.Legacy.Name)
				}
			}
		}
	}
	tweet := WebTweet{
		ID:           id,
		AuthorID:     authorID,
		AuthorHandle: authorHandle,
		AuthorName:   authorName,
		Text:         html.UnescapeString(text),
		RawJSON:      string(unwrappedRaw),
	}
	if parsed.Legacy != nil {
		if tweet.AuthorID == "" {
			tweet.AuthorID = strings.TrimSpace(parsed.Legacy.UserIDStr)
		}
		tweet.CreatedAt = parseTwitterCreatedAt(parsed.Legacy.CreatedAt)
		tweet.InReplyToID = strings.TrimSpace(parsed.Legacy.InReplyToStatusID)
		tweet.ConversationID = strings.TrimSpace(parsed.Legacy.ConversationIDStr)
		tweet.QuotedTweetID = strings.TrimSpace(parsed.Legacy.QuotedStatusIDStr)
		tweet.LikeCount = parsed.Legacy.FavoriteCount
		tweet.RetweetCount = parsed.Legacy.RetweetCount
		tweet.ReplyCount = parsed.Legacy.ReplyCount
		tweet.QuoteCount = parsed.Legacy.QuoteCount
		tweet.BookmarkCount = parsed.Legacy.BookmarkCount
		if (parsed.Legacy.ExtendedEntities != nil && len(parsed.Legacy.ExtendedEntities.Media) > 0) ||
			(parsed.Legacy.Entities != nil && len(parsed.Legacy.Entities.Media) > 0) {
			tweet.HasMedia = true
		}
	}
	if tweet.QuotedTweetID == "" && parsed.QuotedStatusResult != nil && len(parsed.QuotedStatusResult.Result) > 0 {
		if _, quoted, ok := unwrapGraphQLTweet(parsed.QuotedStatusResult.Result, 0); ok {
			tweet.QuotedTweetID = strings.TrimSpace(quoted.RestID)
		}
	}
	if parsed.Views != nil && strings.TrimSpace(parsed.Views.Count) != "" {
		if views, err := strconv.ParseInt(strings.TrimSpace(parsed.Views.Count), 10, 64); err == nil && views > 0 {
			tweet.ViewCount = views
		}
	}
	profile := WebProfile{
		AuthorID:    tweet.AuthorID,
		Handle:      tweet.AuthorHandle,
		DisplayName: tweet.AuthorName,
	}
	return tweet, profile, true
}

func unwrapGraphQLTweet(raw json.RawMessage, depth int) (json.RawMessage, *rawGraphQLTweetResult, bool) {
	if depth > 4 || len(raw) == 0 {
		return nil, nil, false
	}
	var parsed rawGraphQLTweetResult
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, nil, false
	}
	if len(parsed.Tweet) > 0 && string(parsed.Tweet) != "null" {
		return unwrapGraphQLTweet(parsed.Tweet, depth+1)
	}
	if parsed.TypeName == "TweetTombstone" || parsed.TypeName == "TweetUnavailable" {
		return nil, nil, false
	}
	return raw, &parsed, true
}

func unwrapGraphQLUser(user *rawGraphQLUserResult) *rawGraphQLUserResult {
	if user == nil {
		return nil
	}
	if user.User != nil {
		return user.User
	}
	return user
}

func extractGraphQLTweetText(parsed *rawGraphQLTweetResult) string {
	if parsed.Article != nil {
		var title, body string
		if parsed.Article.ArticleResults != nil && parsed.Article.ArticleResults.Result != nil {
			title = strings.TrimSpace(parsed.Article.ArticleResults.Result.Title)
			body = firstNonEmptyString(
				parsed.Article.ArticleResults.Result.PlainText,
				parsed.Article.ArticleResults.Result.PreviewText,
			)
		}
		if title == "" {
			title = strings.TrimSpace(parsed.Article.Title)
		}
		if body == "" {
			body = firstNonEmptyString(parsed.Article.PlainText, parsed.Article.PreviewText)
		}
		if title != "" && body != "" && !strings.HasPrefix(body, title) {
			return title + "\n\n" + body
		}
		if body != "" {
			return body
		}
		if title != "" {
			return title
		}
	}
	if parsed.NoteTweet != nil &&
		parsed.NoteTweet.NoteTweetResults != nil &&
		parsed.NoteTweet.NoteTweetResults.Result != nil {
		if noteText := strings.TrimSpace(parsed.NoteTweet.NoteTweetResults.Result.Text); noteText != "" {
			return noteText
		}
	}
	if parsed.Legacy != nil {
		return strings.TrimSpace(parsed.Legacy.FullText)
	}
	return ""
}

func parseTwitterCreatedAt(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	for _, layout := range []string{legacyTwitterTimeLayout, time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
