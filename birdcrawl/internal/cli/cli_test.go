package cli

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ckoutput "github.com/openclaw/crawlkit/output"
	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func TestSearchEnvelopeBounds(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "birdcrawl.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	var tweets []store.Tweet
	for i := 0; i < 205; i++ {
		tweets = append(tweets, store.Tweet{
			ID:           "tweet-" + itoa(i),
			CreatedAt:    now.Add(-time.Duration(i) * time.Minute),
			AuthorHandle: "example_alex",
			AuthorName:   "Alex Example",
			Text:         "needle synthetic search result",
			FirstSource:  "archive",
		})
	}
	if _, err := st.ImportArchive(ctx, store.ImportBatch{Tweets: tweets, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	var stdout, stderr bytes.Buffer
	err = Run(ctx, []string{"--db", dbPath, "--json", "search", "needle", "--limit", "500"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run error: %v stderr=%s", err, stderr.String())
	}
	var envelope searchEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Results) != maxSearchLimit {
		t.Fatalf("results = %d, want %d", len(envelope.Results), maxSearchLimit)
	}
	if envelope.TotalMatches != 205 || !envelope.Truncated {
		t.Fatalf("total/truncated = %d/%v", envelope.TotalMatches, envelope.Truncated)
	}
	if envelope.Results[0].Where != "" {
		t.Fatalf("where = %q, want empty", envelope.Results[0].Where)
	}
	if envelope.Results[0].ShortRef == "" {
		t.Fatal("short_ref is empty")
	}
}

func TestTopLevelHelpGolden(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), nil, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"birdcrawl: your X archive: tweets, bookmarks, likes and replies",
		"",
		"Read your archive:",
		"  tweets         Your tweets and the replies you sent, newest first.",
		"  bookmarks      Tweets you bookmarked.",
		"  likes          Tweets you liked.",
		"  mentions       Replies and mentions you received.",
		"  search         Full-text search across everything archived.",
		"  open           One tweet with its thread context.",
		"  stats          Your top tweets by likes, retweets or replies.",
		"",
		"Keep it fresh:",
		"  sync           Pull new activity from the X API (paid, budget-capped).",
		"  import         Load an X data export zip.",
		"",
		"Health:",
		"  status         Archive counts, coverage and API spend.",
		"  doctor         Diagnose problems; every failure has a remedy.",
		"  metadata       Machine-readable manifest for trawl.",
		"  version        Print the version.",
		"",
		"Global flags:",
		"  --db PATH      Archive database path.",
		"  --config PATH  Crawler config path.",
		"  --json         Machine-readable output.",
		"  -v, -vv        Log to stderr.",
		"",
		"Examples:",
		"  birdcrawl bookmarks",
		"  birdcrawl tweets --limit 10",
		"  birdcrawl search \"boat trip\" --after 2026-01-01",
		"  birdcrawl open t7k3f",
		"",
		"Run 'birdcrawl COMMAND --help' for flags and details.",
		"Logs: ~/.opentrawl/birdcrawl/logs/current.log",
		"",
	}, "\n")
	if stdout.String() != want {
		t.Fatalf("help =\n%s\nwant\n%s", stdout.String(), want)
	}
}

func TestBrowseHumanJSONEmptyAndMeRendering(t *testing.T) {
	ctx := context.Background()
	dbPath, aliases := seedCLITestArchive(t)
	var humanOut, humanErr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "bookmarks", "--limit", "1"}, &humanOut, &humanErr); err != nil {
		t.Fatalf("bookmarks error: %v stderr=%s", err, humanErr.String())
	}
	for _, want := range []string{
		"Bookmarks: showing 1 of 2, newest first.",
		"Open: birdcrawl open REF",
		"More: birdcrawl bookmarks --limit 2",
		aliases["bookmark-new"],
		"@example_alice",
		"new bookmarked boat trip",
	} {
		if !strings.Contains(humanOut.String(), want) {
			t.Fatalf("bookmarks output missing %q:\n%s", want, humanOut.String())
		}
	}
	if strings.Contains(humanOut.String(), "birdcrawl:tweet/") {
		t.Fatalf("human browse leaked full ref:\n%s", humanOut.String())
	}

	var tweetsOut bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "tweets", "--limit", "5"}, &tweetsOut, &humanErr); err != nil {
		t.Fatalf("tweets error: %v", err)
	}
	if !strings.Contains(tweetsOut.String(), "me → @example_alice") {
		t.Fatalf("tweets output did not render me reply:\n%s", tweetsOut.String())
	}
	var tweetsJSON bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "tweets", "--limit", "5"}, &tweetsJSON, &humanErr); err != nil {
		t.Fatalf("tweets json error: %v", err)
	}
	var tweets listEnvelope
	if err := json.Unmarshal(tweetsJSON.Bytes(), &tweets); err != nil {
		t.Fatal(err)
	}
	if len(tweets.Results) < 2 || tweets.Results[0].Who != "me" || tweets.Results[1].Who != "me" {
		t.Fatalf("tweets json who = %#v", tweets.Results)
	}
	if tweets.Results[1].InReplyTo != "Alice Example (@example_alice)" {
		t.Fatalf("tweets json in_reply_to = %#v", tweets.Results[1])
	}

	var jsonOut bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "bookmarks", "--limit", "1"}, &jsonOut, &humanErr); err != nil {
		t.Fatalf("bookmarks json error: %v", err)
	}
	var envelope listEnvelope
	if err := json.Unmarshal(jsonOut.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Kind != "bookmarks" || len(envelope.Results) != 1 || envelope.Results[0].ShortRef == "" || envelope.Results[0].Ref == "" {
		t.Fatalf("browse json = %#v", envelope)
	}
	if envelope.Results[0].Who != "Alice Example (@example_alice)" {
		t.Fatalf("json who = %q", envelope.Results[0].Who)
	}

	emptyDB := filepath.Join(t.TempDir(), "empty.db")
	st, err := store.Open(ctx, emptyDB)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	var emptyOut bytes.Buffer
	if err := Run(ctx, []string{"--db", emptyDB, "likes"}, &emptyOut, &humanErr); err != nil {
		t.Fatalf("empty likes error: %v", err)
	}
	if !strings.Contains(emptyOut.String(), "No likes archived yet. Run 'birdcrawl sync' or 'birdcrawl import archive PATH'.") {
		t.Fatalf("empty output =\n%s", emptyOut.String())
	}
}

func TestSearchAndStatsUseShortRefsAndComponents(t *testing.T) {
	ctx := context.Background()
	dbPath, aliases := seedCLITestArchive(t)
	var searchOut, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "search", "boat", "--limit", "1"}, &searchOut, &stderr); err != nil {
		t.Fatalf("search error: %v stderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"Search \"boat\": showing 1 of 6.",
		"Open: birdcrawl open REF",
		"More: birdcrawl search \"boat\" --limit 2",
		aliases["own-top"],
	} {
		if !strings.Contains(searchOut.String(), want) {
			t.Fatalf("search output missing %q:\n%s", want, searchOut.String())
		}
	}
	if strings.Contains(searchOut.String(), " in X") || strings.Contains(searchOut.String(), "birdcrawl:tweet/") {
		t.Fatalf("search output leaked bad clause or full ref:\n%s", searchOut.String())
	}

	var searchJSON bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "search", "boat", "--limit", "1"}, &searchJSON, &stderr); err != nil {
		t.Fatalf("search json error: %v", err)
	}
	var search searchEnvelope
	if err := json.Unmarshal(searchJSON.Bytes(), &search); err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].ShortRef == "" {
		t.Fatalf("search json = %#v", search)
	}
	if search.Results[0].Who != "me" {
		t.Fatalf("search json who = %q", search.Results[0].Who)
	}

	var statsOut bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "stats", "--by", "likes", "--limit", "2"}, &statsOut, &stderr); err != nil {
		t.Fatalf("stats error: %v", err)
	}
	for _, want := range []string{
		"Your top tweets by likes, last 30 days.",
		"Showing 2 of 2.",
		"Engagement counts fetched between",
		"Open: birdcrawl open REF",
		"date              likes",
		aliases["own-top"],
		"384",
	} {
		if !strings.Contains(statsOut.String(), want) {
			t.Fatalf("stats output missing %q:\n%s", want, statsOut.String())
		}
	}
	var statsJSON bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "stats", "--by", "likes", "--limit", "1"}, &statsJSON, &stderr); err != nil {
		t.Fatalf("stats json error: %v", err)
	}
	var stats statsEnvelope
	if err := json.Unmarshal(statsJSON.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if len(stats.Results) != 1 || stats.Results[0].ShortRef == "" {
		t.Fatalf("stats json = %#v", stats)
	}
	if stats.Results[0].Who != "me" {
		t.Fatalf("stats json who = %q", stats.Results[0].Who)
	}
	var statsRaw map[string]any
	if err := json.Unmarshal(statsJSON.Bytes(), &statsRaw); err != nil {
		t.Fatal(err)
	}
	if _, ok := statsRaw["freshness_spread"]; ok {
		t.Fatalf("stats json still has freshness_spread: %s", statsJSON.String())
	}
	if statsRaw["counts_fetched_from"] == "" || statsRaw["counts_fetched_to"] == "" {
		t.Fatalf("stats json count freshness missing: %s", statsJSON.String())
	}
}

func TestOpenResolvesShortRefsAndErrors(t *testing.T) {
	ctx := context.Background()
	dbPath, aliases := seedCLITestArchive(t)
	var out, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "--json", "open", aliases["own-top"]}, &out, &stderr); err != nil {
		t.Fatalf("open alias error: %v stderr=%s", err, stderr.String())
	}
	var opened openEnvelope
	if err := json.Unmarshal(out.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Ref != store.TweetRef("own-top") {
		t.Fatalf("opened ref = %q", opened.Ref)
	}

	for _, tt := range []struct {
		name string
		ref  string
		code string
	}{
		{name: "unknown", ref: "33333", code: "unknown_short_ref"},
		{name: "ambiguous", ref: "22222", code: "ambiguous_short_ref"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.code == "ambiguous_short_ref" {
				insertAmbiguousShortRef(t, dbPath)
			}
			out.Reset()
			stderr.Reset()
			err := Run(ctx, []string{"--db", dbPath, "--json", "open", tt.ref}, &out, &stderr)
			if err == nil {
				t.Fatal("expected error")
			}
			var envelope ckoutput.ErrorEnvelope
			if jsonErr := json.Unmarshal(out.Bytes(), &envelope); jsonErr != nil {
				t.Fatalf("decode error: %v output=%s", jsonErr, out.String())
			}
			if envelope.Error.Code != tt.code || envelope.Error.Remedy == "" {
				t.Fatalf("error = %#v, want %s", envelope.Error, tt.code)
			}
			if tt.code == "unknown_short_ref" && envelope.Error.Remedy != "re-run the listing to get a fresh ref, or use the full ref from --json output" {
				t.Fatalf("unknown short ref remedy = %q", envelope.Error.Remedy)
			}
		})
	}
}

func TestStatusOmitsRecentLogAndUsageErrorIsNotLogged(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "birdcrawl.db")
	var stdout, stderr bytes.Buffer
	err := Run(ctx, []string{"--db", dbPath, "--json", "open", "33333"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected open error")
	}
	stdout.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "status"}, &stdout, &stderr); err != nil {
		t.Fatalf("status error: %v", err)
	}
	if strings.Contains(stdout.String(), "Recent log:") {
		t.Fatalf("status included Recent log:\n%s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = Run(ctx, []string{"--db", dbPath, "search"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected usage error")
	}
	logPath := filepath.Join(root, "birdcrawl", "logs", "current.log")
	logData, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(logData), "usage_error") {
		t.Fatalf("usage error was logged as an error:\n%s", string(logData))
	}
}

func seedCLITestArchive(t *testing.T) (string, map[string]string) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "birdcrawl.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tweets := []store.Tweet{
		{
			ID:               "own-top",
			CreatedAt:        now.Add(-time.Hour),
			AuthorID:         "owner",
			AuthorHandle:     "example_owner",
			AuthorName:       "Owner Example",
			Text:             "own boat trip update with useful detail",
			LikeCount:        384,
			RetweetCount:     12,
			ReplyCount:       7,
			FirstSource:      "archive",
			MetricsFetchedAt: now.Add(-30 * time.Minute),
		},
		{
			ID:               "own-reply",
			CreatedAt:        now.Add(-2 * time.Hour),
			AuthorID:         "owner",
			AuthorHandle:     "example_owner",
			AuthorName:       "Owner Example",
			Text:             "reply from owner about the boat trip",
			InReplyToID:      "bookmark-new",
			LikeCount:        5,
			RetweetCount:     1,
			ReplyCount:       2,
			FirstSource:      "archive",
			MetricsFetchedAt: now.Add(-20 * time.Minute),
		},
		{
			ID:           "bookmark-new",
			CreatedAt:    now.Add(-3 * time.Hour),
			AuthorID:     "alice",
			AuthorHandle: "example_alice",
			AuthorName:   "Alice Example",
			Text:         "new bookmarked boat trip",
			FirstSource:  "archive",
		},
		{
			ID:           "bookmark-old",
			CreatedAt:    now.Add(-4 * time.Hour),
			AuthorID:     "casey",
			AuthorHandle: "example_casey",
			AuthorName:   "Casey Example",
			Text:         "old bookmarked boat trip",
			FirstSource:  "archive",
		},
		{
			ID:           "liked",
			CreatedAt:    now.Add(-5 * time.Hour),
			AuthorID:     "blair",
			AuthorHandle: "example_blair",
			AuthorName:   "Blair Example",
			Text:         "liked boat trip",
			FirstSource:  "archive",
		},
		{
			ID:           "mention",
			CreatedAt:    now.Add(-6 * time.Hour),
			AuthorID:     "devon",
			AuthorHandle: "example_devon",
			AuthorName:   "Devon Example",
			Text:         "mention about boat trip",
			InReplyToID:  "own-top",
			FirstSource:  "archive",
		},
	}
	roles := []store.Role{
		{TweetID: "own-top", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "own-reply", Role: "authored", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "bookmark-new", Role: "bookmark", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "bookmark-old", Role: "bookmark", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "liked", Role: "like", FirstSeenAt: now, LastSeenAt: now},
		{TweetID: "mention", Role: "mention", FirstSeenAt: now, LastSeenAt: now},
	}
	if _, err := st.ImportArchive(ctx, store.ImportBatch{Tweets: tweets, Roles: roles, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	refs := make([]string, 0, len(tweets))
	for _, tweet := range tweets {
		refs = append(refs, store.TweetRef(tweet.ID))
	}
	byRef, err := st.ShortRefAliases(ctx, refs)
	if err != nil {
		t.Fatal(err)
	}
	aliases := make(map[string]string, len(tweets))
	for _, tweet := range tweets {
		aliases[tweet.ID] = byRef[store.TweetRef(tweet.ID)]
	}
	return dbPath, aliases
}

func insertAmbiguousShortRef(t *testing.T, dbPath string) {
	t.Helper()
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`insert into short_refs(alias, full_ref) values(?, ?), (?, ?)`,
		"22222", store.TweetRef("own-top"), "22222", store.TweetRef("bookmark-new")); err != nil {
		t.Fatal(err)
	}
}

func TestSyncReturnsCredentialsMissingEnvelope(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--db", filepath.Join(t.TempDir(), "birdcrawl.db"), "--json", "sync"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected sync error")
	}
	var envelope ckoutput.ErrorEnvelope
	if jsonErr := json.Unmarshal(stdout.Bytes(), &envelope); jsonErr != nil {
		t.Fatal(jsonErr)
	}
	if envelope.Error.Code != "credentials_missing" {
		t.Fatalf("code = %q, want credentials_missing", envelope.Error.Code)
	}
	if envelope.Error.Remedy == "" {
		t.Fatal("remedy is empty")
	}
}

// TestStatsMoreHintNeverGoesBackwards guards the regression where the stats
// "More" hint routed through the search cap (maxSearchLimit=200): once stats
// honors --limit uncapped, a large limit could otherwise print a smaller
// next limit than what is already on screen.
func TestStatsMoreHintNeverGoesBackwards(t *testing.T) {
	cases := []struct{ shown, want int }{
		{0, defaultStatsLimit},
		{1, 2},
		{10, 20},
		{250, 500},
	}
	for _, c := range cases {
		got := statsNextLimit(c.shown)
		if got != c.want {
			t.Fatalf("statsNextLimit(%d) = %d, want %d", c.shown, got, c.want)
		}
		if c.shown >= 1 && got <= c.shown {
			t.Fatalf("statsNextLimit(%d) = %d must exceed the shown count", c.shown, got)
		}
	}
}
