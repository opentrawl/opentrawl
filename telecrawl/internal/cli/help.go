package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/usage"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

const diagnosticsLine = "Logs: ~/.telecrawl/logs/telecrawl.log"

func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		switch arg {
		case "--help", "-help", "-h":
			return true
		}
	}
	return false
}

func printUsage(w io.Writer) {
	_, _ = io.WriteString(w, topHelpDoc().Render())
}

func printCommandUsage(w io.Writer, args []string) {
	text := commandUsage(args)
	if !strings.Contains(text, diagnosticsLine) {
		text = strings.TrimRight(text, "\n") + "\n\n" + diagnosticsLine + "\n"
	}
	_, _ = io.WriteString(w, text)
}

func commandUsage(args []string) string {
	if len(args) == 0 {
		return topUsageText()
	}
	switch args[0] {
	case "doctor":
		return "usage: telecrawl doctor [--path PATH] [--json]\n\nCheck Telegram source access and archive readability.\n"
	case "metadata":
		return "usage: telecrawl metadata [--json]\n\nPrint the crawler manifest and contract capabilities.\n"
	case "import", "sync", "wiretap":
		return "usage: telecrawl import [--path PATH] [--chat ID] [--dialogs-limit N] [--messages-limit N] [--fetch-media] [--json]\n\nRead Telegram Desktop data into the local archive. This mutates the archive.\n"
	case "status":
		return "usage: telecrawl status [--json]\n\nShow archive health and counts without importing.\n"
	case "folders":
		return "usage: telecrawl folders [--json]\n\nList Telegram folders from the local archive.\n"
	case "contacts":
		if len(args) > 1 && args[1] == "export" {
			return "usage: telecrawl contacts export [--json]\n\nExport safe contact records from archived conversation evidence.\n"
		}
		return "usage: telecrawl contacts [--limit N] [--json]\n\nList people from the local archive.\n"
	case "chats":
		return fmt.Sprintf("usage: telecrawl chats [--limit N] [--unread] [--folder ID] [--json]\n\nList your chats, newest first. Imports keep at most %d messages per chat by default; capped counts display as %d+.\n", telegramdesktop.DefaultMessagesLimit, telegramdesktop.DefaultMessagesLimit)
	case "topics":
		return "usage: telecrawl topics --chat ID [--limit N] [--json]\n\nList forum topics for one archived chat.\n"
	case "messages":
		return "usage: telecrawl messages [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--asc] [--json]\n\nList a bounded set of archived messages.\n"
	case "search":
		return "usage: telecrawl search [\"query\"] [--who PERSON] [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--json]\n\nSearch archived messages and return refs for telecrawl open. Query is optional with --who, --after, or --before.\n"
	case "who":
		return "usage: telecrawl who NAME [--json]\n\nResolve a person across handles and phones.\n"
	case "open":
		return "usage: telecrawl open REF [--json]\n\nOpen one message with a bounded same-chat context window.\n"
	case "backup":
		if len(args) > 1 {
			return backupCommandUsage(args[1])
		}
		return "usage: telecrawl backup init|push|pull|status|snapshots [--json]\n\nManage encrypted archive backups.\n"
	case "version":
		return "usage: telecrawl version\n\nPrint the telecrawl version.\n"
	default:
		return topUsageText()
	}
}

func topHelpDoc() usage.Doc {
	return usage.Doc{
		Tool:    "telecrawl",
		Tagline: "your Telegram archive: chats, messages and contacts",
		Groups: []usage.Group{
			{Title: "Read your archive", Commands: []usage.Command{
				{Name: "chats", Summary: "Your chats, newest first."},
				{Name: "topics", Summary: "Forum topics in one chat."},
				{Name: "messages", Summary: "Archived messages, newest first."},
				{Name: "contacts", Summary: "People from the archive."},
				{Name: "folders", Summary: "Telegram folders from the archive."},
				{Name: "search", Summary: "Search archived messages."},
				{Name: "open", Summary: "Open one message with nearby context."},
				{Name: "who", Summary: "Resolve a person across handles and phones."},
			}},
			{Title: "Keep it fresh", Commands: []usage.Command{
				{Name: "import", Summary: "Read Telegram Desktop data into the archive."},
				{Name: "backup", Summary: "Manage encrypted archive backups."},
			}},
			{Title: "Health", Commands: []usage.Command{
				{Name: "status", Summary: "Show archive health and counts."},
				{Name: "doctor", Summary: "Check source access and archive readability."},
				{Name: "metadata", Summary: "Print the crawler manifest."},
				{Name: "version", Summary: "Print the telecrawl version."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--json", Summary: "Print machine-readable JSON output."},
			{Name: "--db PATH", Summary: "Archive database path."},
			{Name: "--source PATH", Summary: "Telegram source path for doctor and import."},
			{Name: "-v, -vv", Summary: "Log to stderr."},
		},
		Examples: []string{
			"telecrawl chats --limit 20",
			"telecrawl search \"launch\"",
			"telecrawl open REF",
			"telecrawl who \"alex\"",
		},
		Footer: []string{
			"Run 'telecrawl COMMAND --help' for flags and details.",
			diagnosticsLine,
		},
	}
}

func backupCommandUsage(command string) string {
	switch command {
	case "init":
		return "usage: telecrawl backup init [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--recipient AGE-RECIPIENT] [--no-push] [--json]\n\nInitialises encrypted archive backup settings.\n"
	case "push":
		return "usage: telecrawl backup push [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--recipient AGE-RECIPIENT] [--tag TAG] [--no-push] [--json]\n\nExports and pushes an encrypted archive backup.\n"
	case "pull":
		return "usage: telecrawl backup pull [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--ref REF] [--json]\n\nRestores the archive from an encrypted backup.\n"
	case "status":
		return "usage: telecrawl backup status [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--json]\n\nReads backup repository status.\n"
	case "snapshots":
		return "usage: telecrawl backup snapshots [--config PATH] [--repo PATH] [--remote URL] [--identity PATH] [--limit N] [--json]\n\nLists encrypted backup snapshots.\n"
	default:
		return "usage: telecrawl backup init|push|pull|status|snapshots [--json]\n\nManage encrypted archive backups.\n"
	}
}

func topUsageText() string {
	var out strings.Builder
	printUsage(&out)
	return out.String()
}
