package store

import (
	"context"
	"errors"
)

const (
	openAncestorLimit = 3
	openReplyLimit    = 20
)

type OpenResult struct {
	Tweet              Tweet
	Ancestors          []OpenTweet
	Replies            []Tweet
	AncestorsTruncated bool
	RepliesTruncated   bool
}

type OpenTweet struct {
	Tweet     Tweet
	Available bool
	Ref       string
	Text      string
}

func (s *Store) OpenTweet(ctx context.Context, id string) (OpenResult, error) {
	tweet, err := s.tweetByID(ctx, id)
	if err != nil {
		return OpenResult{}, err
	}
	ancestors, truncated, err := s.ancestors(ctx, tweet)
	if err != nil {
		return OpenResult{}, err
	}
	replies, repliesTruncated, err := s.replies(ctx, id)
	if err != nil {
		return OpenResult{}, err
	}
	return OpenResult{Tweet: tweet, Ancestors: ancestors, Replies: replies, AncestorsTruncated: truncated, RepliesTruncated: repliesTruncated}, nil
}

func (s *Store) ancestors(ctx context.Context, tweet Tweet) ([]OpenTweet, bool, error) {
	var reversed []OpenTweet
	nextID := tweet.InReplyToID
	truncated := false
	for i := 0; nextID != "" && i < openAncestorLimit; i++ {
		parent, err := s.tweetByID(ctx, nextID)
		if errors.Is(err, ErrTweetNotFound) {
			reversed = append(reversed, OpenTweet{Available: false, Ref: TweetRef(nextID), Text: "unavailable (not in archive)"})
			break
		}
		if err != nil {
			return nil, false, err
		}
		reversed = append(reversed, OpenTweet{Tweet: parent, Available: true, Ref: TweetRef(parent.ID), Text: parent.Text})
		nextID = parent.InReplyToID
		if nextID != "" && i == openAncestorLimit-1 {
			truncated = true
		}
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return reversed, truncated, nil
}

func (s *Store) replies(ctx context.Context, id string) ([]Tweet, bool, error) {
	rows, err := s.db.QueryContext(ctx, `select `+tweetSelectColumns("t")+` from tweets t
where t.in_reply_to_id = ?
order by t.created_at asc, t.id asc
limit ?`, id, openReplyLimit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	var out []Tweet
	for rows.Next() {
		tweet, err := scanTweet(rows)
		if err != nil {
			return nil, false, err
		}
		out = append(out, tweet)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(out) > openReplyLimit
	if truncated {
		out = out[:openReplyLimit]
	}
	return out, truncated, nil
}
