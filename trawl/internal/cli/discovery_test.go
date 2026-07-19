package cli

import (
	"context"
	"encoding/json"
	"testing"
)

// discoverCrawlers projects each registered crawler manifest into a Source.
// Here we assert the projection: a valid manifest maps to runtime id, and a
// crawler whose manifest cannot be generated still surfaces its declared id
// and an error.
func TestDiscoverCrawlersProjectsManifests(t *testing.T) {
	ensureSyntheticHome(t)
	tests := []struct {
		name       string
		crawler    fakeCrawler
		wantID     string
		wantBinary string
		wantErr    bool
	}{
		{
			name:       "valid manifest maps runtime id",
			crawler:    fakeCrawler{name: "imessage", metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"iMessage","binary":{"name":"imessage"}}`},
			wantID:     "imessage",
			wantBinary: "cli.test",
		},
		{
			name:       "invalid manifest keeps the declared source name and errors",
			crawler:    fakeCrawler{name: "telegram", metadata: `not-json`},
			wantID:     "telegram",
			wantBinary: "",
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
			if source.Manifest.ID != source.ID || source.Manifest.DisplayName != source.DisplayName || source.Manifest.Binary.Name != source.Binary {
				t.Fatalf("source does not project stored manifest: %#v", source)
			}
			if tt.name == "valid manifest maps runtime id" {
				content, err := json.MarshalIndent(source.Manifest, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				writeRuntimeEvidence(t, "discovery-manifests.json", append(content, '\n'))
			}
		})
	}
}
