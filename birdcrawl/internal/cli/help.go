package cli

import (
	"io"

	"github.com/openclaw/crawlkit/usage"
)

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		switch arg {
		case "--help", "-help", "-h":
			return true
		}
	}
	return false
}

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, birdUsageDoc().Render())
}

func printCommandUsage(w io.Writer, args []string) {
	_, _ = io.WriteString(w, commandUsage(args))
}

func commandUsage(args []string) string {
	if len(args) == 0 {
		return topUsageText()
	}
	switch args[0] {
	case "metadata":
		return "usage: birdcrawl metadata [--json]\n\nPrints the crawler manifest and contract capabilities.\n"
	case "status":
		return "usage: birdcrawl status [--json]\n\nReads archive counts and coverage without syncing.\n"
	case "tweets":
		return "usage: birdcrawl tweets [--limit N] [--after RFC3339] [--before RFC3339] [--json]\n\nShows your tweets and replies, newest first.\n"
	case "bookmarks":
		return "usage: birdcrawl bookmarks [--limit N] [--after RFC3339] [--before RFC3339] [--json]\n\nShows tweets you bookmarked, ordered by tweet date; X does not record when you bookmarked them.\n"
	case "likes":
		return "usage: birdcrawl likes [--limit N] [--after RFC3339] [--before RFC3339] [--json]\n\nShows tweets you liked, newest first.\n"
	case "mentions":
		return "usage: birdcrawl mentions [--limit N] [--after RFC3339] [--before RFC3339] [--json]\n\nShows replies and mentions you received, newest first.\n"
	case "import":
		return "usage: birdcrawl import archive PATH [--json]\n\nImports tweets.js and like.js from an extracted or zipped X archive dump.\n"
	case "sync":
		return "usage: birdcrawl sync [--json]\n\nSyncs live X API data into the local archive.\n"
	case "search":
		return "usage: birdcrawl search QUERY [--limit N] [--after RFC3339] [--before RFC3339] [--json]\n\nSearches archived tweets and returns refs for birdcrawl open.\n"
	case "open":
		return "usage: birdcrawl open birdcrawl:tweet/ID [--json]\n\nOpens one tweet with up to 3 ancestors and up to 20 replies.\n"
	case "stats":
		return "usage: birdcrawl stats [--window 30d] [--by likes|retweets|replies] [--limit N] [--json]\n\nSorts stored tweet counts mechanically and shows when those counts were fetched.\n"
	case "doctor":
		return "usage: birdcrawl doctor [--json]\n\nChecks archive integrity, FTS parity, dump import state and staleness.\n"
	case "version":
		return "usage: birdcrawl version\n\nPrints the birdcrawl version.\n"
	default:
		return topUsageText()
	}
}

func topUsageText() string {
	return birdUsageDoc().Render()
}

func birdUsageDoc() usage.Doc {
	return usage.Doc{
		Tool:    "birdcrawl",
		Tagline: "your X archive: tweets, bookmarks, likes and replies",
		Groups: []usage.Group{
			{Title: "Read your archive", Commands: []usage.Command{
				{Name: "tweets", Summary: "Your tweets and the replies you sent, newest first."},
				{Name: "bookmarks", Summary: "Tweets you bookmarked."},
				{Name: "likes", Summary: "Tweets you liked."},
				{Name: "mentions", Summary: "Replies and mentions you received."},
				{Name: "search", Summary: "Full-text search across everything archived."},
				{Name: "open", Summary: "One tweet with its thread context."},
				{Name: "stats", Summary: "Your top tweets by likes, retweets or replies."},
			}},
			{Title: "Keep it fresh", Commands: []usage.Command{
				{Name: "sync", Summary: "Pull new activity from the X API (paid, budget-capped)."},
				{Name: "import", Summary: "Load an X data export zip."},
			}},
			{Title: "Health", Commands: []usage.Command{
				{Name: "status", Summary: "Archive counts, coverage and API spend."},
				{Name: "doctor", Summary: "Diagnose problems; every failure has a remedy."},
				{Name: "metadata", Summary: "Machine-readable manifest for trawl."},
				{Name: "version", Summary: "Print the version."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--db PATH", Summary: "Archive database path."},
			{Name: "--config PATH", Summary: "Crawler config path."},
			{Name: "--json", Summary: "Machine-readable output."},
			{Name: "-v, -vv", Summary: "Log to stderr."},
		},
		Examples: []string{
			"birdcrawl bookmarks",
			"birdcrawl tweets --limit 10",
			"birdcrawl search \"boat trip\" --after 2026-01-01",
			"birdcrawl open t7k3f",
		},
		Footer: []string{
			"Run 'birdcrawl COMMAND --help' for flags and details.",
			"Logs: ~/.opentrawl/birdcrawl/logs/current.log",
		},
	}
}
