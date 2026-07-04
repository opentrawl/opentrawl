package cli

import (
	"fmt"
	"io"
	"strings"
)

// Sync streams one line per source as it finishes, so the column
// widths are fixed up front instead of fitted after the fact.
func renderSyncLine(w io.Writer, result SyncResult, sourceWidth, stateWidth int) error {
	line := fmt.Sprintf("%-*s  %-*s  %s", sourceWidth, result.Source, stateWidth, result.State, result.Message)
	_, err := fmt.Fprintln(w, strings.TrimRight(line, " "))
	return err
}

func syncSourceWidth(sources []Source) int {
	width := 0
	for _, source := range sources {
		if len(source.ID) > width {
			width = len(source.ID)
		}
	}
	return width
}
