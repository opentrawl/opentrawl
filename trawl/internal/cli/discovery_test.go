package cli

import (
	"context"
	"testing"
)

// discoverCrawlers projects each registered crawler manifest into a Source.
// Here we assert the projection: a valid manifest maps to runtime id, and a
// crawler whose manifest cannot be generated keeps that name and an error.
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
			name:       "invalid manifest keeps binary name and errors",
			crawler:    fakeCrawler{name: "telecrawl", metadata: `not-json`},
			wantID:     "telecrawl",
			wantBinary: "telecrawl",
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
