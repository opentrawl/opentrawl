package cli

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
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
		if sourceWidth := utf8.RuneCountInString(source.ID); sourceWidth > width {
			width = sourceWidth
		}
	}
	return width
}
