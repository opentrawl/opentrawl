package archive

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

type jsonNumber int64

func (n *jsonNumber) UnmarshalJSON(data []byte) error {
	raw := strings.Trim(strings.TrimSpace(string(data)), `"`)
	if raw == "" || raw == "null" {
		*n = 0
		return nil
	}
	if i, err := strconv.ParseInt(raw, 10, 64); err == nil {
		*n = jsonNumber(i)
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*n = jsonNumber(int64(f))
	return nil
}

func (n jsonNumber) Int64() int64 {
	return int64(n)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" && trimmed != "null" {
			return trimmed
		}
	}
	return ""
}

func dedupeTweets(tweets []store.Tweet) []store.Tweet {
	seen := make(map[string]store.Tweet, len(tweets))
	order := make([]string, 0, len(tweets))
	for _, tweet := range tweets {
		if strings.TrimSpace(tweet.ID) == "" {
			continue
		}
		if _, ok := seen[tweet.ID]; !ok {
			order = append(order, tweet.ID)
			seen[tweet.ID] = tweet
			continue
		}
		seen[tweet.ID] = mergeTweet(seen[tweet.ID], tweet)
	}
	out := make([]store.Tweet, 0, len(order))
	for _, id := range order {
		out = append(out, seen[id])
	}
	return out
}

func mergeTweet(a, b store.Tweet) store.Tweet {
	if a.CreatedAt.IsZero() {
		a.CreatedAt = b.CreatedAt
	}
	if a.AuthorID == "" {
		a.AuthorID = b.AuthorID
	}
	if a.AuthorHandle == "" {
		a.AuthorHandle = b.AuthorHandle
	}
	if a.AuthorName == "" {
		a.AuthorName = b.AuthorName
	}
	if a.Text == "" {
		a.Text = b.Text
	}
	if a.InReplyToID == "" {
		a.InReplyToID = b.InReplyToID
	}
	if a.ConversationID == "" {
		a.ConversationID = b.ConversationID
	}
	if a.QuotedTweetID == "" {
		a.QuotedTweetID = b.QuotedTweetID
	}
	a.LikeCount = max(a.LikeCount, b.LikeCount)
	a.RetweetCount = max(a.RetweetCount, b.RetweetCount)
	a.ReplyCount = max(a.ReplyCount, b.ReplyCount)
	a.ViewCount = max(a.ViewCount, b.ViewCount)
	a.QuoteCount = max(a.QuoteCount, b.QuoteCount)
	a.BookmarkCount = max(a.BookmarkCount, b.BookmarkCount)
	a.HasMedia = a.HasMedia || b.HasMedia
	return a
}

func dedupeRoles(roles []store.Role) []store.Role {
	seen := make(map[string]store.Role, len(roles))
	order := make([]string, 0, len(roles))
	for _, role := range roles {
		key := role.TweetID + "\x00" + role.Role
		if strings.TrimSpace(role.TweetID) == "" || strings.TrimSpace(role.Role) == "" {
			continue
		}
		if _, ok := seen[key]; !ok {
			order = append(order, key)
			seen[key] = role
			continue
		}
		existing := seen[key]
		if role.FirstSeenAt.Before(existing.FirstSeenAt) || existing.FirstSeenAt.IsZero() {
			existing.FirstSeenAt = role.FirstSeenAt
		}
		if role.LastSeenAt.After(existing.LastSeenAt) {
			existing.LastSeenAt = role.LastSeenAt
		}
		seen[key] = existing
	}
	out := make([]store.Role, 0, len(order))
	for _, key := range order {
		out = append(out, seen[key])
	}
	return out
}

func dedupeProfiles(profiles []store.Profile) []store.Profile {
	seen := make(map[string]store.Profile, len(profiles))
	for _, profile := range profiles {
		if strings.TrimSpace(profile.AuthorID) == "" {
			continue
		}
		existing := seen[profile.AuthorID]
		if existing.Handle == "" {
			existing.Handle = profile.Handle
		}
		if existing.DisplayName == "" {
			existing.DisplayName = profile.DisplayName
		}
		if profile.LastSeenAt.After(existing.LastSeenAt) {
			existing.LastSeenAt = profile.LastSeenAt
		}
		existing.AuthorID = profile.AuthorID
		seen[profile.AuthorID] = existing
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]store.Profile, 0, len(ids))
	for _, id := range ids {
		out = append(out, seen[id])
	}
	return out
}

func newestTweetTime(tweets []store.Tweet) time.Time {
	var maxTime time.Time
	for _, tweet := range tweets {
		if tweet.CreatedAt.After(maxTime) {
			maxTime = tweet.CreatedAt
		}
	}
	return maxTime
}
