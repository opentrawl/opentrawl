package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/store"
)

type missingHumanRenderer struct{}

func TestPrintRejectsMissingHumanRenderer(t *testing.T) {
	var stdout bytes.Buffer
	r := runtime{stdout: &stdout}
	err := r.print(missingHumanRenderer{})
	if err == nil {
		t.Fatal("expected error")
	}
	if want := "internal: no human renderer for cli.missingHumanRenderer"; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestOpenHumanLabelsAndRetweetStubNote(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "birdcrawl.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	tweets := []store.Tweet{
		{
			ID:           "parent",
			CreatedAt:    now.Add(-time.Hour),
			AuthorID:     "alice",
			AuthorHandle: "alice",
			AuthorName:   "Alice",
			Text:         "RT @source: parent retweet stub",
			FirstSource:  "archive",
		},
		{
			ID:               "target",
			CreatedAt:        now,
			AuthorID:         "owner",
			AuthorHandle:     "owner",
			AuthorName:       "Owner",
			Text:             "RT @source: target retweet stub",
			InReplyToID:      "parent",
			ConversationID:   "thread",
			FirstSource:      "archive",
			MetricsFetchedAt: now,
		},
	}
	roles := []store.Role{{TweetID: "target", Role: "authored", FirstSeenAt: now, LastSeenAt: now}}
	if _, err := st.ImportArchive(ctx, store.ImportBatch{Tweets: tweets, Roles: roles, ImportedAt: now}); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	var out, stderr bytes.Buffer
	if err := Run(ctx, []string{"--db", dbPath, "open", store.TweetRef("target")}, &out, &stderr); err != nil {
		t.Fatalf("open error: %v stderr=%s", err, stderr.String())
	}
	for _, want := range []string{
		"replying to: Alice (@alice)",
		"note: X archives retweets as a truncated stub; open the original on x.com.",
		"Earlier in thread:",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("open output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "replying-to:") || strings.Contains(out.String(), "Ancestors:") {
		t.Fatalf("open output kept old labels:\n%s", out.String())
	}

	out.Reset()
	stderr.Reset()
	if err := Run(ctx, []string{"--db", dbPath, "--json", "open", store.TweetRef("target")}, &out, &stderr); err != nil {
		t.Fatalf("open json error: %v stderr=%s", err, stderr.String())
	}
	var opened openEnvelope
	if err := json.Unmarshal(out.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	if opened.Tweet.Note != retweetStubNote || len(opened.Ancestors) != 1 || opened.Ancestors[0].Note != retweetStubNote {
		t.Fatalf("open json notes = %#v", opened)
	}
}

func TestUnknownCommandSuggestsTopLevelHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"nope"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error")
	}
	if want := `unknown command "nope". Run 'birdcrawl --help'.`; err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}
