package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const defaultStatsWindow = 30 * 24 * time.Hour

type StatsFilter struct {
	Window time.Duration
	By     string
	Limit  int
	Now    time.Time
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
	Text       string    `json:"text"`
	Count      int64     `json:"count"`
	CountsAsOf time.Time `json:"counts_as_of,omitzero"`
}

func (s *Store) Stats(ctx context.Context, filter StatsFilter) (StatsResult, error) {
	if filter.Limit <= 0 {
		filter.Limit = 10
	}
	if filter.Window <= 0 {
		filter.Window = defaultStatsWindow
	}
	if filter.Now.IsZero() {
		filter.Now = time.Now().UTC()
	}
	metric, err := statsMetric(filter.By)
	if err != nil {
		return StatsResult{}, err
	}
	since := formatUTC(filter.Now.Add(-filter.Window))
	out := StatsResult{By: metric.label, Window: filter.Window}
	// Stats rank the owner's own tweets: engagement counts on liked or
	// bookmarked tweets belong to their authors, not to this archive's owner.
	if err := s.db.QueryRowContext(ctx, `select count(*), count(metrics_fetched_at), count(*) - count(metrics_fetched_at),
coalesce(min(metrics_fetched_at), ''), coalesce(max(metrics_fetched_at), '')
from tweets
where created_at >= ? and id in (select tweet_id from tweet_roles where role = 'authored')`, since).Scan(&out.Population, &out.PopulationWithCounts, &out.CountsMissing, newTimeScanner(&out.FreshnessOldest), newTimeScanner(&out.FreshnessNewest)); err != nil {
		return StatsResult{}, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`select %s, %s from tweets t
where t.created_at >= ? and t.id in (select tweet_id from tweet_roles where role = 'authored')
order by %s desc, t.created_at desc, t.id desc
limit ?`, tweetSelectColumns("t"), metric.column, metric.column), since, filter.Limit)
	if err != nil {
		return StatsResult{}, err
	}
	defer rows.Close()
	for rows.Next() {
		tweet, count, err := scanStatsRow(rows)
		if err != nil {
			return StatsResult{}, err
		}
		out.Rows = append(out.Rows, StatsRow{
			Ref:        TweetRef(tweet.ID),
			Time:       tweet.CreatedAt,
			Who:        DisplayName(tweet.AuthorName, tweet.AuthorHandle),
			Text:       tweet.Text,
			Count:      count,
			CountsAsOf: tweet.MetricsFetchedAt,
		})
	}
	return out, rows.Err()
}

type metricColumn struct {
	label  string
	column string
}

func statsMetric(value string) (metricColumn, error) {
	switch strings.TrimSpace(value) {
	case "", "likes":
		return metricColumn{label: "likes", column: "t.like_count"}, nil
	case "retweets":
		return metricColumn{label: "retweets", column: "t.retweet_count"}, nil
	case "replies":
		return metricColumn{label: "replies", column: "t.reply_count"}, nil
	default:
		return metricColumn{}, fmt.Errorf("unknown stats metric %q", value)
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
