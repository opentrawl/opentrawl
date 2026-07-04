package cli

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/xapi"
)

type convertedPage struct {
	tweets    []store.Tweet
	roles     []store.Role
	profiles  []store.Profile
	deficient int
}

func (s *syncRunner) convertPage(phase string, page xapi.TweetPage, role string, roleTime time.Time) (convertedPage, error) {
	users := usersByID(page.Users)
	out := convertedPage{profiles: profilesFromUsers(page.Users, s.now())}
	for _, tweet := range page.Tweets {
		if strings.TrimSpace(tweet.ID) == "" || strings.TrimSpace(tweet.Text) == "" {
			out.deficient++
			continue
		}
		profile := users[tweet.AuthorID]
		out.tweets = append(out.tweets, storeTweet(tweet, profile, s.now()))
		if role != "" {
			out.roles = append(out.roles, store.Role{TweetID: tweet.ID, Role: role, FirstSeenAt: roleTime, LastSeenAt: roleTime})
		}
	}
	if len(page.Tweets) > 0 && out.deficient*2 > len(page.Tweets) {
		return convertedPage{}, deficientPageError{phase: phase, total: len(page.Tweets), bad: out.deficient}
	}
	return out, nil
}

func usersByID(users []xapi.User) map[string]xapi.User {
	out := make(map[string]xapi.User, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.ID) == "" {
			continue
		}
		out[user.ID] = user
	}
	return out
}

func profilesFromUsers(users []xapi.User, now time.Time) []store.Profile {
	out := make([]store.Profile, 0, len(users))
	seen := map[string]struct{}{}
	for _, user := range users {
		if strings.TrimSpace(user.ID) == "" {
			continue
		}
		if _, ok := seen[user.ID]; ok {
			continue
		}
		seen[user.ID] = struct{}{}
		out = append(out, store.Profile{
			AuthorID:    user.ID,
			Handle:      strings.TrimPrefix(user.Username, "@"),
			DisplayName: user.Name,
			LastSeenAt:  now,
		})
	}
	return out
}

func storeTweet(tweet xapi.Tweet, profile xapi.User, now time.Time) store.Tweet {
	metricsAt := time.Time{}
	if tweet.MetricsReturned {
		metricsAt = now
	}
	return store.Tweet{
		ID:               tweet.ID,
		CreatedAt:        tweet.CreatedAt,
		AuthorID:         tweet.AuthorID,
		AuthorHandle:     strings.TrimPrefix(profile.Username, "@"),
		AuthorName:       profile.Name,
		Text:             tweet.Text,
		InReplyToID:      tweet.InReplyToID,
		ConversationID:   tweet.ConversationID,
		QuotedTweetID:    tweet.QuotedTweetID,
		LikeCount:        tweet.PublicMetrics.LikeCount,
		RetweetCount:     tweet.PublicMetrics.RetweetCount,
		ReplyCount:       tweet.PublicMetrics.ReplyCount,
		ViewCount:        tweet.PublicMetrics.ViewCount,
		QuoteCount:       tweet.PublicMetrics.QuoteCount,
		BookmarkCount:    tweet.PublicMetrics.BookmarkCount,
		RawJSON:          tweet.RawJSON,
		FirstSource:      "live",
		MetricsFetchedAt: metricsAt,
	}
}
