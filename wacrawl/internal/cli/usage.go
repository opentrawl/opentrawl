package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/usage"
)

const diagnosticsLine = "Diagnostics: run with -v, or read ~/.opentrawl/wacrawl/logs/wacrawl.log"

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, topHelpDoc().Render())
}

func topHelpDoc() usage.Doc {
	return usage.Doc{
		Tool:    "wacrawl",
		Tagline: "your WhatsApp archive: chats, messages and contacts",
		Groups: []usage.Group{
			{Title: "Read your archive", Commands: []usage.Command{
				{Name: "chats", Summary: "Your chats, newest first."},
				{Name: "unread", Summary: "Chats with unread messages."},
				{Name: "messages", Summary: "Archived messages, newest first."},
				{Name: "contacts", Summary: "Export archived contacts."},
				{Name: "search", Summary: "Search archived messages."},
				{Name: "open", Summary: "Open one message with nearby context."},
				{Name: "who", Summary: "Resolve a person across names and identifiers."},
				{Name: "sql", Summary: "Run a read-only SQL query."},
				{Name: "web", Summary: "Browse the archive in a private web viewer."},
			}},
			{Title: "Keep it fresh", Commands: []usage.Command{
				{Name: "import", Summary: "Read WhatsApp Desktop data into the archive."},
				{Name: "sync", Summary: "Alias for import."},
				{Name: "backup", Summary: "Manage encrypted Git backups."},
			}},
			{Title: "Health", Commands: []usage.Command{
				{Name: "status", Summary: "Show archive status and counts."},
				{Name: "doctor", Summary: "Check source access and archive readability."},
				{Name: "metadata", Summary: "Print crawlkit control metadata."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--json", Summary: "Print machine-readable JSON output."},
			{Name: "--db PATH", Summary: "Archive database path."},
			{Name: "--source PATH", Summary: "WhatsApp Desktop source path."},
			{Name: "--version", Summary: "Print the CLI version."},
			{Name: "-v, -vv", Summary: "Stream diagnostics to stderr."},
		},
		Examples: []string{
			"wacrawl chats --limit 20",
			`wacrawl search "invoice" --from-them --after 2026-01-01`,
			"wacrawl open wacrawl:msg/MESSAGE_ID",
			`wacrawl who "Alice"`,
		},
		Footer: []string{
			"Run 'wacrawl help COMMAND' for flags and details.",
			diagnosticsLine,
		},
	}
}

func commandWantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printCommandUsage(w io.Writer, topic ...string) bool {
	name := strings.Join(topic, " ")
	switch name {
	case "doctor":
		_, _ = fmt.Fprint(w, `Inspect the WhatsApp Desktop source and archive paths.

Usage:
  wacrawl doctor [--source PATH]

Flags:
  --source PATH   WhatsApp Desktop source path.

Examples:
  wacrawl doctor
  wacrawl --json doctor
`)
	case "import", "sync":
		_, _ = fmt.Fprintf(w, `Snapshot WhatsApp Desktop SQLite data into the archive.

Usage:
  wacrawl %s [--source PATH] [--copy-media]

Flags:
  --source PATH   WhatsApp Desktop source path.
  --copy-media    Copy referenced media files into media/ next to the archive DB.

Examples:
  wacrawl %s
  wacrawl %s --copy-media
  wacrawl --db /tmp/wacrawl.db %s
`, name, name, name, name)
	case "status":
		_, _ = fmt.Fprint(w, `Show archive status, counts, date span, unread counts, and last import metadata.

Usage:
  wacrawl status

Examples:
  wacrawl status
  wacrawl --json status
`)
	case "chats":
		_, _ = fmt.Fprint(w, `List archived chats.

Usage:
  wacrawl chats [--limit N] [--unread]

Flags:
  --limit N   Chats to return. Default: 50.
  --unread    Show only chats with unread messages.

Examples:
  wacrawl chats --limit 20
  wacrawl chats --unread
  wacrawl --json chats --limit 100
`)
	case "contacts", "contacts export":
		_, _ = fmt.Fprint(w, `Export archived contacts.

Usage:
  wacrawl [--json] contacts export

Examples:
  wacrawl --json contacts export
`)
	case "who":
		_, _ = fmt.Fprint(w, `Resolve a person from archived participants.

Usage:
  wacrawl who <name>

The resolver matches names, aliases and identifiers by case-insensitive prefix,
substring and close spelling. JSON output is the contract shape used by search.

Examples:
  wacrawl who "Alice"
  wacrawl --json who "Alice"
`)
	case "unread":
		_, _ = fmt.Fprint(w, `List chats with unread messages.

Usage:
  wacrawl unread [--limit N]

Flags:
  --limit N   Chats to return. Default: 50.

Examples:
  wacrawl unread
  wacrawl unread --limit 20
`)
	case "messages":
		_, _ = fmt.Fprint(w, `List archived messages.

Usage:
  wacrawl messages [flags]

Flags:
  --chat JID       Filter by chat JID.
  --sender JID     Filter by sender JID.
  --limit N        Messages to return. Default: 20.
  --after TIME     Only messages after RFC3339 or YYYY-MM-DD.
  --before TIME    Only messages before RFC3339 or YYYY-MM-DD.
  --from-me        Only messages sent by me.
  --from-them      Only messages received from others.
  --has-media      Only messages with media metadata.
  --asc            Show oldest messages first.

Examples:
  wacrawl messages --limit 20
  wacrawl messages --chat 1234567890@s.whatsapp.net --asc
  wacrawl messages --after 2026-01-01 --from-them
  wacrawl --json messages --has-media --limit 100
`)
	case "search":
		_, _ = fmt.Fprint(w, `Search archived messages.

Usage:
  wacrawl search [flags] [query]
  wacrawl search [query] [flags]

Flags:
  --chat JID       Filter by chat JID.
  --sender JID     Filter by sender JID.
  --who NAME       Resolve NAME, then filter to that sender, recipient, or chat member.
  --limit N        Messages to return. Default: 20.
  --after TIME     Only messages after RFC3339 or YYYY-MM-DD.
  --before TIME    Only messages before RFC3339 or YYYY-MM-DD.
  --from-me        Only messages sent by me.
  --from-them      Only messages received from others.
  --has-media      Only messages with media metadata.
  --asc            Show oldest messages first.

Examples:
  wacrawl search "invoice"
  wacrawl search "invoice" --who "Alice Example"
  wacrawl search --who "Alice Example"
  wacrawl search "flight" --after 2026-01-01 --from-them
  wacrawl --json search --chat 1234567890@s.whatsapp.net "release notes"
`)
	case "open":
		_, _ = fmt.Fprint(w, `Open an archived message by ref.

Usage:
  wacrawl open <ref>

The ref must come from wacrawl search. Use the short ref from text output or
the full ref that looks like wacrawl:msg/MESSAGE_ID.

Examples:
  wacrawl open abc23
  wacrawl open wacrawl:msg/MESSAGE_ID
  wacrawl --json open wacrawl:msg/MESSAGE_ID
`)
	case "sql":
		_, _ = fmt.Fprint(w, `Run a read-only SQL query against the archive database.

Usage:
  wacrawl sql <select query>

Examples:
  wacrawl sql "SELECT count(*) FROM messages"
  wacrawl --json sql "SELECT chat_jid, count(*) FROM messages GROUP BY chat_jid"
`)
	case "web":
		_, _ = fmt.Fprint(w, `Browse the local archive in a private web viewer.

The viewer binds only to 127.0.0.1 and requires a random key printed in its URL.
It reads archive status, chats, messages, and search results without serving media
files or exposing configuration and write controls.

Usage:
  wacrawl web [--port N]

Flags:
  --port N   Loopback port. Default: choose a free random port.

Examples:
  wacrawl web
  wacrawl web --port 8787
`)
	case "backup":
		_, _ = fmt.Fprint(w, `Manage encrypted Git backups of the wacrawl archive.

Usage:
  wacrawl backup <init|push|pull|status|snapshots> [flags]

Commands:
  init      Create backup config, age identity, and first encrypted backup.
  push      Export the archive and push encrypted shards.
  pull      Restore encrypted shards into the configured archive DB.
  status    Inspect backup config and manifest.
  snapshots List restorable Git backup snapshots and tags.

Common flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.

Examples:
  wacrawl backup status
  wacrawl backup snapshots
  wacrawl backup push
  wacrawl backup init --repo ~/Projects/backup-wacrawl --remote https://github.com/steipete/backup-wacrawl.git
`)
	case "backup init":
		_, _ = fmt.Fprint(w, `Initialize encrypted Git backup configuration.

Usage:
  wacrawl backup init [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
`)
	case "backup push":
		_, _ = fmt.Fprint(w, `Export and push encrypted archive shards and copied media.

Usage:
  wacrawl backup push [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --recipient AGE    Age recipient. Repeatable.
  --no-push          Commit locally without pushing.
  --no-media         Omit copied media files from this backup.
  --tag NAME         Tag the resulting backup commit.
`)
	case "backup pull":
		_, _ = fmt.Fprint(w, `Restore encrypted archive shards and copied media.

Usage:
  wacrawl backup pull [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
  --no-media         Restore archive rows without copied media files.
  --ref REF          Restore a tag, commit, or branch without changing checkout.
`)
	case "backup status":
		_, _ = fmt.Fprint(w, `Show encrypted backup status and manifest metadata.

Usage:
  wacrawl backup status [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --identity PATH    Age identity path.
`)
	case "backup snapshots":
		_, _ = fmt.Fprint(w, `List restorable encrypted backup snapshots from Git history.

Usage:
  wacrawl backup snapshots [flags]

Flags:
  --config PATH      Backup config path.
  --repo PATH        Backup Git repository path.
  --remote URL       Backup Git remote.
  --limit N          Maximum snapshots to list. Default: 20.
`)
	default:
		return false
	}
	printDiagnosticsLine(w)
	return true
}

func printDiagnosticsLine(w io.Writer) {
	_, _ = fmt.Fprintln(w, "\n"+diagnosticsLine)
}
