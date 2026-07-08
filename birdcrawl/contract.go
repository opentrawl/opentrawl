package birdcrawl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/xapi"
)

const (
	defaultSearchLimit = 20
	defaultStatsLimit  = 10
)

type statusEnvelope struct {
	AppID        string            `json:"app_id"`
	State        string            `json:"state"`
	Summary      string            `json:"summary"`
	Freshness    freshnessEnvelope `json:"freshness"`
	Counts       []countEnvelope   `json:"counts"`
	Spend        spendEnvelope     `json:"spend"`
	Auth         authEnvelope      `json:"auth"`
	summaryHuman string            `json:"-"`
}

type freshnessEnvelope struct {
	LastSync       string    `json:"last_sync,omitempty"`
	LastImport     string    `json:"last_import,omitempty"`
	lastSyncTime   time.Time `json:"-"`
	lastImportTime time.Time `json:"-"`
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
	LiveSyncPaused   bool   `json:"live_sync_paused,omitempty"`
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
	Query         string         `json:"query"`
	Results       []searchResult `json:"results"`
	TotalMatches  int            `json:"total_matches"`
	Truncated     bool           `json:"truncated"`
	Limit         int            `json:"-"`
	ownerAuthorID string         `json:"-"`
}

type searchResult struct {
	Ref               string    `json:"ref"`
	ShortRef          string    `json:"short_ref"`
	Time              string    `json:"time"`
	Who               string    `json:"who"`
	Where             string    `json:"where,omitempty"`
	Snippet           string    `json:"snippet"`
	timeValue         time.Time `json:"-"`
	rawWho            string    `json:"-"`
	authorID          string    `json:"-"`
	inReplyTo         string    `json:"-"`
	inReplyToAuthorID string    `json:"-"`
}

type listEnvelope struct {
	Kind          string       `json:"kind"`
	Results       []listResult `json:"results"`
	Total         int          `json:"total"`
	Truncated     bool         `json:"truncated"`
	Limit         int          `json:"-"`
	ownerAuthorID string       `json:"-"`
}

type listResult struct {
	Ref               string    `json:"ref"`
	ShortRef          string    `json:"short_ref"`
	Time              string    `json:"time"`
	Who               string    `json:"who"`
	InReplyTo         string    `json:"in_reply_to,omitempty"`
	Text              string    `json:"text"`
	timeValue         time.Time `json:"-"`
	rawWho            string    `json:"-"`
	authorID          string    `json:"-"`
	inReplyToAuthorID string    `json:"-"`
}

type openEnvelope struct {
	Ref                string      `json:"ref"`
	Tweet              openTweet   `json:"tweet"`
	Ancestors          []openTweet `json:"ancestors"`
	Replies            []openTweet `json:"replies"`
	AncestorsTruncated bool        `json:"ancestors_truncated"`
	RepliesTruncated   bool        `json:"replies_truncated"`
	ownerAuthorID      string      `json:"-"`
}

type openTweet struct {
	Ref                string    `json:"ref"`
	Time               string    `json:"time,omitempty"`
	Who                string    `json:"who,omitempty"`
	Text               string    `json:"text"`
	InReplyTo          string    `json:"in_reply_to,omitempty"`
	LikeCount          int64     `json:"like_count,omitempty"`
	RetweetCount       int64     `json:"retweet_count,omitempty"`
	ReplyCount         int64     `json:"reply_count,omitempty"`
	CountsAsOf         string    `json:"counts_as_of,omitempty"`
	Note               string    `json:"note,omitempty"`
	Unavailable        bool      `json:"unavailable,omitempty"`
	Conversation       string    `json:"conversation_id,omitempty"`
	QuotedTweetID      string    `json:"quoted_tweet_id,omitempty"`
	ShortRef           string    `json:"-"`
	ReplyingTo         string    `json:"-"`
	authorID           string    `json:"-"`
	replyingToAuthorID string    `json:"-"`
	timeValue          time.Time `json:"-"`
	countsAsOfTime     time.Time `json:"-"`
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
	CountsFetchedFrom    string     `json:"counts_fetched_from"`
	CountsFetchedTo      string     `json:"counts_fetched_to"`
	Population           int        `json:"population"`
	PopulationWithCounts int        `json:"population_with_counts"`
	CountsMissing        int        `json:"counts_missing"`
	Results              []statsRow `json:"results"`
}

type statsRow struct {
	Ref            string    `json:"ref"`
	ShortRef       string    `json:"short_ref"`
	Time           string    `json:"time"`
	Who            string    `json:"who"`
	Text           string    `json:"text"`
	Count          int64     `json:"count"`
	CountsAsOf     string    `json:"counts_as_of"`
	timeValue      time.Time `json:"-"`
	countsAsOfTime time.Time `json:"-"`
}

func (r *runtime) statusEnvelope() statusEnvelope {
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		cfg = birdConfig{MonthlyBudgetMicros: defaultMonthlyBudgetUSDMicros}
	}
	if r.req.Store == nil {
		return r.newStatusEnvelope("missing", "archive database is missing", "archive database is missing", store.Status{}, cfg)
	}
	st, err := store.UseExisting(r.ctx, r.req.Store, r.req.Log)
	if err != nil {
		if errors.Is(err, store.ErrSchemaOutdated) {
			msg := "archive schema needs one sync to finish upgrading"
			return r.newStatusEnvelope("error", msg, msg+"; run: trawl birdcrawl sync", store.Status{}, cfg)
		}
		return r.newStatusEnvelope("error", "archive database cannot be read", "archive database cannot be read", store.Status{}, cfg)
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		return r.newStatusEnvelope("error", "archive status cannot be read", "archive status cannot be read", store.Status{}, cfg)
	}
	return r.newStatusEnvelope(statusState(status), statusSummary(status, formatLocalTime), statusSummary(status, formatHumanLocalTime), status, cfg)
}

func (r *runtime) status(ctx context.Context) (*control.Status, error) {
	_ = ctx
	envelope := r.statusEnvelope()
	status := control.NewStatus(appID, envelope.humanSummary())
	status.State = envelope.State
	status.ConfigPath = r.configPath
	status.DatabasePath = r.dbPath
	status.LastSyncAt = envelope.Freshness.LastSync
	status.LastImportAt = envelope.Freshness.LastImport
	for _, count := range envelope.Counts {
		status.Counts = append(status.Counts, control.NewCount(count.ID, count.Label, count.Value))
	}
	if envelope.Spend.LiveSyncPaused {
		status.Warnings = append(status.Warnings, liveSyncPausedSentence(envelope.Spend.Month))
	}
	return &status, nil
}

func (r *runtime) newStatusEnvelope(state, summary, summaryHuman string, status store.Status, cfg birdConfig) statusEnvelope {
	credentialsPresent := xapi.CredentialsPresent(xapi.DefaultCredentialsPath())
	month := status.SpendMonth
	if month == "" {
		month = time.Now().UTC().Format("2006-01")
	}
	spent := float64(status.SpendMicros) / 1_000_000
	budget := cfg.MonthlyBudgetUSD()
	remaining := max(0, budget-spent)
	liveSyncPaused := cfg.MonthlyBudgetMicros-status.SpendMicros <= 0
	if liveSyncPaused {
		summaryHuman = appendSentence(summaryHuman, liveSyncPausedSentence(month))
	}
	return statusEnvelope{
		AppID:        "birdcrawl",
		State:        state,
		Summary:      summary,
		summaryHuman: summaryHuman,
		Freshness: freshnessEnvelope{
			LastSync:       formatOptionalTime(status.LastLiveSync),
			LastImport:     formatOptionalTime(status.LastImportAt),
			lastSyncTime:   status.LastLiveSync,
			lastImportTime: status.LastImportAt,
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
			LiveSyncPaused:   liveSyncPaused,
		},
		Auth: authEnvelope{
			Authorized:           credentialsPresent && status.TokenValid,
			CredentialsPresent:   credentialsPresent,
			TokenValidAtLastSync: status.TokenValid,
		},
	}
}

func (e statusEnvelope) humanSummary() string {
	if strings.TrimSpace(e.summaryHuman) != "" {
		return e.summaryHuman
	}
	return e.Summary
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

func statusSummary(status store.Status, formatTime func(time.Time) string) string {
	if status.Tweets == 0 {
		return "archive is empty; import an X archive dump"
	}
	live := ""
	switch {
	case status.LastLiveSync.IsZero():
		live = "live sync has not run"
	case strings.HasPrefix(status.LiveSyncResult, "partial"):
		live = "last live sync at " + formatTime(status.LastLiveSync) + " was " + status.LiveSyncResult
	default:
		live = "live synced at " + formatTime(status.LastLiveSync)
	}
	if !status.CoverageThrough.IsZero() {
		return "archive dump imported through " + formatTime(status.CoverageThrough) + "; " + live
	}
	return "archive has local X data; " + live
}

func newSearchEnvelope(query string, results []store.SearchResult, total int, limit int, aliases map[string]string, ownerAuthorID string) searchEnvelope {
	items := make([]searchResult, 0, len(results))
	for _, result := range results {
		ref := store.TweetRef(result.ID)
		items = append(items, searchResult{
			Ref:               ref,
			ShortRef:          aliases[ref],
			Time:              formatOptionalTime(result.CreatedAt),
			Who:               jsonWho(result.Who, result.AuthorID, result.InReplyTo, result.InReplyToAuthorID, ownerAuthorID),
			Where:             result.Where,
			Snippet:           result.Snippet,
			timeValue:         result.CreatedAt,
			rawWho:            result.Who,
			authorID:          result.AuthorID,
			inReplyTo:         result.InReplyTo,
			inReplyToAuthorID: result.InReplyToAuthorID,
		})
	}
	return searchEnvelope{Query: query, Results: items, TotalMatches: total, Truncated: total > len(items), Limit: limit, ownerAuthorID: ownerAuthorID}
}

func newListEnvelope(kind string, results []store.SearchResult, total int, limit int, aliases map[string]string, ownerAuthorID string) listEnvelope {
	items := make([]listResult, 0, len(results))
	for _, result := range results {
		ref := store.TweetRef(result.ID)
		items = append(items, listResult{
			Ref:               ref,
			ShortRef:          aliases[ref],
			Time:              formatOptionalTime(result.CreatedAt),
			Who:               jsonWho(result.Who, result.AuthorID, result.InReplyTo, result.InReplyToAuthorID, ownerAuthorID),
			InReplyTo:         result.InReplyTo,
			Text:              result.Text,
			timeValue:         result.CreatedAt,
			rawWho:            result.Who,
			authorID:          result.AuthorID,
			inReplyToAuthorID: result.InReplyToAuthorID,
		})
	}
	return listEnvelope{Kind: kind, Results: items, Total: total, Truncated: total > len(items), Limit: limit, ownerAuthorID: ownerAuthorID}
}

func newOpenEnvelope(result store.OpenResult, aliases map[string]string, ownerAuthorID string) openEnvelope {
	replyingTo, replyingToAuthorID := openReplyingTo(result.Tweet, result.Ancestors)
	return openEnvelope{
		Ref:                store.TweetRef(result.Tweet.ID),
		Tweet:              newOpenTweet(result.Tweet, aliases, replyingTo, replyingToAuthorID),
		Ancestors:          newAncestorTweets(result.Ancestors, aliases),
		Replies:            newOpenTweets(result.Replies, aliases),
		AncestorsTruncated: result.AncestorsTruncated,
		RepliesTruncated:   result.RepliesTruncated,
		ownerAuthorID:      ownerAuthorID,
	}
}

func newOpenTweets(tweets []store.Tweet, aliases map[string]string) []openTweet {
	out := make([]openTweet, 0, len(tweets))
	for _, tweet := range tweets {
		out = append(out, newOpenTweet(tweet, aliases, "", ""))
	}
	return out
}

func newAncestorTweets(tweets []store.OpenTweet, aliases map[string]string) []openTweet {
	out := make([]openTweet, 0, len(tweets))
	for _, tweet := range tweets {
		if !tweet.Available {
			out = append(out, openTweet{Ref: tweet.Ref, Text: tweet.Text, Unavailable: true})
			continue
		}
		out = append(out, newOpenTweet(tweet.Tweet, aliases, "", ""))
	}
	return out
}

func newOpenTweet(tweet store.Tweet, aliases map[string]string, replyingTo string, replyingToAuthorID string) openTweet {
	ref := store.TweetRef(tweet.ID)
	return openTweet{
		Ref:          ref,
		Time:         formatOptionalTime(tweet.CreatedAt),
		Who:          store.DisplayName(tweet.AuthorName, tweet.AuthorHandle),
		Text:         tweet.Text,
		InReplyTo:    tweet.InReplyToID,
		LikeCount:    tweet.LikeCount,
		RetweetCount: tweet.RetweetCount,
		ReplyCount:   tweet.ReplyCount,
		CountsAsOf:   formatOptionalTime(tweet.MetricsFetchedAt),
		// X exports retweets as truncated "RT @..." stubs. This is a
		// mechanical prefix check only; it does not infer tweet meaning.
		Note:               retweetStubNoteForText(tweet.Text),
		Conversation:       tweet.ConversationID,
		QuotedTweetID:      tweet.QuotedTweetID,
		ShortRef:           aliases[ref],
		ReplyingTo:         replyingTo,
		authorID:           tweet.AuthorID,
		replyingToAuthorID: replyingToAuthorID,
		timeValue:          tweet.CreatedAt,
		countsAsOfTime:     tweet.MetricsFetchedAt,
	}
}

func openReplyingTo(tweet store.Tweet, ancestors []store.OpenTweet) (string, string) {
	if tweet.InReplyToID == "" {
		return "", ""
	}
	for _, ancestor := range ancestors {
		if ancestor.Available && ancestor.Tweet.ID == tweet.InReplyToID {
			return store.DisplayName(ancestor.Tweet.AuthorName, ancestor.Tweet.AuthorHandle), ancestor.Tweet.AuthorID
		}
	}
	return "unavailable tweet", ""
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
