package cli

import (
	"fmt"
	"io"

	"github.com/alecthomas/kong"
	"github.com/openclaw/crawlkit/usage"
)

// helpWithDiagnostics renders the shared crawler help layout for the top
// level and defers to kong for per-command pages. Every page ends with the
// diagnostics line (docs/contract.md, Logging).
func helpWithDiagnostics(options kong.HelpOptions, ctx *kong.Context) error {
	if ctx.Selected() == nil {
		_, err := io.WriteString(ctx.Stdout, clawdexUsageDoc().Render())
		return err
	}
	if err := kong.DefaultHelpPrinter(options, ctx); err != nil {
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "\n%s\n", diagnosticsLine())
	return err
}

func clawdexUsageDoc() usage.Doc {
	return usage.Doc{
		Tool:    "clawdex",
		Tagline: "your contacts: one person per markdown file, indexed and searchable",
		Groups: []usage.Group{
			{Title: "Read your contacts", Commands: []usage.Command{
				{Name: "person list", Summary: "People A to Z with email, phone and tags."},
				{Name: "person show", Summary: "One person in full, by name, email or phone."},
				{Name: "who", Summary: "Resolve a name fragment to candidate people."},
				{Name: "search", Summary: "Search people and notes."},
				{Name: "contacts export", Summary: "Contact rows for other crawlers."},
			}},
			{Title: "Change your contacts", Commands: []usage.Command{
				{Name: "import", Summary: "Pull contacts in from Apple, Google or a crawler."},
				{Name: "export vcard", Summary: "Write people out as vCards."},
			}},
			{Title: "Repo and health", Commands: []usage.Command{
				{Name: "init", Summary: "Create the contacts repo."},
				{Name: "config", Summary: "Show or set clawdex config."},
				{Name: "git", Summary: "Status, commit, pull and push for the data repo."},
				{Name: "status", Summary: "People and source counts."},
				{Name: "doctor", Summary: "Diagnose problems; every failure has a remedy."},
				{Name: "metadata", Summary: "Machine-readable manifest for trawl."},
			}},
		},
		Flags: []usage.Flag{
			{Name: "--config PATH", Summary: "Config file path."},
			{Name: "--repo PATH", Summary: "Contacts data repo path."},
			{Name: "--json", Summary: "Machine-readable output."},
			{Name: "--dry-run", Summary: "Preview changes without writing."},
			{Name: "-v, -vv", Summary: "Log to stderr."},
		},
		Examples: []string{
			"clawdex who alice",
			"clawdex person show \"Ada Lovelace\"",
			"clawdex search \"boat trip\" --limit 5",
		},
		Footer: []string{
			"Run 'clawdex COMMAND --help' for flags and details.",
			diagnosticsLine(),
		},
	}
}
