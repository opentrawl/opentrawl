package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/xapi"
)

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 200
	defaultStatsLimit  = 10
	maxStatsLimit      = 200
)

type metadataEnvelope struct {
	SchemaVersion   int      `json:"schema_version"`
	ContractVersion int      `json:"contract_version"`
	ID              string   `json:"id"`
	DisplayName     string   `json:"display_name"`
	Version         string   `json:"version"`
	Capabilities    []string `json:"capabilities"`
}

type statusEnvelope struct {
	AppID     string                `json:"app_id"`
	State     string                `json:"state"`
	Summary   string                `json:"summary"`
	Freshness freshnessEnvelope     `json:"freshness"`
	Counts    []countEnvelope       `json:"counts"`
	Spend     spendEnvelope         `json:"spend"`
	Auth      authEnvelope          `json:"auth"`
	Log       *render.DoctorLogTail `json:"log,omitempty"`
	logTail   render.LogTail        `json:"-"`
}

type freshnessEnvelope struct {
	LastSync   string `json:"last_sync,omitempty"`
	LastImport string `json:"last_import,omitempty"`
}

type countEnvelope struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type authEnvelope struct {
	Authorized           bool `json:"authorized"`
	CredentialsPresent   bool `json:"credentials_present"`
	TokenValidAtLastSync bool `json:"token_valid_at_last_sync"`
}

type spendEnvelope struct {
	Month            string `json:"month"`
	SpentUSD         string `json:"spent_usd"`
	MonthlyBudgetUSD string `json:"monthly_budget_usd"`
	RemainingUSD     string `json:"remaining_usd"`
}

type errorEnvelope struct {
	Error contractErrorBody `json:"error"`
}

type contractErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Remedy  string `json:"remedy"`
}

type doctorOutput struct {
	Checks  []doctorCheck         `json:"checks"`
	Log     *render.DoctorLogTail `json:"log,omitempty"`
	logTail render.LogTail        `json:"-"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

type searchEnvelope struct {
	Query        string         `json:"query"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
}

type searchResult struct {
	Ref     string `json:"ref"`
	Time    string `json:"time"`
	Who     string `json:"who"`
	Where   string `json:"where"`
	Snippet string `json:"snippet"`
}

type openEnvelope struct {
	Ref                string      `json:"ref"`
	Tweet              openTweet   `json:"tweet"`
	Ancestors          []openTweet `json:"ancestors"`
	Replies            []openTweet `json:"replies"`
	AncestorsTruncated bool        `json:"ancestors_truncated"`
	RepliesTruncated   bool        `json:"replies_truncated"`
}

type openTweet struct {
	Ref           string `json:"ref"`
	Time          string `json:"time,omitempty"`
	Who           string `json:"who,omitempty"`
	Text          string `json:"text"`
	InReplyTo     string `json:"in_reply_to,omitempty"`
	LikeCount     int64  `json:"like_count,omitempty"`
	RetweetCount  int64  `json:"retweet_count,omitempty"`
	ReplyCount    int64  `json:"reply_count,omitempty"`
	CountsAsOf    string `json:"counts_as_of,omitempty"`
	Unavailable   bool   `json:"unavailable,omitempty"`
	Conversation  string `json:"conversation_id,omitempty"`
	QuotedTweetID string `json:"quoted_tweet_id,omitempty"`
}

type importEnvelope struct {
	Tweets              int    `json:"tweets"`
	Authored            int    `json:"authored"`
	LikesSeen           int    `json:"likes_seen"`
	Profiles            int    `json:"profiles"`
	NoteTweetsMerged    int    `json:"note_tweets_merged"`
	NoteTweetsUnmatched int    `json:"note_tweets_unmatched"`
	LikesWithoutText    int    `json:"likes_without_text"`
	StartedAt           string `json:"started_at"`
	FinishedAt          string `json:"finished_at"`
}

type statsEnvelope struct {
	By                   string     `json:"by"`
	Window               string     `json:"window"`
	FreshnessSpread      string     `json:"freshness_spread"`
	Population           int        `json:"population"`
	PopulationWithCounts int        `json:"population_with_counts"`
	CountsMissing        int        `json:"counts_missing"`
	Results              []statsRow `json:"results"`
}

type statsRow struct {
	Ref        string `json:"ref"`
	Time       string `json:"time"`
	Who        string `json:"who"`
	Text       string `json:"text"`
	Count      int64  `json:"count"`
	CountsAsOf string `json:"counts_as_of"`
}

func contractMetadata() metadataEnvelope {
	return metadataEnvelope{
		SchemaVersion:   1,
		ContractVersion: 1,
		ID:              "birdcrawl",
		DisplayName:     "X",
		Version:         version,
		Capabilities:    []string{"metadata", "status", "sync", "search", "open", "doctor", "stats", "archive_import"},
	}
}

func (r *runtime) statusEnvelope() statusEnvelope {
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		cfg = birdConfig{MonthlyBudgetMicros: defaultMonthlyBudgetUSDMicros}
	}
	if info, err := os.Stat(r.dbPath); err != nil {
		if os.IsNotExist(err) {
			return r.newStatusEnvelope("missing", "archive database is missing", store.Status{}, cfg)
		}
		return r.newStatusEnvelope("error", "archive database cannot be read", store.Status{}, cfg)
	} else if info.IsDir() {
		return r.newStatusEnvelope("error", "archive database path is a directory", store.Status{}, cfg)
	}
	st, err := store.OpenReadOnly(r.ctx, r.dbPath)
	if err != nil {
		return r.newStatusEnvelope("error", "archive database cannot be read", store.Status{}, cfg)
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		return r.newStatusEnvelope("error", "archive status cannot be read", store.Status{}, cfg)
	}
	return r.newStatusEnvelope(statusState(status), statusSummary(status), status, cfg)
}

func (r *runtime) newStatusEnvelope(state, summary string, status store.Status, cfg birdConfig) statusEnvelope {
	logTail := r.logTail()
	credentialsPresent := xapi.CredentialsPresent(xapi.DefaultCredentialsPath())
	month := status.SpendMonth
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	spent := float64(status.SpendMicros) / 1_000_000
	budget := cfg.MonthlyBudgetUSD()
	remaining := max(0, budget-spent)
	return statusEnvelope{
		AppID:   "birdcrawl",
		State:   state,
		Summary: summary,
		Freshness: freshnessEnvelope{
			LastSync:   formatOptionalTime(status.LastLiveSync),
			LastImport: formatOptionalTime(status.LastImportAt),
		},
		Counts: []countEnvelope{
			{ID: "authored", Label: "authored", Value: int64(status.Authored)},
			{ID: "bookmarks", Label: "bookmarks", Value: int64(status.Bookmarks)},
			{ID: "likes_seen", Label: "tweets liked", Value: int64(status.LikesSeen)},
			{ID: "replies_to_me", Label: "replies to me", Value: int64(status.RepliesToMe)},
		},
		Spend: spendEnvelope{
			Month:            month,
			SpentUSD:         fmt.Sprintf("%.2f", spent),
			MonthlyBudgetUSD: fmt.Sprintf("%.2f", budget),
			RemainingUSD:     fmt.Sprintf("%.2f", remaining),
		},
		Auth: authEnvelope{
			Authorized:           credentialsPresent && status.TokenValid,
			CredentialsPresent:   credentialsPresent,
			TokenValidAtLastSync: status.TokenValid,
		},
		Log:     render.DoctorLogTailOutput(logTail),
		logTail: logTail,
	}
}

func statusState(status store.Status) string {
	switch {
	case status.Tweets == 0:
		return "empty"
	case status.LastImportAt.IsZero():
		return "stale"
	default:
		return "ok"
	}
}

func statusSummary(status store.Status) string {
	if status.Tweets == 0 {
		return "archive is empty; import an X archive dump"
	}
	live := ""
	switch {
	case status.LastLiveSync.IsZero():
		live = "live sync has not run"
	case strings.HasPrefix(status.LiveSyncResult, "partial"):
		live = "last live sync at " + formatLocalTime(status.LastLiveSync) + " was " + status.LiveSyncResult
	default:
		live = "live synced at " + formatLocalTime(status.LastLiveSync)
	}
	if !status.CoverageThrough.IsZero() {
		return "archive dump imported through " + formatLocalTime(status.CoverageThrough) + "; " + live
	}
	return "archive has local X data; " + live
}

func newSearchEnvelope(query string, results []store.SearchResult, total int) searchEnvelope {
	items := make([]searchResult, 0, len(results))
	for _, result := range results {
		items = append(items, searchResult{
			Ref:     store.TweetRef(result.ID),
			Time:    formatOptionalTime(result.CreatedAt),
			Who:     result.Who,
			Where:   result.Where,
			Snippet: result.Snippet,
		})
	}
	return searchEnvelope{Query: query, Results: items, TotalMatches: total, Truncated: total > len(items)}
}

func newOpenEnvelope(result store.OpenResult) openEnvelope {
	return openEnvelope{
		Ref:                store.TweetRef(result.Tweet.ID),
		Tweet:              newOpenTweet(result.Tweet),
		Ancestors:          newAncestorTweets(result.Ancestors),
		Replies:            newOpenTweets(result.Replies),
		AncestorsTruncated: result.AncestorsTruncated,
		RepliesTruncated:   result.RepliesTruncated,
	}
}

func newOpenTweets(tweets []store.Tweet) []openTweet {
	out := make([]openTweet, 0, len(tweets))
	for _, tweet := range tweets {
		out = append(out, newOpenTweet(tweet))
	}
	return out
}

func newAncestorTweets(tweets []store.OpenTweet) []openTweet {
	out := make([]openTweet, 0, len(tweets))
	for _, tweet := range tweets {
		if !tweet.Available {
			out = append(out, openTweet{Ref: tweet.Ref, Text: tweet.Text, Unavailable: true})
			continue
		}
		out = append(out, newOpenTweet(tweet.Tweet))
	}
	return out
}

func newOpenTweet(tweet store.Tweet) openTweet {
	return openTweet{
		Ref:           store.TweetRef(tweet.ID),
		Time:          formatOptionalTime(tweet.CreatedAt),
		Who:           store.DisplayName(tweet.AuthorName, tweet.AuthorHandle),
		Text:          tweet.Text,
		InReplyTo:     tweet.InReplyToID,
		LikeCount:     tweet.LikeCount,
		RetweetCount:  tweet.RetweetCount,
		ReplyCount:    tweet.ReplyCount,
		CountsAsOf:    formatOptionalTime(tweet.MetricsFetchedAt),
		Conversation:  tweet.ConversationID,
		QuotedTweetID: tweet.QuotedTweetID,
	}
}

func newImportEnvelope(stats store.ImportStats) importEnvelope {
	return importEnvelope{
		Tweets:              stats.Tweets,
		Authored:            stats.Authored,
		LikesSeen:           stats.LikesSeen,
		Profiles:            stats.Profiles,
		NoteTweetsMerged:    stats.NoteTweetsMerged,
		NoteTweetsUnmatched: stats.NoteTweetsUnmatched,
		LikesWithoutText:    stats.LikesWithoutText,
		StartedAt:           formatOptionalTime(stats.StartedAt),
		FinishedAt:          formatOptionalTime(stats.FinishedAt),
	}
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatLocalTime(t)
}
