package usage

import (
	"strings"
	"testing"
)

func TestDocRenderFull(t *testing.T) {
	doc := Doc{
		Tool:    "birdcrawl",
		Tagline: "your bird archive: posts, bookmarks, likes and replies",
		Groups: []Group{
			{Title: "Read your archive", Commands: []Command{
				{Name: "posts", Summary: "Your posts, newest first."},
				{Name: "bookmarks", Summary: "Your bookmarks, newest first."},
			}},
			{Title: "Maintain your archive", Commands: []Command{
				{Name: "sync", Summary: "Update the local archive."},
				{Name: "doctor", Summary: "Check local setup and archive health."},
			}},
		},
		Flags: []Flag{
			{Name: "--db PATH", Summary: "Archive database path."},
			{Name: "--json", Summary: "Write JSON output."},
		},
		Examples: []string{
			"birdcrawl posts --limit 20",
			"birdcrawl search \"from:alex\"",
		},
		Footer: []string{
			"Run trawl twitter doctor when setup looks wrong.",
			"Run trawl twitter help <command> for command details.",
		},
	}
	want := strings.Join([]string{
		"birdcrawl: your bird archive: posts, bookmarks, likes and replies",
		"",
		"Read your archive:",
		"  posts      Your posts, newest first.",
		"  bookmarks  Your bookmarks, newest first.",
		"",
		"Maintain your archive:",
		"  sync       Update the local archive.",
		"  doctor     Check local setup and archive health.",
		"",
		"Global flags:",
		"  --db PATH  Archive database path.",
		"  --json     Write JSON output.",
		"",
		"Examples:",
		"  birdcrawl posts --limit 20",
		"  birdcrawl search \"from:alex\"",
		"",
		"Run trawl twitter doctor when setup looks wrong.",
		"Run trawl twitter help <command> for command details.",
		"",
	}, "\n")
	if got := doc.Render(); got != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", got, want)
	}
}

func TestDocRenderMinimal(t *testing.T) {
	doc := Doc{
		Tool:    "birdcrawl",
		Tagline: "your bird archive",
		Groups: []Group{
			{Title: "Read your archive", Commands: []Command{
				{Name: "posts", Summary: "Your posts, newest first."},
			}},
		},
	}
	want := strings.Join([]string{
		"birdcrawl: your bird archive",
		"",
		"Read your archive:",
		"  posts  Your posts, newest first.",
		"",
	}, "\n")
	if got := doc.Render(); got != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", got, want)
	}
}
