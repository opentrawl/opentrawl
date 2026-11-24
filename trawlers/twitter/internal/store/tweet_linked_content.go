package store

import (
	"context"
	"encoding/json"
	"strings"
)

// QuotedTweet is the post another post quotes, as the archive knows it.
type QuotedTweet struct {
	ID           string
	AuthorHandle string
	AuthorName   string
	// TextWithExpandedLinks has its t.co links replaced where X said where
	// they lead.
	TextWithExpandedLinks string
	ArchivedInThisArchive bool
}

// PublicURL is the x.com address of the quoted post.
func (q QuotedTweet) PublicURL() string {
	handle := strings.TrimPrefix(strings.TrimSpace(q.AuthorHandle), "@")
	if handle == "" {
		handle = "i"
	}
	return "https://x.com/" + handle + "/status/" + q.ID
}

// TweetLinkedContent is what a tweet's stored X payload says about the
// links in its text and the post it quotes.
type TweetLinkedContent struct {
	ExpandedURLByShortenedURL map[string]string
	QuotedTweetFromPayload    *QuotedTweet
}

// storedXTweetPayload covers the three payload shapes the archive keeps:
// X web GraphQL results, API v2 tweets and imported archive posts.
type storedXTweetPayload struct {
	Entities           xURLEntities            `json:"entities"`
	Legacy             xGraphQLTweetLegacy     `json:"legacy"`
	QuotedStatusResult xGraphQLTweetResultSlot `json:"quoted_status_result"`
	QuotedTweet        *importedQuotedTweet    `json:"quotedTweet"`
}

type xURLEntities struct {
	URLs  []xURLEntity `json:"urls"`
	Media []xURLEntity `json:"media"`
}

type xURLEntity struct {
	ShortenedURL string `json:"url"`
	ExpandedURL  string `json:"expanded_url"`
}

type xGraphQLTweetLegacy struct {
	FullText string       `json:"full_text"`
	Entities xURLEntities `json:"entities"`
}

type xGraphQLTweetResultSlot struct {
	Result *xGraphQLTweetResult `json:"result"`
}

type xGraphQLTweetResult struct {
	RestID string              `json:"rest_id"`
	Core   xGraphQLTweetCore   `json:"core"`
	Legacy xGraphQLTweetLegacy `json:"legacy"`
	// TweetWithVisibilityResults wraps the post one level down.
	Tweet *xGraphQLTweetResult `json:"tweet"`
}

type xGraphQLTweetCore struct {
	UserResults struct {
		Result struct {
			Core   xGraphQLUserNames `json:"core"`
			Legacy xGraphQLUserNames `json:"legacy"`
		} `json:"result"`
	} `json:"user_results"`
}

type xGraphQLUserNames struct {
	ScreenName string `json:"screen_name"`
	Name       string `json:"name"`
}

type importedQuotedTweet struct {
	ID     string `json:"id"`
	Text   string `json:"text"`
	Author struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"author"`
}

// ReadTweetLinkedContent reads the link targets and quoted post that X
// stored with a tweet. A payload it cannot read gives no links.
func ReadTweetLinkedContent(rawJSON string) TweetLinkedContent {
	content := TweetLinkedContent{ExpandedURLByShortenedURL: map[string]string{}}
	if strings.TrimSpace(rawJSON) == "" {
		return content
	}
	var payload storedXTweetPayload
	if err := json.Unmarshal([]byte(rawJSON), &payload); err != nil {
		return content
	}
	content.addURLEntities(payload.Entities)
	content.addURLEntities(payload.Legacy.Entities)
	if quoted := payload.QuotedStatusResult.Result; quoted != nil {
		if quoted.Tweet != nil {
			quoted = quoted.Tweet
		}
		content.addURLEntities(quoted.Legacy.Entities)
		names := quoted.Core.UserResults.Result.Core
		if names.ScreenName == "" {
			names = quoted.Core.UserResults.Result.Legacy
		}
		if quoted.RestID != "" {
			content.QuotedTweetFromPayload = &QuotedTweet{
				ID:                    quoted.RestID,
				AuthorHandle:          names.ScreenName,
				AuthorName:            names.Name,
				TextWithExpandedLinks: content.ExpandShortenedURLs(quoted.Legacy.FullText),
			}
		}
	}
	if imported := payload.QuotedTweet; imported != nil && imported.ID != "" {
		content.QuotedTweetFromPayload = &QuotedTweet{
			ID:                    imported.ID,
			AuthorHandle:          imported.Author.Username,
			AuthorName:            imported.Author.Name,
			TextWithExpandedLinks: content.ExpandShortenedURLs(imported.Text),
		}
	}
	return content
}

func (c TweetLinkedContent) addURLEntities(entities xURLEntities) {
	for _, entity := range append(entities.URLs, entities.Media...) {
		shortened := strings.TrimSpace(entity.ShortenedURL)
		expanded := strings.TrimSpace(entity.ExpandedURL)
		if strings.HasPrefix(shortened, "https://t.co/") && expanded != "" {
			c.ExpandedURLByShortenedURL[shortened] = expanded
		}
	}
}

// ExpandShortenedURLs replaces each t.co link whose target X stored.
func (c TweetLinkedContent) ExpandShortenedURLs(text string) string {
	if len(c.ExpandedURLByShortenedURL) == 0 || !strings.Contains(text, "https://t.co/") {
		return text
	}
	fields := strings.Fields(text)
	replacements := make([]string, 0, len(c.ExpandedURLByShortenedURL)*2)
	for _, field := range fields {
		if expanded, ok := c.ExpandedURLByShortenedURL[field]; ok {
			replacements = append(replacements, field, expanded)
		}
	}
	if len(replacements) == 0 {
		return text
	}
	return strings.NewReplacer(replacements...).Replace(text)
}

// TweetTextWithExpandedLinks is the tweet text with stored link targets.
func TweetTextWithExpandedLinks(tweet Tweet) string {
	return ReadTweetLinkedContent(tweet.RawJSON).ExpandShortenedURLs(tweet.Text)
}

// QuotedTweetOf returns the post a tweet quotes: the archived post when the
// archive holds it, otherwise what X stored with the quoting tweet.
func (s *Store) QuotedTweetOf(ctx context.Context, tweet Tweet) (*QuotedTweet, error) {
	quotedID := strings.TrimSpace(tweet.QuotedTweetID)
	if quotedID == "" {
		return nil, nil
	}
	archived, err := s.archivedQuotedTweets(ctx, `where t.id = ?`, quotedID)
	if err != nil {
		return nil, err
	}
	if quoted, ok := archived[quotedID]; ok {
		return &quoted, nil
	}
	if quoted := ReadTweetLinkedContent(tweet.RawJSON).QuotedTweetFromPayload; quoted != nil && quoted.ID == quotedID {
		return quoted, nil
	}
	return nil, nil
}

func (s *Store) archivedQuotedTweets(ctx context.Context, where string, args ...any) (map[string]QuotedTweet, error) {
	rows, err := s.db.QueryContext(ctx, `select t.id, coalesce(t.author_handle, ''), coalesce(t.author_name, ''),
coalesce(t.text, ''), coalesce(t.raw_json, '') from tweets t `+where, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	quotedTweets := map[string]QuotedTweet{}
	for rows.Next() {
		var quoted QuotedTweet
		var text, rawJSON string
		if err := rows.Scan(&quoted.ID, &quoted.AuthorHandle, &quoted.AuthorName, &text, &rawJSON); err != nil {
			return nil, err
		}
		quoted.TextWithExpandedLinks = ReadTweetLinkedContent(rawJSON).ExpandShortenedURLs(text)
		quoted.ArchivedInThisArchive = true
		quotedTweets[quoted.ID] = quoted
	}
	return quotedTweets, rows.Err()
}
