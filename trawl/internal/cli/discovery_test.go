package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverCrawlers(t *testing.T) {
	tests := []struct {
		name    string
		scripts []fakeCrawler
		apps    map[string]string
		want    []discoveredSource
	}{
		{
			name: "binary present and valid",
			scripts: []fakeCrawler{{
				name:     "imsgcrawl",
				metadata: `{"schema_version":1,"contract_version":1,"id":"imessage","display_name":"Messages"}`,
			}},
			want: []discoveredSource{{id: "imessage", binary: "imsgcrawl"}},
		},
		{
			name: "binary present but metadata invalid",
			scripts: []fakeCrawler{{
				name:     "telecrawl",
				metadata: `not-json`,
			}},
			want: []discoveredSource{{id: "telecrawl", binary: "telecrawl", metadataErr: true}},
		},
		{
			name: "binary absent is skipped",
			want: nil,
		},
		{
			name: "drop-in manifest is picked up",
			scripts: []fakeCrawler{{
				name:     "examplecrawl",
				metadata: `{"schema_version":1,"contract_version":1,"id":"examplecrawl","display_name":"Example"}`,
			}},
			apps: map[string]string{
				"example.json": `{"id":"example","binary":"examplecrawl"}`,
			},
			want: []discoveredSource{{id: "examplecrawl", binary: "examplecrawl"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			binDir := writeFakeCrawlers(t, tt.scripts...)
			t.Setenv("PATH", binDir)
			appsDir := filepath.Join(t.TempDir(), "apps")
			if err := os.MkdirAll(appsDir, 0o755); err != nil {
				t.Fatal(err)
			}
			for name, data := range tt.apps {
				if err := os.WriteFile(filepath.Join(appsDir, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			got := discoverCrawlers(context.Background(), appsDir)
			if len(got) != len(tt.want) {
				t.Fatalf("discovered %d sources, want %d: %#v", len(got), len(tt.want), got)
			}
			for i, want := range tt.want {
				if got[i].ID != want.id || got[i].Binary != want.binary {
					t.Fatalf("source[%d] = (%q, %q), want (%q, %q)", i, got[i].ID, got[i].Binary, want.id, want.binary)
				}
				if (got[i].MetadataErr != nil) != want.metadataErr {
					t.Fatalf("source[%d] metadataErr = %v, want %v", i, got[i].MetadataErr, want.metadataErr)
				}
			}
		})
	}
}

type discoveredSource struct {
	id          string
	binary      string
	metadataErr bool
}
