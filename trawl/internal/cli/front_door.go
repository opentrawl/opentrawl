package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const trawlOrientation = `Search your own life. Every installed trawler archives one part of your life
(an app), and trawl searches all of them at once.`

const statusCommandHelpDescription = "Show archive contents, update times and failures"

func writeFrontDoor(w io.Writer) error {
	sources := discoverInstalledTrawlers(context.Background())
	sections := []string{
		trawlOrientation,
		trawlersBlock(sources),
		startHereBlock(render.TrawlInvocationDisplay(w)),
	}
	_, err := fmt.Fprintln(w, strings.Join(sections, "\n\n"))
	return err
}

func trawlersBlock(sources []InstalledTrawler) string {
	if len(sources) == 0 {
		return "Trawlers:\n  No trawlers are installed yet."
	}
	rows := make([][2]string, 0, len(sources))
	for _, source := range sources {
		rows = append(rows, [2]string{trawlerHumanName(source), trawlerCommandNamesShownInBareTrawlOverviewText(source)})
	}
	lines := append([]string{"Trawlers:"}, alignRows(rows, 5)...)
	return strings.Join(lines, "\n")
}

const trawlerCommandNameSeparator = " · "

func trawlerCommandNamesShownInBareTrawlOverviewText(trawler InstalledTrawler) string {
	return strings.Join(trawler.TrawlerCommandNamesShownInBareTrawlOverview, trawlerCommandNameSeparator)
}

func startHereBlock(trawlInvocationDisplay string) string {
	rows := [][2]string{
		{trawlInvocationDisplay + ` search "boat trip"`, "Find anything in your archive"},
		{trawlInvocationDisplay + " open LINK", "Open a result"},
		{trawlInvocationDisplay + " sync", "Update every trawler"},
		{trawlInvocationDisplay + " status", statusCommandHelpDescription},
		{trawlInvocationDisplay + " --help", "See every command"},
	}
	lines := append([]string{"Start here:"}, alignRows(rows, 4)...)
	return strings.Join(lines, "\n")
}

func trawlerBlockName(source InstalledTrawler) string {
	name := trawlerHumanName(source)
	if len(source.RegisteredTrawlerAliases) > 0 {
		name += " (" + strings.Join(source.RegisteredTrawlerAliases, ", ") + ")"
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
