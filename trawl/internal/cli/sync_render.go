package cli

import (
	"fmt"
	"io"
	"strings"
)

// Sync streams one line per source as it finishes, so the column
// widths are fixed up front instead of fitted after the fact.
func renderSyncLine(w io.Writer, result SyncResult, sourceWidth, stateWidth int) error {
	source := firstNonEmpty(result.displaySource, result.Source)
	line := fmt.Sprintf("%-*s  %-*s  %s", sourceWidth, source, stateWidth, result.State, result.Message)
	_, err := fmt.Fprintln(w, strings.TrimRight(line, " "))
	return err
}

func syncSourceWidth(sources []Source) int {
	width := 0
	for _, source := range sources {
		name := sourceHumanName(source)
		if len(name) > width {
			width = len(name)
		}
	}
	return width
}
