package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

type SearchFilter struct {
	Query  string
	Limit  int
	After  *time.Time
	Before *time.Time
}

type ListFilter struct {
	Limit  int
	After  *time.Time
	Before *time.Time
}

type SearchResult struct {
	Tweet
	Snippet           string
	Who               string
	Where             string
	InReplyTo         string
	InReplyToAuthorID string
	Roles             []string
}

func (s *Store) Search(ctx context.Context, filter SearchFilter) ([]SearchResult, int, error) {
	if strings.TrimSpace(filter.Query) == "" {
		return nil, 0, errors.New("search query required")
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
coalesce(p.author_id,''), coalesce(p.author_name,''), coalesce(p.author_handle,''),
coalesce((select group_concat(role, char(10)) from (select role from tweet_roles where tweet_id = t.id order by role)), '')
from tweets_fts f
join tweets t on t.rowid = f.rowid
left join tweets p on p.id = t.in_reply_to_id
where tweets_fts match ?` + where + `
order by t.created_at desc, t.id desc limit ?`
	args = append(countArgs, limitArg(filter.Limit))
	rows, err := s.db.QueryContext(ctx, selectQuery, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []SearchResult
	for rows.Next() {
		tweet, parentID, parentName, parentHandle, roleList, err := scanTweetWithParentAndRoles(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, SearchResult{
			Tweet:             tweet,
			Snippet:           ckstore.FTS5Snippet(tweet.Text, filter.Query),
			Who:               DisplayName(tweet.AuthorName, tweet.AuthorHandle),
			Where:             replyWhere(tweet.InReplyToID, parentName, parentHandle),
			InReplyTo:         replyDisplay(tweet.InReplyToID, parentName, parentHandle),
			InReplyToAuthorID: parentID,
			Roles:             splitRoleList(roleList),
		})
	}
	return out, total, rows.Err()
}

func scanTweetWithParentAndRoles(rows *sql.Rows) (Tweet, string, string, string, string, error) {
	var t Tweet
	var createdAt, metricsAt, parentID, parentName, parentHandle string
	var roleList string
	var hasMedia int
	err := rows.Scan(&t.ID, &createdAt, &t.AuthorID, &t.AuthorHandle, &t.AuthorName, &t.Text,
		&t.InReplyToID, &t.ConversationID, &t.QuotedTweetID, &t.LikeCount, &t.RetweetCount,
		&t.ReplyCount, &t.ViewCount, &t.QuoteCount, &t.BookmarkCount, &hasMedia, &t.RawJSON,
		&t.FirstSource, &metricsAt, &parentID, &parentName, &parentHandle, &roleList)
	if err != nil {
		return Tweet{}, "", "", "", "", err
	}
	t.CreatedAt = parseStoredTime(createdAt)
	t.MetricsFetchedAt = parseStoredTime(metricsAt)
	t.HasMedia = hasMedia != 0
	return t, parentID, parentName, parentHandle, roleList, nil
}

func splitRoleList(value string) []string {
	parts := strings.Split(value, "\n")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (s *Store) ListByRole(ctx context.Context, role string, filter ListFilter) ([]SearchResult, int, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return nil, 0, errors.New("role required")
	}
	where, whereArgs := searchWhere(SearchFilter{After: filter.After, Before: filter.Before}, "t.")
	countArgs := append([]any{role}, whereArgs...)
	var total int
	if err := s.db.QueryRowContext(ctx, `select count(*)
from tweets t
join tweet_roles r on r.tweet_id = t.id
where r.role = ?`+where, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args := append(countArgs, limitArg(filter.Limit))
	rows, err := s.db.QueryContext(ctx, `select `+tweetSelectColumns("t")+`,
coalesce(p.author_id,''), coalesce(p.author_name,''), coalesce(p.author_handle,'')
from tweets t
join tweet_roles r on r.tweet_id = t.id
left join tweets p on p.id = t.in_reply_to_id
where r.role = ?`+where+`
order by t.created_at desc, t.id desc
limit ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var out []SearchResult
	for rows.Next() {
		tweet, parentID, parentName, parentHandle, err := scanTweetWithParent(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, SearchResult{
			Tweet:             tweet,
			Who:               DisplayName(tweet.AuthorName, tweet.AuthorHandle),
			Where:             replyWhere(tweet.InReplyToID, parentName, parentHandle),
			InReplyTo:         replyDisplay(tweet.InReplyToID, parentName, parentHandle),
			InReplyToAuthorID: parentID,
		})
	}
	return out, total, rows.Err()
}

// limitArg maps a resolved limit to SQLite's LIMIT argument: 0 means return
// everything, which SQLite spells -1.
func limitArg(limit int) int {
	if limit <= 0 {
		return -1
	}
	return limit
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
		return ""
	}
	name := replyDisplay(replyID, parentName, parentHandle)
	if name == "" {
		return "reply"
	}
	return "reply to " + name
}

func replyDisplay(replyID, parentName, parentHandle string) string {
	if strings.TrimSpace(replyID) == "" {
		return ""
	}
	if strings.TrimSpace(parentName) == "" && strings.TrimSpace(parentHandle) == "" {
		// The parent row exists but the export carries no author for it
		// (authorless liked rows); an arrow to a nameless target reads as
		// a defect, so render the author alone.
		return ""
	}
	return DisplayName(parentName, parentHandle)
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

func scanTweetWithParent(scanner tweetScanner) (Tweet, string, string, string, error) {
	var t Tweet
	var createdAt, metricsAt, parentID, parentName, parentHandle string
	var hasMedia int
	err := scanner.Scan(&t.ID, &createdAt, &t.AuthorID, &t.AuthorHandle, &t.AuthorName, &t.Text,
		&t.InReplyToID, &t.ConversationID, &t.QuotedTweetID, &t.LikeCount, &t.RetweetCount,
		&t.ReplyCount, &t.ViewCount, &t.QuoteCount, &t.BookmarkCount, &hasMedia, &t.RawJSON,
		&t.FirstSource, &metricsAt, &parentID, &parentName, &parentHandle)
	if err != nil {
		return Tweet{}, "", "", "", err
	}
	t.CreatedAt = parseStoredTime(createdAt)
	t.MetricsFetchedAt = parseStoredTime(metricsAt)
	t.HasMedia = hasMedia != 0
	return t, parentID, parentName, parentHandle, nil
}

func (s *Store) tweetByID(ctx context.Context, id string) (Tweet, error) {
	row := s.db.QueryRowContext(ctx, `select `+tweetSelectColumns("t")+` from tweets t where t.id = ?`, id)
	tweet, err := scanTweet(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Tweet{}, ErrTweetNotFound
	}
	return tweet, err
}
