package harness

import (
	"fmt"
	"io"
)

func WriteTable(w io.Writer, report Report) error {
	widths := tableWidths(report)
	if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", widths.name, "Check", widths.status, "Result", widths.detail, "Detail", "Remedy"); err != nil {
		return err
	}
	for _, result := range report {
		if _, err := fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", widths.name, result.Name, widths.status, result.Status, widths.detail, result.Detail, result.Remedy); err != nil {
			return err
		}
	}
	return nil
}

type tableWidth struct {
	name   int
	status int
	detail int
}

func tableWidths(report Report) tableWidth {
	widths := tableWidth{name: len("Check"), status: len("Result"), detail: len("Detail")}
	for _, result := range report {
		widths.name = max(widths.name, len(result.Name))
		widths.status = max(widths.status, len(result.Status))
		widths.detail = max(widths.detail, len(result.Detail))
	}
	return widths
}
