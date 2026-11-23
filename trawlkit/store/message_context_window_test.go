package store

import (
	"context"
	"reflect"
	"testing"
)

func TestReadSurroundingMessageContextWindow(t *testing.T) {
	window, err := ReadSurroundingMessageContextWindow(
		context.Background(),
		"target",
		2,
		func(_ context.Context, limit int) ([]string, error) {
			if limit != 3 {
				t.Fatalf("expected over-fetch limit 3, got %d", limit)
			}
			// Newest-first (closest to target first).
			return []string{"prev-1", "prev-2", "prev-3"}, nil
		},
		func(_ context.Context, limit int) ([]string, error) {
			if limit != 3 {
				t.Fatalf("expected over-fetch limit 3, got %d", limit)
			}
			// Oldest-first (closest to target first).
			return []string{"next-1"}, nil
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !window.BeforeTruncated || window.AfterTruncated {
		t.Fatalf("unexpected truncation flags: before=%v after=%v", window.BeforeTruncated, window.AfterTruncated)
	}
	wantChronological := []string{"prev-2", "prev-1", "target", "next-1"}
	if got := window.MessagesInChronologicalOrder(); !reflect.DeepEqual(got, wantChronological) {
		t.Fatalf("chronological mismatch: got %v want %v", got, wantChronological)
	}
	wantNewestFirst := []string{"next-1", "target", "prev-1", "prev-2"}
	if got := window.MessagesNewestFirst(); !reflect.DeepEqual(got, wantNewestFirst) {
		t.Fatalf("newest-first mismatch: got %v want %v", got, wantNewestFirst)
	}
}

func TestFTS5EastAsianAndTokenHelpers(t *testing.T) {
	if got := AppendEastAsianCharacterPairsForFTS5("成都茶社"); got != "成都茶社 成都 都茶 茶社" {
		t.Fatalf("unexpected appended bigrams: %q", got)
	}
	if got := ReplaceEastAsianRunsWithCharacterPairsForFTS5("成都茶社"); got != "成都 都茶 茶社" {
		t.Fatalf("unexpected replaced bigrams: %q", got)
	}
	if got := FTS5QueryFromWordTokens("Booking.com", nil); got != `"Booking" "com"` {
		t.Fatalf("unexpected word tokens query: %q", got)
	}
	if got := FTS5TokenQueryOrPhrase("..."); got != `"..."` {
		t.Fatalf("unexpected fallback phrase query: %q", got)
	}
}
