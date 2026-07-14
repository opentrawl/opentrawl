package wacrawl

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func TestMessageWhereUsesKnownParticipantBeforeOpaqueChatID(t *testing.T) {
	message := store.Message{
		ChatJID:    "118390991671363@lid",
		ChatName:   "118390991671363@lid",
		SenderName: "Avery Example",
	}
	if got := messageWhere(message); got != "Avery Example" {
		t.Fatalf("human conversation label = %q", got)
	}
	if got := messageWhereJSON(message); got != "Avery Example" {
		t.Fatalf("machine conversation label = %q", got)
	}
}

func TestSearchUsesKnownParticipantBeforeOpaqueChatID(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "whatsapp.db")
	backing, err := ckstore.Open(ctx, ckstore.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = backing.Close() }()
	archive, err := store.Use(ctx, backing, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.ReplaceAll(ctx, store.ImportStats{}, nil, nil, nil, nil, []store.Message{{
		SourcePK:    1,
		ChatJID:     "118390991671363@lid",
		ChatName:    "118390991671363@lid",
		MessageID:   "synthetic-message",
		SenderName:  "Avery Example",
		Timestamp:   time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC),
		Text:        "synthetic needle",
		MessageType: "text",
	}}); err != nil {
		t.Fatal(err)
	}
	result, err := New().Search(ctx, &trawlkit.Request{Store: backing, Paths: trawlkit.Paths{Archive: path}, Format: output.JSON}, trawlkit.Query{Text: "needle", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 1 || result.Results[0].Summary.Title != "Avery Example" || result.Results[0].Summary.Subtitle != "Avery Example" {
		t.Fatalf("search result = %#v", result.Results)
	}
}
