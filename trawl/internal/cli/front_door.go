package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// helpAgentsBlock is the agent-facing appendix `trawl --help` carries: the
// ordinary text path, ref grammar, and one runnable search-to-open transcript.
// JSON remains available for programs composing structured results.
const helpAgentsBlock = `Agents:
  Use ordinary command output: it is designed for people and agents.
  Refs are source:kind/id, for example imessage:msg/8842.
  Use --json only when writing a script or pipeline.
  Search, then open a hit by the ref it carries:
    trawl search "boat trip"
    trawl open imessage:msg/8842`

// writeFrontDoor renders bare `trawl`: the live Sources block and the first
// commands a cold reader should try. Source rows come from manifest headline
// declarations, so the door stays truthful as crawlers come and go.
func writeFrontDoor(w io.Writer) error {
	sources := discoverCrawlers(context.Background())
	sections := []string{
		sourcesBlock(sources),
		startHereBlock(sources),
	}
	_, err := fmt.Fprintln(w, strings.Join(sections, "\n\n"))
	return err
}

// sourcesBlock renders installed crawlers as source names plus declared
// manifest headlines. Sources with no headlines render as names only.
func sourcesBlock(sources []Source) string {
	if len(sources) == 0 {
		return "Sources:\n  No crawlers are installed yet."
	}
	rows := make([][2]string, 0, len(sources))
	for _, source := range sources {
		rows = append(rows, [2]string{sourceBlockName(source), sourceHeadlineText(source)})
	}
	lines := append([]string{"Sources:"}, alignRows(rows, 5)...)
	return strings.Join(lines, "\n")
}

const headlineSeparator = " · "

func sourceHeadlineText(source Source) string {
	return strings.Join(source.Headlines, headlineSeparator)
}

// startHereBlock renders the worked first steps. The source namespace example
// uses Telegram when installed, because it has several source-specific verbs.
func startHereBlock(sources []Source) string {
	rows := [][2]string{
		{"trawl status", "every source, and how fresh"},
		{`trawl search "boat trip"`, "all sources at once, newest first"},
		{"trawl chats --with anna", "conversations across every messaging source"},
	}
	if token := startHereSourceToken(sources); token != "" {
		rows = append(rows, [2]string{"trawl " + token, "everything " + token + " can do"})
	}
	lines := append([]string{"Start here:"}, alignRows(rows, 5)...)
	return strings.Join(lines, "\n")
}

func startHereSourceToken(sources []Source) string {
	for _, source := range sources {
		if source.Surface == "telegram" || source.ID == "telegram" {
			return "telegram"
		}
	}
	if len(sources) == 0 {
		return ""
	}
	return sourceCommandToken(sources[0])
}

// sourceBlockName is the left column of a Sources row: the canonical surface
// name, with any declared human aliases in parentheses (e.g. "x (twitter)").
func sourceBlockName(source Source) string {
	name := sourceHumanName(source)
	if len(source.Aliases) > 0 {
		name += " (" + strings.Join(source.Aliases, ", ") + ")"
	}
	return name
}

// alignRows lays out "  left  right" rows with every non-empty right column
// starting at the same offset. Empty right cells render as the left value only.
func alignRows(rows [][2]string, gap int) []string {
	width := 0
	for _, row := range rows {
		if n := len(row[0]); n > width {
			width = n
		}
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row[1] == "" {
			out = append(out, "  "+row[0])
			continue
		}
		pad := strings.Repeat(" ", width-len(row[0])+gap)
		out = append(out, "  "+row[0]+pad+row[1])
	}
	return out
}
