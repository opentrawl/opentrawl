package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const trawlOrientation = `Search your own life. Each trawler copies one app's history to a local archive. Trawl searches every archive at once.`

const statusCommandHelpDescription = "Show archive contents, update times and failures"

func writeFrontDoor(w io.Writer) error {
	trawlers := discoverInstalledTrawlers(context.Background())
	outputWidth := render.OutputWidth(w)
	sections := []string{
		wrapTextForOutputWidth(trawlOrientation, outputWidth),
		trawlersBlock(trawlers, outputWidth),
		startHereBlock(render.TrawlInvocationDisplay(w), outputWidth),
	}
	_, err := fmt.Fprintln(w, strings.Join(sections, "\n\n"))
	return err
}

func trawlersBlock(trawlers []InstalledTrawler, outputWidth int) string {
	if len(trawlers) == 0 {
		return "Trawlers:\n" + strings.Join(
			render.WrapWithIndent("  ", "No trawlers are installed yet.", outputWidth, "  "),
			"\n",
		)
	}
	rows := make([][2]string, 0, len(trawlers))
	for _, trawler := range trawlers {
		rows = append(rows, [2]string{trawlerHumanName(trawler), trawlerCommandNamesShownInBareTrawlOverviewText(trawler)})
	}
	lines := append([]string{"Trawlers:"}, formatRowsForOutputWidth(rows, 5, outputWidth)...)
	return strings.Join(lines, "\n")
}

const trawlerCommandNameSeparator = " · "

func trawlerCommandNamesShownInBareTrawlOverviewText(trawler InstalledTrawler) string {
	return strings.Join(trawlerCommandNamesShownInBareTrawlOverview(trawler), trawlerCommandNameSeparator)
}

func trawlerCommandNamesShownInBareTrawlOverview(trawler InstalledTrawler) []string {
	var commandNames []string
	for _, command := range trawler.RegisteredTrawlerManifest.GetRegisteredTrawlerCommandDeclarations() {
		if command == nil || !command.GetTrawlerCommandIsShownInBareTrawlOverview() {
			continue
		}
		if commandName := registeredTrawlerCommandName(command); commandName != "" {
			commandNames = append(commandNames, commandName)
		}
	}
	return commandNames
}

func startHereBlock(trawlInvocationDisplay string, outputWidth int) string {
	rows := [][2]string{
		{trawlInvocationDisplay + ` search "boat trip"`, "Find anything in your archive"},
		{trawlInvocationDisplay + " open LINK", "Open a result"},
		{trawlInvocationDisplay + " update", "Get new items from every app"},
		{trawlInvocationDisplay + " status", statusCommandHelpDescription},
		{trawlInvocationDisplay + " --help", "See every command"},
	}
	lines := append([]string{"Start here:"}, formatRowsForOutputWidth(rows, 4, outputWidth)...)
	return strings.Join(lines, "\n")
}

func wrapTextForOutputWidth(text string, outputWidth int) string {
	return strings.Join(render.Wrap(text, outputWidth), "\n")
}

func formatRowsForOutputWidth(rows [][2]string, gap, outputWidth int) []string {
	alignedRows := alignRows(rows, gap)
	allAlignedRowsFit := true
	for _, row := range alignedRows {
		if render.DisplayWidth(row) > outputWidth {
			allAlignedRowsFit = false
			break
		}
	}
	if allAlignedRowsFit {
		return alignedRows
	}
	formattedRows := make([]string, 0, len(rows)*2)
	for _, row := range rows {
		formattedRows = append(
			formattedRows,
			render.WrapWithIndent("  ", row[0], outputWidth, "  ")...,
		)
		if strings.TrimSpace(row[1]) == "" {
			continue
		}
		formattedRows = append(
			formattedRows,
			render.WrapWithIndent("    ", row[1], outputWidth, "    ")...,
		)
	}
	return formattedRows
}

// alignRows lays out "  left  right" rows with every non-empty right column
// starting at the same offset. Empty right cells render as the left value only.
func alignRows(rows [][2]string, gap int) []string {
	width := 0
	for _, row := range rows {
		if n := render.DisplayWidth(row[0]); n > width {
			width = n
		}
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if row[1] == "" {
			out = append(out, "  "+row[0])
			continue
		}
		pad := strings.Repeat(" ", width-render.DisplayWidth(row[0])+gap)
		out = append(out, "  "+row[0]+pad+row[1])
	}
	return out
}
