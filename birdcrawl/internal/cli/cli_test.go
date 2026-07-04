package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

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
	if envelope.Results[0].Where != "X" {
		t.Fatalf("where = %q, want X", envelope.Results[0].Where)
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
	var envelope errorEnvelope
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
