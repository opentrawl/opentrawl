package cli

import (
	"fmt"
	"io"
	"strings"
)

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `calcrawl reads the local Apple Calendar store.

Usage:
  calcrawl metadata [--json]
  calcrawl status [--json]
  calcrawl sync [--json]
  calcrawl search [QUERY] [--who NAME] [--limit N] [--after DATE] [--before DATE] [--json]
  calcrawl who NAME [--json]
  calcrawl open REF [--json]
  calcrawl doctor [--json]
  calcrawl contacts export [--json]
  calcrawl help COMMAND
  calcrawl --version

Global flags:
  --json          Print machine-readable output.
  -v, --verbose  Stream diagnostics to stderr. Use -vv for debug detail.

Output:
  Default output is compact text for humans and agents.
  Use --json for stable machine parsing.
  Search returns 20 rows by default. Use --limit N to choose a different count.
  Search may omit QUERY when --who, --after or --before is present.

Diagnostics: run with -v, or read ~/.calcrawl/logs/calcrawl.log
`)
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
  calcrawl search [QUERY] [--who NAME] [--limit N] [--after DATE] [--before DATE] [--json]

Search archived calendar events.

Flags:
  --who NAME    Resolve a person, then filter to events where they are organizer or attendee.
  --limit N      Results to print. Default: 20.
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
	_, err = fmt.Fprintln(w, "\nDiagnostics: run with -v, or read ~/.calcrawl/logs/calcrawl.log")
	return err
}
