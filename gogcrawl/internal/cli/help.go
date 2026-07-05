package cli

import (
	"fmt"
	"io"
	"strings"
)

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `gogcrawl archives Gmail through the authenticated gog CLI.

Usage:
  gogcrawl metadata [--json]
  gogcrawl status [--json]
  gogcrawl sync [--json]
  gogcrawl search [QUERY] [--limit N] [--after DATE] [--before DATE] [--who PERSON] [--json]
  gogcrawl who NAME [--json]
  gogcrawl open REF [--json]
  gogcrawl doctor [--json]
  gogcrawl contacts export [--json]
  gogcrawl help COMMAND
  gogcrawl --version

Global flags:
  --archive PATH     Local gogcrawl archive database path.
  --backup-repo PATH Local gog backup repository path.
  --json             Print machine-readable output.
  -v, --verbose      Stream diagnostics to stderr. Use -vv for debug detail.

Output:
  Default output is compact text for humans and agents.
  Use --json for the frozen control contract.

Diagnostics: run with -v, or read ~/.opentrawl/gogcrawl/logs/gogcrawl.log
`)
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
	_, err = fmt.Fprintln(w, "\nDiagnostics: run with -v, or read ~/.opentrawl/gogcrawl/logs/gogcrawl.log")
	return err
}
