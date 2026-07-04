package cli

import (
	"fmt"
	"io"
	"strings"
)

func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `imsgcrawl reads local iMessage Messages data.

Usage:
  imsgcrawl [--db PATH] metadata [--json]
  imsgcrawl [--db PATH] [--archive PATH] sync [--json]
  imsgcrawl [--db PATH] [--archive PATH] status [--json]
  imsgcrawl [--db PATH] [--archive PATH] doctor [--json]
  imsgcrawl [--archive PATH] chats [--limit N|--all] [--json]
  imsgcrawl [--archive PATH] messages --chat ID [--limit N|--all] [--asc] [--json]
  imsgcrawl [--archive PATH] who NAME [--json]
  imsgcrawl [--archive PATH] search [--limit N] [--after TIME] [--before TIME] [--who NAME] [QUERY] [--json]
  imsgcrawl [--archive PATH] open REF [--json]
  imsgcrawl [--db PATH] contacts export [--json]
  imsgcrawl help COMMAND
  imsgcrawl --version

Global flags:
  --json       Print machine-readable JSON output.
  --db PATH    Messages source database path.
  --archive PATH
              Local imsgcrawl archive path.
  -v, --verbose
              Stream diagnostics to stderr. Use -vv for debug detail.

Output:
  Default output is compact text for humans and agents.
  Use --json for stable machine parsing.
  Use --all only when you explicitly want complete local output.

Help:
  imsgcrawl help chats
  imsgcrawl chats --help

Diagnostics: run with -v, or read ~/.imsgcrawl/logs/imsgcrawl.log
`)
}

func printCommandUsage(w io.Writer, args []string) error {
	topic := strings.Join(args, " ")
	var err error
	switch topic {
	case "metadata":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--db PATH] metadata [--json]

Print crawlkit control metadata.
`)
	case "sync":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--db PATH] [--archive PATH] sync [--json]

Refresh the local imsgcrawl archive from the Messages database.
`)
	case "status":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--db PATH] [--archive PATH] status [--json]

Report source/archive readability and aggregate counts.
`)
	case "doctor":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--db PATH] [--archive PATH] doctor [--json]

Check source, archive and Full Disk Access readiness.
`)
	case "chats":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--archive PATH] chats [--limit N|--all] [--json]

List archived chats.

Flags:
  --limit N   Maximum chats to print. Default: 50.
  --all       Print all chats. Use explicitly for complete local output.
`)
	case "messages":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--archive PATH] messages --chat ID [--limit N|--all] [--asc] [--json]

List archived messages for one chat.

Flags:
  --chat ID   Chat ID from imsgcrawl chats.
  --limit N   Maximum messages to print. Default: 20.
  --all       Print all messages for the chat.
  --asc       Show oldest messages first.
`)
	case "who":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--archive PATH] who NAME [--json]

Resolve a name, alias or identifier fragment to archived participants.
`)
	case "search":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--archive PATH] search [--limit N] [--after TIME] [--before TIME] [--who NAME] [QUERY] [--json]

Search archived message text.

Flags:
  --limit N     Maximum search results. Default: 20.
  --after TIME  Only messages at or after RFC3339 or YYYY-MM-DD.
  --before TIME Only messages at or before RFC3339 or YYYY-MM-DD.
  --who NAME    Resolve a person first, then filter by their exact identifiers.
`)
	case "open":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--archive PATH] open REF [--json]

Open one message ref from imsgcrawl search --json with a bounded chat context.
`)
	case "contacts", "contacts export":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--db PATH] contacts export [--json]

Export phone contacts from the Messages source database.
`)
	default:
		return usageErr(fmt.Errorf("unknown help topic %q", topic))
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, "\nDiagnostics: run with -v, or read ~/.imsgcrawl/logs/imsgcrawl.log")
	return err
}
