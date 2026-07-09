package cli

import (
	"context"
	"testing"
)

// discoverCrawlers projects each registered crawler manifest into a Source.
// Here we assert the projection: a valid manifest maps to runtime id, and a
// crawler whose manifest cannot be generated still surfaces the canonical
// id and an error — never the pre-rename binary name a broken crawler might
// self-report (TRAWL-147/194 leak: "imsgcrawl status failed" next to a
// table row that says "iMessage").
func TestDiscoverCrawlersProjectsManifests(t *testing.T) {
	tests := []struct {
		name       string
		crawler    fakeCrawler
		wantID     string
		wantBinary string
		wantErr    bool
	}{
		{
			name:       "valid manifest maps runtime id",
			crawler:    fakeCrawler{name: "imsgcrawl", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage","binary":{"name":"imsgcrawl"}}`},
			wantID:     "imessage",
			wantBinary: "imessage",
		},
		{
			name:       "invalid manifest canonicalizes the legacy binary name and errors",
			crawler:    fakeCrawler{name: "telecrawl", metadata: `not-json`},
			wantID:     "telegram",
			wantBinary: "telegram",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.crawler)
			t.Setenv("PATH", binDir)

			got := discoverCrawlers(context.Background())
			if len(got) != 1 {
				t.Fatalf("discovered %d sources, want 1: %#v", len(got), got)
			}
			source := got[0]
			if source.ID != tt.wantID || source.Binary != tt.wantBinary {
				t.Fatalf("source = (%q, %q), want (%q, %q)", source.ID, source.Binary, tt.wantID, tt.wantBinary)
			}
			if (source.MetadataErr != nil) != tt.wantErr {
				t.Fatalf("MetadataErr = %v, want error %v", source.MetadataErr, tt.wantErr)
			}
		})
	}
}

// canonicalizeSourceID is the formatter discoverCrawlers relies on to keep
// a broken crawler's self-reported id human-safe. Every pre-rename binary
// name it knows about must resolve to its canonical id; anything else,
// including an id that is already canonical, must pass through unchanged.
func TestCanonicalizeSourceID(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"imsgcrawl", "imessage"},
		{"telecrawl", "telegram"},
		{"wacrawl", "whatsapp"},
		{"clawdex", "contacts"},
		{"photoscrawl", "photos"},
		{"gogcrawl", "gmail"},
		{"calcrawl", "calendar"},
		{"birdcrawl", "twitter"},
		{"imessage", "imessage"},
		{"notes", "notes"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if got := canonicalizeSourceID(tt.raw); got != tt.want {
				t.Fatalf("canonicalizeSourceID(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}
