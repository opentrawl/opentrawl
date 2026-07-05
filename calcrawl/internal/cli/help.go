package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/usage"
)

const diagnosticsLine = "Diagnostics: run with -v, or read ~/.opentrawl/calcrawl/logs/calcrawl.log"

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, topHelpDoc().Render())
}

func topHelpDoc() usage.Doc {
	return usage.Doc{
		Tool:    "calcrawl",
		Tagline: "your Apple Calendar archive: events, people and places",
		Groups: []usage.Group{
			{Title: "Read your archive", Commands: []usage.Command{
				{Name: "search", Summary: "Search archived events, newest first."},
				{Name: "open", Summary: "Open one event in full detail."},
				{Name: "who", Summary: "Resolve a person across names, emails and phones."},
				{Name: "contacts export", Summary: "Export attendees that have phone numbers."},
			}},
			{Title: "Keep it fresh", Commands: []usage.Command{
				{Name: "sync", Summary: "Refresh the archive from Calendar.app."},
			}},
			{Title: "Health", Commands: []usage.Command{
				{Name: "status", Summary: "Show archive freshness and counts."},
				{Name: "doctor", Summary: "Check source access and archive readiness."},
				{Name: "metadata", Summary: "Print the crawler manifest."},
				{Name: "version", Summary: "Print the calcrawl version."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--json", Summary: "Print machine-readable JSON output."},
			{Name: "-v, -vv", Summary: "Stream diagnostics to stderr; -vv adds debug detail."},
			{Name: "--version", Summary: "Print the calcrawl version."},
		},
		Examples: []string{
			"calcrawl search \"dentist\" --limit 10",
			"calcrawl search --who \"katja\" --after 2026-01-01",
			"calcrawl open REF",
			"calcrawl who \"alice\"",
		},
		Footer: []string{
			"Run 'calcrawl COMMAND --help' for flags and details.",
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
  calcrawl metadata [--json]

Print crawlkit control metadata.
`)
	case "status":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl status [--json]

Report archive freshness and aggregate counts.
`)
	case "sync":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl sync [--json]

Refresh the local calendar archive from Calendar.app's SQLite store.
`)
	case "search":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl search [QUERY] [--who NAME] [--limit N | --all] [--after DATE] [--before DATE] [--json]

Search archived calendar events.

Flags:
  --who NAME     Resolve a person, then filter to events where they are organizer or attendee.
  --limit N      Results to print. Default: 20.
  --all          Print every match, no limit.
  --after DATE   Include events at or after DATE.
  --before DATE  Include events at or before DATE.

QUERY is optional when --who, --after or --before is present.
Use calcrawl who NAME to inspect ambiguous people.
`)
	case "who":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl who NAME [--json]

Resolve a person against archived organizers and attendees.
`)
	case "open":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl open REF [--json]

Open one archived event ref or short alias returned by search text.
`)
	case "doctor":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl doctor [--json]

Check source store access, archive presence and schema readiness.
`)
	case "version":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl version

Print the calcrawl version. Same as calcrawl --version.
`)
	case "contacts", "contacts export":
		_, err = fmt.Fprint(w, `Usage:
  calcrawl contacts export [--json]

Export attendee identities that have phone numbers in the crawlkit contact-export shape.
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
