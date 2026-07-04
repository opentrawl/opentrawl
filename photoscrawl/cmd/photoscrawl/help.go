package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/openclaw/photoscrawl/internal/archive"
)

var verbHelp = map[string]string{
	"metadata": `Usage: photoscrawl metadata [--json]

Control manifest for trawl discovery: capabilities, commands, privacy.`,
	"status": `Usage: photoscrawl status [--json] [--db <path>]

Archive freshness, asset counts, and the last run's log tail.`,
	"doctor": `Usage: photoscrawl doctor [--json] [--db <path>] [--library <path>]

Source access and archive health checks.`,
	"sync": `Usage: photoscrawl sync [--library <path>] [--json] [--db <path>]

Import the Apple Photos library into the archive. Safe to re-run;
only new and changed assets are written.`,
	"classify": `Usage: photoscrawl classify [--model <id>] [--limit <n>] [--all] [--json] [--db <path>]

Write metadata, place, and model-card observations for queued assets.
Without --model only mechanical metadata is classified.`,
	"search": `Usage: photoscrawl search <query> [--after <date>] [--before <date>] [--limit <n>] [--json] [--db <path>]

Full-text search over photo cards, filenames, albums, and places.
Terms are stemmed and OR-combined; results are ranked, best first.
Dates take 2006-01-02 or RFC 3339 forms.`,
	"open": `Usage: photoscrawl open <ref> [--json] [--db <path>]

Full card for one asset: capture facts, place, summary, description.
Accepts the canonical photoscrawl:asset/<32-hex> ref or a short alias
from search output.`,
}

func printHelp(w io.Writer, paths archive.Paths) {
	fmt.Fprintf(w, `photoscrawl — Apple Photos crawler for the trawl suite

Usage: photoscrawl <command> [flags]

Commands:
  metadata   control manifest for trawl discovery
  status     archive freshness and counts
  doctor     source access and archive health checks
  sync       import the Photos library into the archive
  classify   write metadata, place, and model-card observations
  search     ranked full-text search over cards, albums, and places
  open       full card for one asset by ref or short alias

All commands take --json and --format. Read commands time out after
two minutes. Every run logs to:
  %s

Run 'photoscrawl <command> --help' for that command's flags.
`, filepath.Join(paths.LogDir, "current.log"))
}

// printVerbHelp reports whether it knew the verb; unknown verbs fall through
// to normal dispatch so their usage errors stay intact.
func printVerbHelp(w io.Writer, paths archive.Paths, verb string) bool {
	text, ok := verbHelp[verb]
	if !ok {
		return false
	}
	fmt.Fprintln(w, text)
	fmt.Fprintf(w, "\nLogs: %s\n", filepath.Join(paths.LogDir, "current.log"))
	return true
}
