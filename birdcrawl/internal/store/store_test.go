package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSchemaMigrationSetsUserVersion(t *testing.T) {
	st := openTestStore(t)
	version, err := st.SchemaVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
}

func TestParseTweetRef(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		want    string
		wantErr bool
	}{
		{name: "valid", ref: "birdcrawl:tweet/12345", want: "12345"},
		{name: "wrong crawler", ref: "telecrawl:msg/12345", wantErr: true},
		{name: "missing id", ref: "birdcrawl:tweet/", wantErr: true},
		{name: "space", ref: "birdcrawl:tweet/12 345", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseTweetRef(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("id = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenBounds(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	var tweets []Tweet
	parent := ""
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		tweets = append(tweets, Tweet{
			ID:             id,
			CreatedAt:      now.Add(time.Duration(i) * time.Minute),
			AuthorHandle:   "example_alex",
			AuthorName:     "Alex Example",
			Text:           "ancestor " + id,
			InReplyToID:    parent,
			ConversationID: "thread",
			FirstSource:    "archive",
		})
		parent = id
	}
	for i := 0; i < 21; i++ {
		tweets = append(tweets, Tweet{
			ID:             "reply-" + itoa(i),
			CreatedAt:      now.Add(time.Duration(10+i) * time.Minute),
			AuthorHandle:   "example_blair",
			AuthorName:     "Blair Example",
			Text:           "reply",
			InReplyToID:    "e",
			ConversationID: "thread",
			FirstSource:    "archive",
		})
	}
	if _, err := st.ImportArchive(ctx, ImportBatch{Tweets: tweets, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	result, err := st.OpenTweet(ctx, "e")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Ancestors) != 3 || !result.AncestorsTruncated {
		t.Fatalf("ancestors = %d truncated %v, want 3 true", len(result.Ancestors), result.AncestorsTruncated)
	}
	if len(result.Replies) != 20 || !result.RepliesTruncated {
		t.Fatalf("replies = %d truncated %v, want 20 true", len(result.Replies), result.RepliesTruncated)
	}
}

func TestStatsOrdering(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tweets := []Tweet{
		statsTweet("low", now.Add(-time.Hour), 2, now.Add(-30*time.Minute)),
		statsTweet("high", now.Add(-2*time.Hour), 9, now.Add(-20*time.Minute)),
		statsTweet("middle", now.Add(-3*time.Hour), 5, now.Add(-10*time.Minute)),
		statsTweet("liked-not-mine", now.Add(-time.Minute), 99, now),
	}
	roles := []Role{
		{TweetID: "low", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "high", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "middle", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "liked-not-mine", Role: "like", FirstSeenAt: now, LastSeenAt: now},
	}
	if _, err := st.ImportArchive(ctx, ImportBatch{Tweets: tweets, Roles: roles, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	result, err := st.Stats(ctx, StatsFilter{By: "likes", Limit: 3, Window: 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(result.Rows))
	}
	if result.Rows[0].Ref != TweetRef("high") || result.Rows[1].Ref != TweetRef("middle") {
		t.Fatalf("ordering = %#v", result.Rows)
	}
	if result.Rows[0].CountsAsOf.IsZero() {
		t.Fatal("counts_as_of was not populated")
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(context.Background(), filepath.Join(t.TempDir(), "birdcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func statsTweet(id string, createdAt time.Time, likes int64, countsAt time.Time) Tweet {
	return Tweet{
		ID:               id,
		CreatedAt:        createdAt,
		AuthorHandle:     "example_alex",
		AuthorName:       "Alex Example",
		Text:             "synthetic stats tweet",
		LikeCount:        likes,
		FirstSource:      "archive",
		MetricsFetchedAt: countsAt,
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}
