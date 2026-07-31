package twitter

import (
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/twitter/internal/store"
	"github.com/opentrawl/opentrawl/twitter/internal/xapi"
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
	readiness    archiveReadiness  `json:"-"`
}

type archiveReadiness string

const (
	archiveReadinessMissing archiveReadiness = "missing"
	archiveReadinessReady   archiveReadiness = "ready"
	archiveReadinessInvalid archiveReadiness = "invalid"
)

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

type listEnvelope struct {
	Results   []listResult
	Total     int
	Truncated bool
}

type listResult struct {
	Ref       string
	Who       string
	Text      string
	timeValue time.Time
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
	By         string
	Population int
	Results    []statsRow
}

type statsRow struct {
	Ref       string
	Text      string
	Count     int64
	timeValue time.Time
}

func (r *runtime) statusEnvelope() statusEnvelope {
	cfg, err := loadBirdConfig(r.configPath)
	if err != nil {
		cfg = birdConfig{MonthlyBudgetMicros: defaultMonthlyBudgetUSDMicros}
	}
	if r.req.OpenedTrawlerArchiveStore == nil {
		envelope := r.newStatusEnvelope("missing", "archive is missing; import an X archive dump", "archive is missing; import an X archive dump", store.Status{}, cfg)
		envelope.readiness = archiveReadinessMissing
		return envelope
	}
	st, err := store.UseExisting(r.ctx, r.req.OpenedTrawlerArchiveStore, r.req.TrawlerCommandLog)
	if err != nil {
		envelope := r.newStatusEnvelope("error", "archive database cannot be read", "archive database cannot be read", store.Status{}, cfg)
		envelope.readiness = archiveReadinessInvalid
		return envelope
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		envelope := r.newStatusEnvelope("error", "archive status cannot be read", "archive status cannot be read", store.Status{}, cfg)
		envelope.readiness = archiveReadinessInvalid
		return envelope
	}
	envelope := r.newStatusEnvelope(statusState(status), statusSummary(status, formatLocalTime), statusSummary(status, formatHumanLocalTime), status, cfg)
	if archiveReady(status) {
		envelope.readiness = archiveReadinessReady
	} else {
		envelope.readiness = archiveReadinessMissing
	}
	return envelope
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
		AppID:        "twitter",
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
	if !archiveReady(status) {
		return "empty"
	}
	return "ok"
}

func statusSummary(status store.Status, formatTime func(time.Time) string) string {
	if status.Tweets == 0 {
		return "archive is empty; import an X archive dump"
	}
	if status.LastImportAt.IsZero() {
		return "local X data exists; import an X archive dump to establish archive readiness"
	}
	live := ""
	switch {
	case status.LastLiveSync.IsZero():
		live = "live update has not run"
	case strings.HasPrefix(status.LiveSyncResult, "partial"):
		live = "last live update at " + formatTime(status.LastLiveSync) + " was " + status.LiveSyncResult
	default:
		live = "last updated at " + formatTime(status.LastLiveSync)
	}
	if !status.CoverageThrough.IsZero() {
		return "archive dump imported through " + formatTime(status.CoverageThrough) + "; " + live
	}
	return "archive has local X data; " + live
}

func archiveReady(status store.Status) bool {
	return status.Tweets > 0 && !status.LastImportAt.IsZero()
}

func newListEnvelope(results []store.SearchResult, total int, ownerAuthorID string) listEnvelope {
	items := make([]listResult, 0, len(results))
	for _, result := range results {
		ref := store.TweetRef(result.ID)
		items = append(items, listResult{
			Ref:       ref,
			Who:       postAuthorDisplayName(result.Who, result.AuthorID, ownerAuthorID),
			Text:      result.Text,
			timeValue: result.CreatedAt,
		})
	}
	return listEnvelope{Results: items, Total: total, Truncated: total > len(items)}
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
