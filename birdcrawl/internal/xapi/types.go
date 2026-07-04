package xapi

import (
	"encoding/json"
	"time"
)

// Prices in USD micros per returned resource, reconstructed from the full
// 2026-07-04 X bill ($10.36 for the complete backfill): posts on the owner's
// own timelines (authored, likes, bookmarks) and own-tweet lookups bill
// $0.001 each; other people's posts (the mentions timeline) bill $0.005;
// expansion profiles and /2/users/me billed $0. Beware the console usage
// page: it lags by hours — only the credits balance is authoritative.
const (
	PriceOwnedPostMicros = int64(1000)
	PriceOtherPostMicros = int64(5000)
	PriceUserMicros      = int64(0)
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type Tweet struct {
	ID              string
	Text            string
	CreatedAt       time.Time
	AuthorID        string
	ConversationID  string
	InReplyToID     string
	QuotedTweetID   string
	PublicMetrics   Metrics
	RawJSON         string
	MetricsReturned bool
}

type Metrics struct {
	LikeCount     int64 `json:"like_count"`
	RetweetCount  int64 `json:"retweet_count"`
	ReplyCount    int64 `json:"reply_count"`
	ViewCount     int64 `json:"impression_count"`
	QuoteCount    int64 `json:"quote_count"`
	BookmarkCount int64 `json:"bookmark_count"`
}

type TweetPage struct {
	Tweets    []Tweet
	Users     []User
	NextToken string
	NewestID  string
	Charge    Charge
}

type Charge struct {
	OwnedPosts int
	OtherPosts int
	Users      int
}

func (c Charge) Micros() int64 {
	return int64(c.OwnedPosts)*PriceOwnedPostMicros +
		int64(c.OtherPosts)*PriceOtherPostMicros +
		int64(c.Users)*PriceUserMicros
}

type tweetPageResponse struct {
	Data     []json.RawMessage `json:"data"`
	Includes struct {
		Users []User `json:"users"`
	} `json:"includes"`
	Meta struct {
		NextToken   string `json:"next_token"`
		NewestID    string `json:"newest_id"`
		ResultCount int    `json:"result_count"`
	} `json:"meta"`
}

type meResponse struct {
	Data User `json:"data"`
}

type rawTweet struct {
	ID                  string       `json:"id"`
	Text                string       `json:"text"`
	CreatedAt           string       `json:"created_at"`
	AuthorID            string       `json:"author_id"`
	ConversationID      string       `json:"conversation_id"`
	ReferencedTweets    []referenced `json:"referenced_tweets"`
	PublicMetrics       *Metrics     `json:"public_metrics"`
	InReplyToUserID     string       `json:"in_reply_to_user_id"`
	EditHistoryTweetIDs []string     `json:"edit_history_tweet_ids"`
}

type referenced struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}
