package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
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
		{name: "valid", ref: "twitter:tweet/12345", want: "12345"},
		{name: "legacy", ref: "birdcrawl:tweet/12345", want: "12345"},
		{name: "wrong crawler", ref: "telegram:msg/12345", wantErr: true},
		{name: "missing id", ref: "twitter:tweet/", wantErr: true},
		{name: "space", ref: "twitter:tweet/12 345", wantErr: true},
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
	if result.Population != 3 {
		t.Fatalf("population = %d, want 3", result.Population)
	}
}

func TestListByRoleFiltersOrdersAndCounts(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tweets := []Tweet{
		testTweet("tweet-old", now.Add(-5*time.Hour), "owner", "example_owner", "Owner Example", "old authored"),
		testTweet("tweet-new", now.Add(-time.Hour), "owner", "example_owner", "Owner Example", "new authored"),
		testTweet("bookmark-old", now.Add(-4*time.Hour), "alice", "example_alice", "Alice Example", "old bookmark"),
		testTweet("bookmark-new", now.Add(-2*time.Hour), "alice", "example_alice", "Alice Example", "new bookmark"),
		testTweet("liked", now.Add(-3*time.Hour), "blair", "example_blair", "Blair Example", "liked tweet"),
		testTweet("mention", now.Add(-30*time.Minute), "casey", "example_casey", "Casey Example", "mention tweet"),
	}
	roles := []Role{
		{TweetID: "tweet-old", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "tweet-new", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "bookmark-old", Role: "bookmark", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "bookmark-new", Role: "bookmark", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "liked", Role: "like", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "mention", Role: "mention", FirstSeenAt: now, LastSeenAt: now},
	}
	if _, err := st.ImportArchive(ctx, ImportBatch{Tweets: tweets, Roles: roles, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		role string
		want string
	}{
		{role: "authored", want: "tweet-new"},
		{role: "bookmark", want: "bookmark-new"},
		{role: "like", want: "liked"},
		{role: "mention", want: "mention"},
	} {
		results, total, err := st.ListByRole(ctx, tt.role, ListFilter{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != tt.want {
			t.Fatalf("%s first result = %#v, want %s", tt.role, results, tt.want)
		}
		if tt.role == "authored" || tt.role == "bookmark" {
			if total != 2 {
				t.Fatalf("%s total = %d, want 2", tt.role, total)
			}
		}
	}
	after := now.Add(-3 * time.Hour)
	before := now.Add(-time.Hour)
	results, total, err := st.ListByRole(ctx, "bookmark", ListFilter{Limit: 10, After: &after, Before: &before})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(results) != 1 || results[0].ID != "bookmark-new" {
		t.Fatalf("windowed bookmarks = total %d rows %#v, want bookmark-new only", total, results)
	}
	// Limit 0 returns every row with no default cap.
	all, total, err := st.ListByRole(ctx, "bookmark", ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("bookmark limit 0 = total %d rows %d, want all 2", total, len(all))
	}
}

func TestShortRefsRebuildLookupAndAliasDisplay(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tweets := []Tweet{
		testTweet("a", now, "owner", "example_owner", "Owner Example", "first"),
		testTweet("b", now.Add(time.Minute), "owner", "example_owner", "Owner Example", "second"),
	}
	if _, err := st.ImportArchive(ctx, ImportBatch{Tweets: tweets, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	aliases, err := st.ShortRefAliases(ctx, []string{TweetRef("a"), TweetRef("b")})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{TweetRef("a"), TweetRef("b")} {
		alias := aliases[ref]
		if alias == "" {
			t.Fatalf("alias for %s is empty", ref)
		}
		matches, err := st.ResolveShortRef(ctx, alias)
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) != 1 || matches[0] != ref {
			t.Fatalf("lookup %s = %#v, want %s", alias, matches, ref)
		}
	}
}

func TestEnsureShortRefsLogsReadRefresh(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	run, err := cklog.NewRun(cklog.Options{
		StateRoot: filepath.Join(t.TempDir(), "logs"),
		CrawlerID: "birdcrawl",
		Command:   "search",
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Finish(nil) })
	st.SetLog(run)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if _, err := st.ImportArchive(ctx, ImportBatch{Tweets: []Tweet{
		testTweet("a", now, "owner", "example_owner", "Owner Example", "first"),
	}, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `insert into tweets(id, created_at, text, first_source) values(?, ?, ?, ?)`, "b", formatUTC(now.Add(time.Minute)), "second", "archive"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ShortRefAliases(ctx, []string{TweetRef("b")}); err != nil {
		t.Fatal(err)
	}
	logData, err := os.ReadFile(run.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "short_refs_refresh: rebuilt derived short ref cache on read") {
		t.Fatalf("refresh log missing:\n%s", string(logData))
	}
}

func TestShortRefsCurrentPropagatesIndexReadError(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	if _, err := st.ImportArchive(ctx, ImportBatch{Tweets: []Tweet{
		testTweet("a", now, "owner", "example_owner", "Owner Example", "first"),
	}, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `drop table short_refs`); err != nil {
		t.Fatal(err)
	}
	current, err := st.shortRefsCurrent(ctx)
	if err == nil {
		t.Fatal("expected short_refs read error")
	}
	if current {
		t.Fatal("current = true, want false")
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

func testTweet(id string, createdAt time.Time, authorID string, handle string, name string, text string) Tweet {
	return Tweet{
		ID:           id,
		CreatedAt:    createdAt,
		AuthorID:     authorID,
		AuthorHandle: handle,
		AuthorName:   name,
		Text:         text,
		FirstSource:  "archive",
	}
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
