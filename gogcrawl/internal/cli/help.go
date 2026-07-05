package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/usage"
)

const diagnosticsLine = "Diagnostics: run with -v, or read ~/.opentrawl/gogcrawl/logs/gogcrawl.log"

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, topHelpDoc().Render())
}

func topHelpDoc() usage.Doc {
	return usage.Doc{
		Tool:    "gogcrawl",
		Tagline: "your Gmail archive: every message, searchable locally",
		Groups: []usage.Group{
			{Title: "Read your archive", Commands: []usage.Command{
				{Name: "search", Summary: "Search archived mail text."},
				{Name: "open", Summary: "Open one message in full detail."},
				{Name: "who", Summary: "Resolve a person across names and addresses."},
				{Name: "contacts export", Summary: "Export Google contacts that have phone numbers."},
			}},
			{Title: "Keep it fresh", Commands: []usage.Command{
				{Name: "sync", Summary: "Refresh the archive from Gmail via gog."},
			}},
			{Title: "Health", Commands: []usage.Command{
				{Name: "status", Summary: "Show archive freshness and counts."},
				{Name: "doctor", Summary: "Check gog, auth and archive readiness."},
				{Name: "metadata", Summary: "Print the crawler manifest."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--archive PATH", Summary: "Archive database path."},
			{Name: "--backup-repo PATH", Summary: "Local gog backup repository path."},
			{Name: "--json", Summary: "Print machine-readable JSON output."},
			{Name: "-v, -vv", Summary: "Stream diagnostics to stderr; -vv adds debug detail."},
			{Name: "--version", Summary: "Print the gogcrawl version."},
		},
		Examples: []string{
			`gogcrawl search "invoice" --limit 10`,
			`gogcrawl search --who "katja" --after 2026-01-01`,
			`gogcrawl open REF`,
			`gogcrawl who "alice"`,
		},
		Footer: []string{
			"Run 'gogcrawl COMMAND --help' for flags and details.",
			diagnosticsLine,
		},
	}
}

func printCommandUsage(w io.Writer, args []string) error {
	topic := strings.Join(args, " ")
	var err error
	switch topic {
	case "metadata":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl metadata [--json]

Print control metadata.
`)
	case "status":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl status [--json]

Report archive state, freshness, counts and safe auth state.
`)
	case "sync":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl sync [--query QUERY] [--max N] [--json]

Refresh the local Gmail archive from gog.

Flags:
  --query QUERY  Optional Gmail query for bounded test syncs.
  --max N        Maximum messages to back up. Default: 0, meaning all.
`)
	case "search":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl search [QUERY] [--limit N] [--after DATE] [--before DATE] [--who PERSON] [--json]

Search archived Gmail subject and body text.
QUERY is optional when --who, --after or --before is present.

Flags:
  --limit N      Results to return. Default: 20.
  --after DATE   Only messages after RFC3339 or YYYY-MM-DD.
  --before DATE  Only messages before RFC3339 or YYYY-MM-DD.
  --who PERSON   Resolve a name, or filter by an exact email, phone or handle.
`)
	case "who":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl who NAME [--json]

Resolve a Gmail participant name or identifier.
`)
	case "open":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl open REF [--json]

Open one archived Gmail message by full ref or short ref.
`)
	case "doctor":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl doctor [--json]

Check gog, auth and archive readiness.
`)
	case "contacts", "contacts export":
		_, err = fmt.Fprint(w, `Usage:
  gogcrawl contacts export [--json]

Export Google contacts with display names and phone numbers.
`)
	default:
		return usageErr(fmt.Errorf("unknown help topic %q", topic))
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, "\n"+diagnosticsLine)
	return err
}
