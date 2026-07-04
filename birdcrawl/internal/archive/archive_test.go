package archive

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

func TestImportDirectory(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	result, err := Importer{Now: fixedNow}.Import(ctx, st, filepath.Join("testdata", "synthetic-dump"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Tweets != 8 {
		t.Fatalf("tweets = %d, want 8", result.Stats.Tweets)
	}
	if result.Stats.Authored != 5 {
		t.Fatalf("authored = %d, want 5", result.Stats.Authored)
	}
	if result.Stats.LikesSeen != 3 {
		t.Fatalf("likes = %d, want 3", result.Stats.LikesSeen)
	}
	if result.Stats.Profiles != 1 {
		t.Fatalf("profiles = %d, want 1 (dump owner)", result.Stats.Profiles)
	}
	// The second note exercises the dump quirks: leading reply mention on the
	// tweet only, different t.co ids on each side, and a one-second offset.
	if result.Stats.NoteTweetsMerged != 2 || result.Stats.NoteTweetsUnmatched != 0 {
		t.Fatalf("note tweets merged %d unmatched %d, want 2/0", result.Stats.NoteTweetsMerged, result.Stats.NoteTweetsUnmatched)
	}
	if result.Stats.LikesWithoutText != 1 {
		t.Fatalf("likes without text = %d, want 1", result.Stats.LikesWithoutText)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.Authored != 5 || status.LikesSeen != 3 {
		t.Fatalf("status counts = authored %d likes %d", status.Authored, status.LikesSeen)
	}
}

func TestImportStampsOwnerAndMergesNotes(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := (Importer{Now: fixedNow}).Import(ctx, st, filepath.Join("testdata", "synthetic-dump")); err != nil {
		t.Fatal(err)
	}
	opened, err := st.OpenTweet(ctx, "1800000000000000001")
	if err != nil {
		t.Fatal(err)
	}
	if opened.Tweet.AuthorHandle != "example_alex" || opened.Tweet.AuthorName != "Alex Example" {
		t.Fatalf("owner not stamped: %q %q", opened.Tweet.AuthorHandle, opened.Tweet.AuthorName)
	}
	if opened.Tweet.MetricsFetchedAt.IsZero() {
		t.Fatal("authored tweet has no metrics_fetched_at; dump counts should be dated")
	}
	note, err := st.OpenTweet(ctx, "1800000000000000004")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note.Tweet.Text, "only note-tweet.js carries") {
		t.Fatalf("note tweet text not merged: %q", note.Tweet.Text)
	}
	escaped, err := st.OpenTweet(ctx, "1800000000000000003")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(escaped.Tweet.Text, "> a plain text fixture & friends") {
		t.Fatalf("HTML entities not unescaped: %q", escaped.Tweet.Text)
	}
}

func TestImportRequiresAccountFile(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tweets.js", "like.js"} {
		data, err := os.ReadFile(filepath.Join("testdata", "synthetic-dump", "data", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st := openTestStore(t)
	_, err := Importer{Now: fixedNow}.Import(context.Background(), st, root)
	if err == nil || !strings.Contains(err.Error(), "account.js") {
		t.Fatalf("want account.js error, got %v", err)
	}
}

func TestImportZip(t *testing.T) {
	ctx := context.Background()
	zipPath := buildFixtureZip(t)
	st := openTestStore(t)
	result, err := Importer{Now: fixedNow}.Import(ctx, st, zipPath)
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.Tweets != 8 {
		t.Fatalf("tweets = %d, want 8", result.Stats.Tweets)
	}
}

func TestFTSParityAfterImport(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if _, err := (Importer{Now: fixedNow}).Import(ctx, st, filepath.Join("testdata", "synthetic-dump")); err != nil {
		t.Fatal(err)
	}
	tweets, fts, err := st.FTSParity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if tweets != fts {
		t.Fatalf("FTS parity = tweets %d fts %d", tweets, fts)
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "birdcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func buildFixtureZip(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "archive.zip")
	file, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(file)
	for _, rel := range []string{"data/tweets.js", "data/like.js", "data/account.js", "data/note-tweet.js"} {
		data, err := os.ReadFile(filepath.Join("testdata", "synthetic-dump", rel))
		if err != nil {
			t.Fatal(err)
		}
		w, err := zw.Create(rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return out
}

func fixedNow() time.Time {
	return time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
}
