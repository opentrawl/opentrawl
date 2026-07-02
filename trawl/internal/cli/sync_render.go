package cli

import (
	"fmt"
	"github.com/mattn/go-runewidth"
	"io"
	"strings"
)

func renderSyncLine(w io.Writer, result SyncResult, sourceWidth, stateWidth int) error {
	line := padCell(result.Source, sourceWidth) + "  " +
		padCell(result.State, stateWidth) + "  " +
		result.Message
	_, err := fmt.Fprintln(w, strings.TrimRight(line, " "))
	return err
}

func syncSourceWidth(sources []Source) int {
	width := 0
	for _, source := range sources {
		if sourceWidth := runewidth.StringWidth(source.ID); sourceWidth > width {
			width = sourceWidth
		}
	}
	return width
}
