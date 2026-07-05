package registry

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

func TestBinariesAreTheCanonicalSet(t *testing.T) {
	want := []string{
		"imsgcrawl", "telecrawl", "wacrawl", "clawdex",
		"photoscrawl", "gogcrawl", "calcrawl", "birdcrawl",
	}
	if !slices.Equal(Binaries, want) {
		t.Fatalf("Binaries = %#v, want %#v", Binaries, want)
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	writeFakeCrawler(t, dir, "imsgcrawl", `{"schema_version":1,"contract_version":1,"id":"imsgcrawl","display_name":"iMessage"}`, 0)
	writeFakeCrawler(t, dir, "telecrawl", `not-json`, 0)
	writeFakeCrawler(t, dir, "wacrawl", `{"schema_version":1,"contract_version":1,"id":"","display_name":"WhatsApp"}`, 0)
	t.Setenv("PATH", dir)

	got := Discover(context.Background())
	if len(got) != 3 {
		t.Fatalf("discovered %d crawlers, want 3: %#v", len(got), got)
	}
	// imsgcrawl parses cleanly.
	if got[0].Name != "imsgcrawl" || got[0].Err != nil || got[0].Manifest.ID != "imsgcrawl" {
		t.Fatalf("imsgcrawl = %#v", got[0])
	}
	// telecrawl emits junk: Err set, manifest zero.
	if got[1].Name != "telecrawl" || got[1].Err == nil || got[1].Manifest.ID != "" {
		t.Fatalf("telecrawl = %#v", got[1])
	}
	// wacrawl reports an empty id: rejected as an error.
	if got[2].Name != "wacrawl" || got[2].Err == nil {
		t.Fatalf("wacrawl = %#v", got[2])
	}
	// clawdex etc. are absent from PATH and dropped entirely.
}

func writeFakeCrawler(t *testing.T, dir, name, metadata string, exit int) {
	t.Helper()
	// printf is a shell builtin, so the fake works even though the test
	// strips PATH down to this dir (no external cat on PATH).
	script := "#!/bin/sh\nif [ \"$1\" = \"metadata\" ]; then\n  printf '%s\\n' '" + metadata + "'\n  exit " + strconv.Itoa(exit) + "\nfi\nexit 64\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
