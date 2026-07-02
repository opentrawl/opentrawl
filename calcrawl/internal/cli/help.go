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
  calcrawl search QUERY [--limit N] [--after DATE] [--before DATE] [--json]
  calcrawl open REF [--json]
  calcrawl doctor [--json]
  calcrawl contacts export [--json]
  calcrawl help COMMAND
  calcrawl --version

Global flags:
  --json       Print machine-readable output.

Output:
  Default output is compact text for humans and agents.
  Use --json for stable machine parsing.
  Search returns 20 rows by default and never more than 200.
`)
}

func printCommandUsage(w io.Writer, args []string) error {
	topic := strings.Join(args, " ")
	switch topic {
	case "metadata":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl metadata [--json]

Print crawlkit control metadata.
`)
	case "status":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl status [--json]

Report archive freshness and aggregate counts.
`)
	case "sync":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl sync [--json]

Refresh the local calendar archive from Calendar.app's SQLite store.
`)
	case "search":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl search QUERY [--limit N] [--after DATE] [--before DATE] [--json]

Search archived calendar events.

Flags:
  --limit N      Maximum results. Default: 20. Maximum: 200.
  --after DATE   Include events at or after DATE.
  --before DATE  Include events at or before DATE.
`)
	case "open":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl open REF [--json]

Open one archived event ref returned by search.
`)
	case "doctor":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl doctor [--json]

Check source store access, archive presence and schema readiness.
`)
	case "contacts", "contacts export":
		_, _ = fmt.Fprint(w, `Usage:
  calcrawl contacts export [--json]

Export attendee identities that have phone numbers in the crawlkit contact-export shape.
`)
	default:
		return usageErr(fmt.Errorf("unknown help topic %q", topic))
	}
	return nil
}
