package cli

import (
	"io"
	"strings"
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
	_, _ = io.WriteString(w, `birdcrawl: local X archive crawler

usage:
  birdcrawl metadata [--json]
  birdcrawl status [--json]
  birdcrawl import archive PATH [--json]
  birdcrawl sync [--json]
  birdcrawl search QUERY [--limit N] [--after RFC3339] [--before RFC3339] [--json]
  birdcrawl open birdcrawl:tweet/ID [--json]
  birdcrawl stats [--window 30d] [--by likes|retweets|replies] [--limit N] [--json]
  birdcrawl doctor [--json]
  birdcrawl version

global flags:
  --db PATH       use a specific archive database
  --config PATH   use a specific crawler config
  --json          write JSON
  -v, -vv         write log lines to stderr

Diagnostics: run with -v, or read ~/.birdcrawl/birdcrawl/logs/current.log
`)
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
	var out strings.Builder
	printUsage(&out)
	return out.String()
}
