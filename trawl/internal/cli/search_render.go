package cli

import (
	"fmt"
	"io"
	"strings"
)

// The search table keeps every row on one line: who is capped, the
// snippet takes what remains of the width, and the ref — which exists
// to be copied into `trawl open` — sits on its own indented line only
// when the table would otherwise not fit.
const searchWhoLimit = 28

func renderSearchTable(w io.Writer, rows []SearchRow, more int) error {
	if len(rows) == 0 {
		if _, err := fmt.Fprintln(w, "No results."); err != nil {
			return err
		}
		return nil
	}
	tableRows := make([][]string, 0, len(rows))
	inlineRefs := true
	for _, row := range rows {
		tableRows = append(tableRows, []string{
			row.Source,
			searchDate(row),
			truncateCell(searchWho(row), searchWhoLimit),
			row.Ref,
			row.Snippet,
		})
	}
	// Try refs as a column first; drop to per-row ref lines when the
	// snippet would be squeezed below usefulness.
	if widths := columnWidths([]string{"SOURCE", "DATE", "WHO", "REF", "SNIPPET"}, tableRows); lastColumnBudget(widths) <= 24 {
		inlineRefs = false
		for i, row := range tableRows {
			tableRows[i] = []string{row[0], row[1], row[2], row[4]}
		}
	}
	header := []string{"SOURCE", "DATE", "WHO", "REF", "SNIPPET"}
	if !inlineRefs {
		header = []string{"SOURCE", "DATE", "WHO", "SNIPPET"}
	}
	refs := make([]string, 0, len(rows))
	for _, row := range rows {
		if inlineRefs {
			refs = append(refs, "")
		} else {
			refs = append(refs, "open: "+row.Ref)
		}
	}
	if err := writeSearchRows(w, header, tableRows, refs, inlineRefs); err != nil {
		return err
	}
	if more > 0 {
		_, err := fmt.Fprintf(w, "…and %d more; narrow the query or add --after, or use --json\n", more)
		return err
	}
	return nil
}

func writeSearchRows(w io.Writer, header []string, rows [][]string, refs []string, inlineRefs bool) error {
	widths := columnWidths(header, rows)
	free := lastColumnBudget(widths)
	if err := writeTableRow(w, header, widths, free); err != nil {
		return err
	}
	for i, row := range rows {
		if err := writeTableRow(w, row, widths, free); err != nil {
			return err
		}
		if !inlineRefs && refs[i] != "" {
			if _, err := fmt.Fprintf(w, "  %s\n", refs[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

func searchDate(row SearchRow) string {
	if row.timeOK {
		return row.parsedTime.Format("2006-01-02")
	}
	return row.Time
}

func searchWho(row SearchRow) string {
	who := strings.TrimSpace(row.Who)
	where := strings.TrimSpace(row.Where)
	// In a direct chat the chat is named after the person, so
	// "Katja → Katja" collapses to the person.
	if strings.EqualFold(who, where) {
		return who
	}
	if who != "" && where != "" {
		return who + " → " + where
	}
	return firstNonEmpty(who, where)
}
