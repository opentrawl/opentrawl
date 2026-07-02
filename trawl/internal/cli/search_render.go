package cli

import (
	"fmt"
	"io"
	"strings"
)

func renderSearchTable(w io.Writer, rows []SearchRow, more int) error {
	if len(rows) == 0 {
		if _, err := fmt.Fprintln(w, "No results."); err != nil {
			return err
		}
		return nil
	}
	tableRows := make([][4]string, 0, len(rows))
	for _, row := range rows {
		tableRows = append(tableRows, [4]string{
			row.Source,
			searchDate(row),
			searchWho(row),
			searchSnippet(row),
		})
	}
	if err := writeTable(w, [4]string{"SOURCE", "DATE", "WHO", "SNIPPET"}, tableRows, nil); err != nil {
		return err
	}
	if more > 0 {
		_, err := fmt.Fprintf(w, "…and %d more; narrow with --after or --source\n", more)
		return err
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
	if who != "" && where != "" {
		return who + " → " + where
	}
	return firstNonEmpty(who, where)
}

func searchSnippet(row SearchRow) string {
	if row.Ref == "" {
		return row.Snippet
	}
	if row.Snippet == "" {
		return row.Ref
	}
	return row.Snippet + "   " + row.Ref
}
