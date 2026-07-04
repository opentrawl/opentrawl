package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"
)

type SearchFilter struct {
	Query  string
	Limit  int
	After  *time.Time
	Before *time.Time
}

type SearchResult struct {
	Tweet
	Snippet string
	Who     string
	Where   string
}

func (s *Store) Search(ctx context.Context, filter SearchFilter) ([]SearchResult, int, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return nil, 0, errors.New("search query required")
	}
	if filter.Limit <= 0 {
		filter.Limit = 20
	}
	queryText, err := ckstore.FTS5Terms(filter.Query, "")
	if err != nil {
		return nil, 0, err
	}
	where, args := searchWhere(filter, "t.")
	countQuery := `select count(*) from tweets_fts f join tweets t on t.rowid = f.rowid where tweets_fts match ?` + where
	countArgs := append([]any{queryText}, args...)
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	selectQuery := `select ` + tweetSelectColumns("t") + `,
coalesce(p.author_name,''), coalesce(p.author_handle,'')
from tweets_fts f
join tweets t on t.rowid = f.rowid
left join tweets p on p.id = t.in_reply_to_id
where tweets_fts match ?` + where + `
order by t.created_at desc, t.id desc limit ?`
	args = append(countArgs, filter.Limit)
	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		tweet, parentName, parentHandle, err := scanTweetWithParent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, SearchResult{
			Tweet:   tweet,
			Snippet: ckstore.FTS5Snippet(tweet.Text, filter.Query),
			Who:     DisplayName(tweet.AuthorName, tweet.AuthorHandle),
			Where:   replyWhere(tweet.InReplyToID, parentName, parentHandle),
		})
	}
	return out, total, rows.Err()
}

func searchWhere(filter SearchFilter, prefix string) (string, []any) {
	var clauses []string
	var args []any
	if filter.After != nil {
		clauses = append(clauses, prefix+"created_at >= ?")
		args = append(args, formatUTC(*filter.After))
	}
	if filter.Before != nil {
		clauses = append(clauses, prefix+"created_at <= ?")
		args = append(args, formatUTC(*filter.Before))
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " and " + strings.Join(clauses, " and "), args
}

func replyWhere(replyID, parentName, parentHandle string) string {
	if strings.TrimSpace(replyID) == "" {
		return "X"
	}
	name := DisplayName(parentName, parentHandle)
	if name == "" {
		return "X"
	}
	return "reply to " + name
}

func DisplayName(name, handle string) string {
	name = strings.TrimSpace(name)
	handle = strings.TrimPrefix(strings.TrimSpace(handle), "@")
	switch {
	case name != "" && handle != "":
		return fmt.Sprintf("%s (@%s)", name, handle)
	case name != "":
		return name
	case handle != "":
		return "@" + handle
	default:
		// Authorless rows exist only one way: the official X export lists
		// liked tweets without any author fields (verified: all 41k
		// authorless rows in the reference archive carry the like role).
		// Say that, rather than "unknown author", which reads as a defect.
		return "liked · author not in export"
	}
}

func tweetSelectColumns(alias string) string {
	return alias + `.id,` + alias + `.created_at,coalesce(` + alias + `.author_id,''),coalesce(` + alias + `.author_handle,''),
coalesce(` + alias + `.author_name,''),coalesce(` + alias + `.text,''),coalesce(` + alias + `.in_reply_to_id,''),
coalesce(` + alias + `.conversation_id,''),coalesce(` + alias + `.quoted_tweet_id,''),` + alias + `.like_count,
` + alias + `.retweet_count,` + alias + `.reply_count,` + alias + `.view_count,` + alias + `.quote_count,
` + alias + `.bookmark_count,` + alias + `.has_media,coalesce(` + alias + `.raw_json,''),` + alias + `.first_source,
coalesce(` + alias + `.metrics_fetched_at,'')`
}

type tweetScanner interface {
	Scan(dest ...any) error
}

func scanTweet(scanner tweetScanner) (Tweet, error) {
	var t Tweet
	var createdAt, metricsAt string
	var hasMedia int
	err := scanner.Scan(&t.ID, &createdAt, &t.AuthorID, &t.AuthorHandle, &t.AuthorName, &t.Text,
		&t.InReplyToID, &t.ConversationID, &t.QuotedTweetID, &t.LikeCount, &t.RetweetCount,
		&t.ReplyCount, &t.ViewCount, &t.QuoteCount, &t.BookmarkCount, &hasMedia, &t.RawJSON,
		&t.FirstSource, &metricsAt)
	if err != nil {
		return Tweet{}, err
	}
	t.CreatedAt = parseStoredTime(createdAt)
	t.MetricsFetchedAt = parseStoredTime(metricsAt)
	t.HasMedia = hasMedia != 0
	return t, nil
}

func scanTweetWithParent(scanner tweetScanner) (Tweet, string, string, error) {
	var t Tweet
	var createdAt, metricsAt, parentName, parentHandle string
	var hasMedia int
	err := scanner.Scan(&t.ID, &createdAt, &t.AuthorID, &t.AuthorHandle, &t.AuthorName, &t.Text,
		&t.InReplyToID, &t.ConversationID, &t.QuotedTweetID, &t.LikeCount, &t.RetweetCount,
		&t.ReplyCount, &t.ViewCount, &t.QuoteCount, &t.BookmarkCount, &hasMedia, &t.RawJSON,
		&t.FirstSource, &metricsAt, &parentName, &parentHandle)
	if err != nil {
		return Tweet{}, "", "", err
	}
	t.CreatedAt = parseStoredTime(createdAt)
	t.MetricsFetchedAt = parseStoredTime(metricsAt)
	t.HasMedia = hasMedia != 0
	return t, parentName, parentHandle, nil
}

func (s *Store) tweetByID(ctx context.Context, id string) (Tweet, error) {
	row := s.db.QueryRowContext(ctx, `select `+tweetSelectColumns("t")+` from tweets t where t.id = ?`, id)
	tweet, err := scanTweet(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Tweet{}, ErrTweetNotFound
	}
	return tweet, err
}
