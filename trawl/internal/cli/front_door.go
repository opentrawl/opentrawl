package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// frontDoorIntro is the two-line tagline bare `trawl` opens with. Bare
// trawl is the highest-traffic surface the tool has, so it stays short: a
// tagline, the live Sources block, three worked first steps, and a pointer
// to --help. The fuller generated page lives behind `trawl --help`.
const frontDoorIntro = `Search your own life. Every installed crawler archives one source, and
trawl searches all of them at once.`

// frontDoorAgents is the bare front door's closing two lines: the one thing
// an agent needs (--json) and where the full surface lives.
const frontDoorAgents = `Agents: add --json to any command for structured output.
Every flag and shared verb: trawl --help`

// helpAgentsBlock is the agent-facing appendix `trawl --help` carries: the
// ref grammar, the --json contract, and one runnable search-to-open
// transcript. imessage:msg/8842 is the same worked ref trawl's own
// open-error remedy names, so the grammar reads identically everywhere.
const helpAgentsBlock = `Agents:
  Refs are source:kind/id, for example imessage:msg/8842.
  Add --json to any command for structured, machine-readable output.
  Search, then open a hit by the ref it carries:
    trawl search "boat trip" --json
    trawl open imessage:msg/8842`

// writeFrontDoor renders bare `trawl`: the tagline, the live Sources block,
// three worked first steps, and the --json / --help pointers. The Sources
// block and the first-step source come from the live registry, so the door
// stays truthful as crawlers come and go.
func writeFrontDoor(w io.Writer) error {
	sources := discoverCrawlers(context.Background())
	sections := []string{
		frontDoorIntro,
		sourcesBlock(sources),
		startHereBlock(sources),
		frontDoorAgents,
	}
	_, err := fmt.Fprintln(w, strings.Join(sections, "\n\n"))
	return err
}

// sourcesBlock renders the installed crawlers as an aligned two-column list:
// the surface name a person types (with any declared alias in parentheses)
// on the left, its one-line description on the right. Both the front door
// and `trawl --help` render this, so a source is always findable by grepping
// the name a person would actually type.
func sourcesBlock(sources []Source) string {
	if len(sources) == 0 {
		return "Sources:\n  No crawlers are installed yet."
	}
	rows := make([][2]string, 0, len(sources))
	for _, source := range sources {
		rows = append(rows, [2]string{sourceBlockName(source), source.Description})
	}
	lines := append([]string{"Sources:"}, alignRows(rows, 4)...)
	return strings.Join(lines, "\n")
}

// startHereBlock renders the three worked first steps. The third step names
// the first installed source, so a cold reader sees a real command to type,
// not a `<source>` placeholder.
func startHereBlock(sources []Source) string {
	rows := [][2]string{
		{"trawl status", "your sources, and how fresh each one is"},
		{`trawl search "boat trip"`, "search every source, newest first"},
	}
	if len(sources) > 0 {
		rows = append(rows, [2]string{"trawl " + sourceCommandToken(sources[0]), "one source's own commands"})
	}
	lines := append([]string{"Start here:"}, alignRows(rows, 5)...)
	return strings.Join(lines, "\n")
}

// sourceBlockName is the left column of a Sources row: the canonical surface
// name, with any declared human aliases in parentheses (e.g. "x (twitter)").
func sourceBlockName(source Source) string {
	name := firstNonEmpty(source.Surface, source.ID)
	if len(source.Aliases) > 0 {
		name += " (" + strings.Join(source.Aliases, ", ") + ")"
	}
	return name
}

// alignRows lays out "  left  right" rows with every right column starting at
// the same offset: two-space indent, the left values padded to the longest
// plus gap trailing spaces. gap is the space after the longest left value.
func alignRows(rows [][2]string, gap int) []string {
	width := 0
	for _, row := range rows {
		if n := len(row[0]); n > width {
			width = n
		}
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		pad := strings.Repeat(" ", width-len(row[0])+gap)
		out = append(out, "  "+row[0]+pad+row[1])
	}
	return out
}
