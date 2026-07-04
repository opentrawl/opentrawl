package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/usage"
)

const diagnosticsFooter = "Diagnostics: run with -v, or read ~/.imsgcrawl/logs/imsgcrawl.log"

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, topHelpDoc().Render())
}

func topHelpDoc() usage.Doc {
	return usage.Doc{
		Tool:    "imsgcrawl",
		Tagline: "reads local iMessage Messages data",
		Groups: []usage.Group{
			{Title: "Read your archive", Commands: []usage.Command{
				{Name: "chats", Summary: "Archived chats, newest first."},
				{Name: "messages", Summary: "Messages in one chat, newest first."},
				{Name: "who", Summary: "Resolve a name to archived participants."},
				{Name: "search", Summary: "Search archived message text."},
				{Name: "open", Summary: "Open one message with nearby context."},
			}},
			{Title: "Keep it fresh", Commands: []usage.Command{
				{Name: "sync", Summary: "Refresh the archive from Messages."},
			}},
			{Title: "Health", Commands: []usage.Command{
				{Name: "status", Summary: "Source and archive readability and counts."},
				{Name: "doctor", Summary: "Check source, archive and Full Disk Access."},
				{Name: "metadata", Summary: "Print crawlkit control metadata."},
			}},
			{Title: "Export", Commands: []usage.Command{
				{Name: "contacts export", Summary: "Export phone contacts from Messages."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--json", Summary: "Print machine-readable JSON output."},
			{Name: "--db PATH", Summary: "Messages source database path."},
			{Name: "--archive PATH", Summary: "Local imsgcrawl archive path."},
			{Name: "-v, -vv", Summary: "Stream diagnostics to stderr."},
		},
		Examples: []string{
			`imsgcrawl chats --limit 20`,
			`imsgcrawl search "launch"`,
			`imsgcrawl open REF`,
			`imsgcrawl who "alex"`,
		},
		Footer: []string{
			"Run 'imsgcrawl help COMMAND' for flags and details.",
			diagnosticsFooter,
		},
	}
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
  --limit N   Chats to print. Default: 50.
  --all       Print all chats. Use explicitly for complete local output.
`)
	case "messages":
		_, err = fmt.Fprint(w, `Usage:
  imsgcrawl [--archive PATH] messages --chat ID [--limit N|--all] [--asc] [--json]

List archived messages for one chat.

Flags:
  --chat ID   Chat ID from imsgcrawl chats.
  --limit N   Messages to print. Default: 20.
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
  --limit N     Search results to print. Default: 20.
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
	_, err = fmt.Fprintln(w, "\n"+diagnosticsFooter)
	return err
}
