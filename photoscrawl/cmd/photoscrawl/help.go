package main

import (
	"fmt"
	"io"
	"path/filepath"

	ckusage "github.com/openclaw/crawlkit/usage"
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
Without --model only mechanical metadata is classified.
--limit bounds a run (default 100); --all classifies every queued asset.`,
	"search": `Usage: photoscrawl search <query> [--after <date>] [--before <date>] [--limit <n>] [--all] [--json] [--db <path>]

Full-text search over photo cards, filenames, albums, and places.
Terms are stemmed and OR-combined; results are ranked, best first.
Dates take 2006-01-02 or RFC 3339 forms.
--limit is honored exactly (default 20); --all returns every match.`,
	"open": `Usage: photoscrawl open <ref> [--json] [--db <path>]

Full card for one asset: capture facts, place, summary, description.
Accepts the canonical photoscrawl:asset/<32-hex> ref or a short alias
from search output.`,
}

func printHelp(w io.Writer, paths archive.Paths) error {
	doc := ckusage.Doc{
		Tool:    "photoscrawl",
		Tagline: "your Apple Photos archive: search, open and classify your photos",
		Groups: []ckusage.Group{
			{Title: "Read your archive", Commands: []ckusage.Command{
				{Name: "search", Summary: "Ranked full-text search over cards, albums and places."},
				{Name: "open", Summary: "Full card for one asset by ref or short alias."},
			}},
			{Title: "Keep it fresh", Commands: []ckusage.Command{
				{Name: "sync", Summary: "Import the Apple Photos library into the archive."},
				{Name: "classify", Summary: "Write metadata, place and model-card observations."},
			}},
			{Title: "Health", Commands: []ckusage.Command{
				{Name: "status", Summary: "Archive freshness and asset counts."},
				{Name: "doctor", Summary: "Source access and archive health checks."},
				{Name: "metadata", Summary: "Control manifest for trawl discovery."},
			}},
		},
		Flags: []ckusage.Flag{
			{Name: "--json", Summary: "Machine-readable output (--format json is the same)."},
			{Name: "--db PATH", Summary: "Archive database path."},
		},
		Footer: []string{
			"Read commands time out after two minutes.",
			"Run 'photoscrawl <command> --help' for that command's flags.",
			"Logs: " + filepath.Join(paths.LogDir, "current.log"),
		},
	}
	_, err := fmt.Fprint(w, doc.Render())
	return err
}

// printVerbHelp reports whether it knew the verb; unknown verbs fall through
// to normal dispatch so their usage errors stay intact.
func printVerbHelp(w io.Writer, paths archive.Paths, verb string) (bool, error) {
	text, ok := verbHelp[verb]
	if !ok {
		return false, nil
	}
	if _, err := fmt.Fprintln(w, text); err != nil {
		return true, err
	}
	if _, err := fmt.Fprintf(w, "\nLogs: %s\n", filepath.Join(paths.LogDir, "current.log")); err != nil {
		return true, err
	}
	return true, nil
}
