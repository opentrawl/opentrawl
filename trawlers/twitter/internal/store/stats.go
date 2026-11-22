package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const defaultStatsWindow = 30 * 24 * time.Hour

type StatsWindow struct {
	AllTime  bool
	Duration time.Duration
}

type StatsFilter struct {
	Window StatsWindow
	Metric TweetStatsRankingMetric
	Limit  int
	Now    time.Time
}

type TweetStatsRankingMetric struct {
	label  string
	column string
}

type StatsResult struct {
	By                   string
	Window               time.Duration
	FreshnessOldest      time.Time
	FreshnessNewest      time.Time
	CountsMissing        int
	Rows                 []StatsRow
	Population           int
	PopulationWithCounts int
}

type StatsRow struct {
	Ref        string    `json:"ref"`
	Time       time.Time `json:"time,omitzero"`
	Who        string    `json:"who"`
	AuthorID   string    `json:"author_id,omitempty"`
	Text       string    `json:"text"`
	Count      int64     `json:"count"`
	CountsAsOf time.Time `json:"counts_as_of,omitzero"`
}

func (s *Store) Stats(ctx context.Context, filter StatsFilter) (StatsResult, error) {
	if filter.Limit <= 0 {
		filter.Limit = 10
	}
	if !filter.Window.AllTime && filter.Window.Duration <= 0 {
		filter.Window.Duration = defaultStatsWindow
	}
	if filter.Now.IsZero() {
		filter.Now = time.Now().UTC()
	}
	out := StatsResult{By: filter.Metric.label, Window: filter.Window.Duration}
	whereClause := "t.id in (select tweet_id from tweet_roles where role = 'authored') and t.text not like 'RT @%'"
	var whereArgs []any
	if !filter.Window.AllTime {
		whereClause = "t.created_at >= ? and " + whereClause
		whereArgs = append(whereArgs, formatUTC(filter.Now.Add(-filter.Window.Duration)))
	}
	// Stats rank the owner's own tweets: engagement counts on liked or
	// bookmarked tweets belong to their authors, not to this archive's owner.
	if err := s.db.QueryRowContext(ctx, `select count(*), count(t.metrics_fetched_at), count(*) - count(t.metrics_fetched_at),
coalesce(min(t.metrics_fetched_at), ''), coalesce(max(t.metrics_fetched_at), '')
from tweets t
where `+whereClause, whereArgs...).Scan(&out.Population, &out.PopulationWithCounts, &out.CountsMissing, newTimeScanner(&out.FreshnessOldest), newTimeScanner(&out.FreshnessNewest)); err != nil {
		return StatsResult{}, err
	}
	selectArgs := append(append([]any(nil), whereArgs...), filter.Limit)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`select %s, %s from tweets t
where %s
order by %s desc, t.created_at desc, t.id desc
limit ?`, tweetSelectColumns("t"), filter.Metric.column, whereClause, filter.Metric.column), selectArgs...)
	if err != nil {
		return StatsResult{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		tweet, count, err := scanStatsRow(rows)
		if err != nil {
			return StatsResult{}, err
		}
		out.Rows = append(out.Rows, StatsRow{
			Ref:        TweetRef(tweet.ID),
			Time:       tweet.CreatedAt,
			Who:        DisplayName(tweet.AuthorName, tweet.AuthorHandle),
			AuthorID:   tweet.AuthorID,
			Text:       tweet.Text,
			Count:      count,
			CountsAsOf: tweet.MetricsFetchedAt,
		})
	}
	return out, rows.Err()
}

func ParseTweetStatsRankingMetric(value string) (TweetStatsRankingMetric, error) {
	switch strings.TrimSpace(value) {
	case "", "likes":
		return TweetStatsRankingMetric{label: "likes", column: "t.like_count"}, nil
	case "retweets":
		return TweetStatsRankingMetric{label: "retweets", column: "t.retweet_count"}, nil
	case "replies":
		return TweetStatsRankingMetric{label: "replies", column: "t.reply_count"}, nil
	default:
		return TweetStatsRankingMetric{}, fmt.Errorf("unknown tweet stats ranking metric %q", value)
	}
}

func scanStatsRow(scanner tweetScanner) (Tweet, int64, error) {
	var t Tweet
	var createdAt, metricsAt string
	var hasMedia int
	var count int64
	err := scanner.Scan(&t.ID, &createdAt, &t.AuthorID, &t.AuthorHandle, &t.AuthorName, &t.Text,
		&t.InReplyToID, &t.ConversationID, &t.QuotedTweetID, &t.LikeCount, &t.RetweetCount,
		&t.ReplyCount, &t.ViewCount, &t.QuoteCount, &t.BookmarkCount, &hasMedia, &t.RawJSON,
		&t.FirstSource, &metricsAt, &count)
	if err != nil {
		return Tweet{}, 0, err
	}
	t.CreatedAt = parseStoredTime(createdAt)
	t.MetricsFetchedAt = parseStoredTime(metricsAt)
	t.HasMedia = hasMedia != 0
	return t, count, nil
}

type timeScanner struct {
	dst *time.Time
}

func newTimeScanner(dst *time.Time) *timeScanner {
	return &timeScanner{dst: dst}
}

func (s *timeScanner) Scan(value any) error {
	switch v := value.(type) {
	case string:
		*s.dst = parseStoredTime(v)
	case []byte:
		*s.dst = parseStoredTime(string(v))
	case nil:
		*s.dst = time.Time{}
	}
	return nil
}
