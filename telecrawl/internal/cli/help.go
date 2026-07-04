package cli

import (
	"io"
	"strings"
)

const diagnosticsLine = "Diagnostics: run with -v, or read ~/.telecrawl/logs/telecrawl.log"

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
	_, _ = io.WriteString(w, `telecrawl: Telegram archive probe/import CLI

usage:
  telecrawl doctor [--path PATH] [--json]
  telecrawl metadata [--json]
  telecrawl import [--path PATH] [--chat ID] [--dialogs-limit N] [--messages-limit N] [--fetch-media] [--json]
  telecrawl status [--json]
  telecrawl folders [--json]
  telecrawl contacts [--limit N] [--json]
  telecrawl contacts export [--json]
  telecrawl chats [--limit N] [--unread] [--folder ID] [--json]
  telecrawl topics --chat ID [--limit N] [--json]
  telecrawl messages [--chat ID] [--topic ID] [--limit N] [--after DATE] [--json]
  telecrawl search ["query"] [--who PERSON] [--chat ID] [--topic ID] [--limit N] [--json]
  telecrawl who NAME [--json]
  telecrawl open telecrawl:msg/ID [--json]
  telecrawl backup init|push|pull|status|snapshots [--json]
  telecrawl version

global flags:
  --json         Print machine-readable JSON output.
  --db PATH      Archive database path.
  --source PATH  Telegram source path for doctor/import.
  -v, --verbose  Stream diagnostics to stderr. Use -vv for debug detail.

notes:
  import auto-detects Telegram Desktop tdata or native macOS Postbox data
  import archives local cached Postbox media by default; --fetch-media also tries Telegram cloud media
  backup writes encrypted age shards to a git repo

Diagnostics: run with -v, or read ~/.telecrawl/logs/telecrawl.log
`)
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
		return "usage: telecrawl doctor [--path PATH] [--json]\n\nChecks Telegram source access and archive readability.\n"
	case "metadata":
		return "usage: telecrawl metadata [--json]\n\nPrints the crawler manifest and contract capabilities.\n"
	case "import", "sync", "wiretap":
		return "usage: telecrawl import [--path PATH] [--chat ID] [--dialogs-limit N] [--messages-limit N] [--fetch-media] [--json]\n\nImports Telegram data into the local archive. This mutates the archive.\n"
	case "status":
		return "usage: telecrawl status [--json]\n\nReads archive status without importing or syncing.\n"
	case "folders":
		return "usage: telecrawl folders [--json]\n\nLists Telegram folders from the local archive.\n"
	case "contacts":
		if len(args) > 1 && args[1] == "export" {
			return "usage: telecrawl contacts export [--json]\n\nExports safe contact records from archived conversation evidence.\n"
		}
		return "usage: telecrawl contacts [--limit N] [--json]\n\nLists contacts from the local archive.\n"
	case "chats":
		return "usage: telecrawl chats [--limit N] [--unread] [--folder ID] [--json]\n\nLists chats from the local archive.\n"
	case "topics":
		return "usage: telecrawl topics --chat ID [--limit N] [--json]\n\nLists forum topics for one archived chat.\n"
	case "messages":
		return "usage: telecrawl messages [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--asc] [--json]\n\nLists a bounded set of archived messages.\n"
	case "search":
		return "usage: telecrawl search [\"query\"] [--who PERSON] [--chat ID] [--topic ID] [--sender ID] [--limit N] [--after DATE] [--before DATE] [--from-me|--from-them] [--media] [--pinned] [--json]\n\nSearches archived messages and returns refs for telecrawl open. Query is optional when --who, --after, or --before is set.\n"
	case "who":
		return "usage: telecrawl who NAME [--json]\n\nResolves a name or identifier to archived Telegram participants.\n"
	case "open":
		return "usage: telecrawl open telecrawl:msg/ID [--json]\n\nOpens one search ref with a bounded same-chat context window.\n"
	case "backup":
		if len(args) > 1 {
			return backupCommandUsage(args[1])
		}
		return "usage: telecrawl backup init|push|pull|status|snapshots [--json]\n\nManages encrypted archive backups.\n"
	case "version":
		return "usage: telecrawl version\n\nPrints the telecrawl version.\n"
	default:
		return topUsageText()
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
		return "usage: telecrawl backup init|push|pull|status|snapshots [--json]\n\nManages encrypted archive backups.\n"
	}
}

func topUsageText() string {
	var out strings.Builder
	printUsage(&out)
	return out.String()
}
