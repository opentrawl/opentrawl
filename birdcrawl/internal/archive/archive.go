package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

const legacyTwitterTime = "Mon Jan 02 15:04:05 -0700 2006"

type Importer struct {
	Now func() time.Time
}

type Result struct {
	Stats store.ImportStats
}

func (i Importer) Import(ctx context.Context, st *store.Store, path string) (Result, error) {
	started := i.now()
	files, err := readDumpFiles(path)
	if err != nil {
		return Result{}, err
	}
	tweets, roles, profiles, counters, err := parseDump(files, started)
	if err != nil {
		return Result{}, err
	}
	stats, err := st.ImportArchive(ctx, store.ImportBatch{
		Tweets:          tweets,
		Roles:           roles,
		Profiles:        profiles,
		CoverageThrough: newestTweetTime(tweets),
		ImportedAt:      started,
	})
	if err != nil {
		return Result{}, err
	}
	stats.NoteTweetsMerged = counters.noteTweetsMerged
	stats.NoteTweetsUnmatched = counters.noteTweetsUnmatched
	stats.LikesWithoutText = counters.likesWithoutText
	stats.StartedAt = started
	stats.FinishedAt = i.now()
	return Result{Stats: stats}, nil
}

func (i Importer) now() time.Time {
	if i.Now != nil {
		return i.Now().UTC()
	}
	return time.Now().UTC()
}

type dumpFiles struct {
	tweets     []byte
	likes      []byte
	account    []byte
	noteTweets []byte
}

func readDumpFiles(path string) (dumpFiles, error) {
	info, err := os.Stat(path)
	if err != nil {
		return dumpFiles{}, err
	}
	if info.IsDir() {
		return readDumpDir(path)
	}
	return readDumpZip(path)
}

func readDumpDir(root string) (dumpFiles, error) {
	var files dumpFiles
	for name, dest := range dumpFileTargets(&files) {
		for _, rel := range []string{name, filepath.Join("data", name)} {
			if data, err := os.ReadFile(filepath.Join(root, rel)); err == nil {
				*dest = data
				break
			}
		}
	}
	return requireDumpFiles(files)
}

func dumpFileTargets(files *dumpFiles) map[string]*[]byte {
	return map[string]*[]byte{
		"tweets.js":     &files.tweets,
		"like.js":       &files.likes,
		"account.js":    &files.account,
		"note-tweet.js": &files.noteTweets,
	}
}

func readDumpZip(path string) (dumpFiles, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return dumpFiles{}, err
	}
	defer func() { _ = reader.Close() }()
	var files dumpFiles
	targets := dumpFileTargets(&files)
	for _, file := range reader.File {
		dest, ok := targets[filepath.Base(file.Name)]
		if !ok {
			continue
		}
		data, err := readZipFile(file)
		if err != nil {
			return dumpFiles{}, err
		}
		*dest = data
	}
	return requireDumpFiles(files)
}

func readZipFile(file *zip.File) ([]byte, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func requireDumpFiles(files dumpFiles) (dumpFiles, error) {
	if len(files.tweets) == 0 {
		return dumpFiles{}, errors.New("tweets.js not found in archive dump")
	}
	if len(files.likes) == 0 {
		return dumpFiles{}, errors.New("like.js not found in archive dump")
	}
	if len(files.account) == 0 {
		return dumpFiles{}, errors.New("account.js not found in archive dump; authored tweets cannot be attributed without it")
	}
	return files, nil
}

type parseCounters struct {
	noteTweetsMerged    int
	noteTweetsUnmatched int
	likesWithoutText    int
}

func parseDump(files dumpFiles, importedAt time.Time) ([]store.Tweet, []store.Role, []store.Profile, parseCounters, error) {
	var counters parseCounters
	acct, err := parseAccount(files.account)
	if err != nil {
		return nil, nil, nil, counters, err
	}
	authored, roles, err := parseTweets(files.tweets, importedAt)
	if err != nil {
		return nil, nil, nil, counters, err
	}
	notes, err := parseNoteTweets(files.noteTweets)
	if err != nil {
		return nil, nil, nil, counters, err
	}
	counters.noteTweetsMerged, counters.noteTweetsUnmatched = mergeNoteTweets(authored, notes)
	stampOwner(authored, acct)
	liked, likeRoles, hollow, err := parseLikes(files.likes, importedAt)
	if err != nil {
		return nil, nil, nil, counters, err
	}
	counters.likesWithoutText = hollow
	if len(liked) > 0 && hollow*2 > len(liked) {
		return nil, nil, nil, counters, fmt.Errorf(
			"like.js looks deficient: %d of %d rows carry no text; refusing to import hollow rows", hollow, len(liked))
	}
	tweets := append(authored, liked...)
	roles = append(roles, likeRoles...)
	profiles := []store.Profile{{AuthorID: acct.ID, Handle: acct.Handle, DisplayName: acct.Name, LastSeenAt: importedAt}}
	return dedupeTweets(tweets), dedupeRoles(roles), dedupeProfiles(profiles), counters, nil
}

// stampOwner sets identity and count freshness on authored rows: tweets.js has
// no author fields (the dump is implicitly the owner's), and its engagement
// counts are as of dump generation, for which the newest authored tweet is the
// provable lower bound.
func stampOwner(authored []store.Tweet, acct owner) {
	var newest time.Time
	for _, tweet := range authored {
		if tweet.CreatedAt.After(newest) {
			newest = tweet.CreatedAt
		}
	}
	for i := range authored {
		authored[i].AuthorID = acct.ID
		authored[i].AuthorHandle = acct.Handle
		authored[i].AuthorName = acct.Name
		authored[i].MetricsFetchedAt = newest
	}
}

func unwrapYTD(data []byte) ([]byte, error) {
	data = bytes.TrimSpace(data)
	start := bytes.IndexByte(data, '[')
	if start < 0 {
		start = bytes.IndexByte(data, '{')
	}
	if start < 0 {
		return nil, errors.New("archive JavaScript wrapper did not contain JSON")
	}
	data = bytes.TrimSpace(data[start:])
	data = bytes.TrimSuffix(data, []byte(";"))
	return data, nil
}

type tweetWrapper struct {
	Tweet rawTweet `json:"tweet"`
}

type rawTweet struct {
	ID               string      `json:"id"`
	IDStr            string      `json:"id_str"`
	CreatedAt        string      `json:"created_at"`
	CreatedAtCamel   string      `json:"createdAt"`
	FullText         string      `json:"full_text"`
	FullTextCamel    string      `json:"fullText"`
	Text             string      `json:"text"`
	InReplyToID      string      `json:"in_reply_to_status_id"`
	InReplyToIDStr   string      `json:"in_reply_to_status_id_str"`
	ConversationID   string      `json:"conversation_id"`
	QuotedTweetID    string      `json:"quoted_status_id"`
	QuotedTweetIDStr string      `json:"quoted_status_id_str"`
	FavouriteCount   jsonNumber  `json:"favorite_count"`
	RetweetCount     jsonNumber  `json:"retweet_count"`
	ReplyCount       jsonNumber  `json:"reply_count"`
	ViewCount        jsonNumber  `json:"view_count"`
	QuoteCount       jsonNumber  `json:"quote_count"`
	BookmarkCount    jsonNumber  `json:"bookmark_count"`
	ExtendedEntities any         `json:"extended_entities"`
	Entities         rawEntities `json:"entities"`
}

type rawEntities struct {
	Media []any `json:"media"`
}

func parseTweets(data []byte, importedAt time.Time) ([]store.Tweet, []store.Role, error) {
	body, err := unwrapYTD(data)
	if err != nil {
		return nil, nil, err
	}
	var wrapped []tweetWrapper
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, nil, fmt.Errorf("parse tweets.js: %w", err)
	}
	tweets := make([]store.Tweet, 0, len(wrapped))
	roles := make([]store.Role, 0, len(wrapped))
	for _, item := range wrapped {
		tweet, err := item.Tweet.toTweet()
		if err != nil {
			return nil, nil, err
		}
		if tweet.ID == "" {
			continue
		}
		tweets = append(tweets, tweet)
		roles = append(roles, store.Role{TweetID: tweet.ID, Role: "authored", FirstSeenAt: importedAt, LastSeenAt: importedAt})
	}
	return tweets, roles, nil
}

func (r rawTweet) toTweet() (store.Tweet, error) {
	id := firstNonEmpty(r.IDStr, r.ID)
	createdAt, err := parseTweetTime(firstNonEmpty(r.CreatedAt, r.CreatedAtCamel))
	if err != nil {
		return store.Tweet{}, err
	}
	tweet := store.Tweet{
		ID:             id,
		CreatedAt:      createdAt,
		Text:           html.UnescapeString(firstNonEmpty(r.FullText, r.FullTextCamel, r.Text)),
		InReplyToID:    firstNonEmpty(r.InReplyToIDStr, r.InReplyToID),
		ConversationID: r.ConversationID,
		QuotedTweetID:  firstNonEmpty(r.QuotedTweetIDStr, r.QuotedTweetID),
		LikeCount:      r.FavouriteCount.Int64(),
		RetweetCount:   r.RetweetCount.Int64(),
		ReplyCount:     r.ReplyCount.Int64(),
		ViewCount:      r.ViewCount.Int64(),
		QuoteCount:     r.QuoteCount.Int64(),
		BookmarkCount:  r.BookmarkCount.Int64(),
		HasMedia:       len(r.Entities.Media) > 0 || r.ExtendedEntities != nil,
		FirstSource:    "archive",
	}
	return tweet, nil
}

type likeWrapper struct {
	Like rawLike `json:"like"`
}

type rawLike struct {
	TweetID   string `json:"tweetId"`
	FullText  string `json:"fullText"`
	CreatedAt string `json:"createdAt"`
}

func parseLikes(data []byte, importedAt time.Time) ([]store.Tweet, []store.Role, int, error) {
	body, err := unwrapYTD(data)
	if err != nil {
		return nil, nil, 0, err
	}
	var wrapped []likeWrapper
	if err := json.Unmarshal(body, &wrapped); err != nil {
		return nil, nil, 0, fmt.Errorf("parse like.js: %w", err)
	}
	tweets := make([]store.Tweet, 0, len(wrapped))
	roles := make([]store.Role, 0, len(wrapped))
	hollow := 0
	for _, item := range wrapped {
		id := strings.TrimSpace(item.Like.TweetID)
		if id == "" {
			continue
		}
		text := html.UnescapeString(item.Like.FullText)
		if strings.TrimSpace(text) == "" {
			hollow++
		}
		createdAt := snowflakeTime(id)
		if parsed, err := parseTweetTime(item.Like.CreatedAt); err == nil && !parsed.IsZero() {
			createdAt = parsed
		}
		tweets = append(tweets, store.Tweet{ID: id, CreatedAt: createdAt, Text: text, FirstSource: "archive"})
		roles = append(roles, store.Role{TweetID: id, Role: "like", FirstSeenAt: importedAt, LastSeenAt: importedAt})
	}
	return tweets, roles, hollow, nil
}

func parseTweetTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(legacyTwitterTime, value); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse tweet time %q: %w", value, err)
	}
	return t.UTC(), nil
}

func snowflakeTime(id string) time.Time {
	const twitterEpochMillis = int64(1288834974657)
	value, err := strconv.ParseInt(id, 10, 64)
	if err != nil || value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli((value >> 22) + twitterEpochMillis).UTC()
}
