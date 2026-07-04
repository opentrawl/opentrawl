package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
)

// The federated search list is List-shaped — heading, copy-pasteable
// hint lines, then a labelled table — but laid out here because the
// merged view is the one surface that needs a source column, which
// crawlkit's list component does not carry yet (noted on TRAWL-95).
// Rows stay on one line; when the terminal cannot fit them the layout
// sheds deliberately: first the ref column moves to a per-row `open:`
// line, then the where column yields to the text. The date is never
// squeezed and the text never drops below a readable minimum.
const (
	searchWhoLimit     = 24
	searchWhereLimit   = 20
	searchMinTextWidth = 24
	searchColumnGap    = "  "
)

type searchListContext struct {
	Query   string
	MoreCmd string
}

func renderSearchResults(w io.Writer, merged mergedSearchResult, list searchListContext) error {
	if len(merged.Rows) == 0 {
		return renderSearchEmpty(w, list.Query)
	}
	if _, err := fmt.Fprintln(w, searchHeading(list.Query, len(merged.Rows), merged.TotalMatches)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Open: trawl open REF"); err != nil {
		return err
	}
	if merged.More > 0 {
		if _, err := fmt.Fprintln(w, "More: "+list.MoreCmd); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	return writeSearchTable(w, merged.Rows)
}

type searchColumn struct {
	header string
	width  int
	cell   func(SearchRow) string
}

func writeSearchTable(w io.Writer, rows []SearchRow) error {
	width := render.OutputWidth(w)
	columns := []searchColumn{
		{header: "date", cell: searchDate},
		{header: "source", cell: func(row SearchRow) string { return row.surface }},
		{header: "who", cell: func(row SearchRow) string {
			return render.Truncate(normalizeSelf(row.Who), searchWhoLimit)
		}},
		{header: "where", cell: func(row SearchRow) string {
			return render.Truncate(normalizeSelf(row.Where), searchWhereLimit)
		}},
		{header: "ref", cell: searchDisplayRef},
	}
	// Columns whose values are all empty are omitted (rendering.md): a
	// strip of dashes tells a reader nothing.
	columns = searchColumnsWithValues(columns, rows)
	for i := range columns {
		columns[i].width = searchColumnWidth(columns[i], rows)
	}
	// Shed until the text column is readable: the ref moves to its own
	// per-row line first, then the where column yields entirely.
	inlineRefs := true
	if searchTextBudget(width, columns) < searchMinTextWidth {
		inlineRefs = false
		columns = dropSearchColumn(columns, "ref")
	}
	if searchTextBudget(width, columns) < searchMinTextWidth {
		columns = dropSearchColumn(columns, "where")
	}
	textWidth := searchTextBudget(width, columns)
	if textWidth < searchMinTextWidth {
		textWidth = searchMinTextWidth
	}

	if err := writeSearchLine(w, searchHeaderCells(columns), columns, "text", textWidth); err != nil {
		return err
	}
	for _, row := range rows {
		cells := make([]string, 0, len(columns))
		for _, column := range columns {
			cells = append(cells, column.cell(row))
		}
		if err := writeSearchLine(w, cells, columns, row.Snippet, textWidth); err != nil {
			return err
		}
		if !inlineRefs {
			if _, err := fmt.Fprintf(w, "  open: %s\n", searchDisplayRef(row)); err != nil {
				return err
			}
		}
	}
	return nil
}

func searchColumnsWithValues(columns []searchColumn, rows []SearchRow) []searchColumn {
	kept := make([]searchColumn, 0, len(columns))
	for _, column := range columns {
		empty := true
		for _, row := range rows {
			if strings.TrimSpace(column.cell(row)) != "" {
				empty = false
				break
			}
		}
		if !empty {
			kept = append(kept, column)
		}
	}
	return kept
}

func dropSearchColumn(columns []searchColumn, header string) []searchColumn {
	kept := make([]searchColumn, 0, len(columns))
	for _, column := range columns {
		if column.header != header {
			kept = append(kept, column)
		}
	}
	return kept
}

func searchColumnWidth(column searchColumn, rows []SearchRow) int {
	width := render.DisplayWidth(column.header)
	for _, row := range rows {
		if cellWidth := render.DisplayWidth(column.cell(row)); cellWidth > width {
			width = cellWidth
		}
	}
	return width
}

func searchTextBudget(width int, columns []searchColumn) int {
	used := 0
	for _, column := range columns {
		used += column.width + len(searchColumnGap)
	}
	return width - used
}

func searchHeaderCells(columns []searchColumn) []string {
	cells := make([]string, 0, len(columns))
	for _, column := range columns {
		cells = append(cells, column.header)
	}
	return cells
}

func writeSearchLine(w io.Writer, cells []string, columns []searchColumn, text string, textWidth int) error {
	var line strings.Builder
	for i, column := range columns {
		line.WriteString(padSearchCell(searchCellOrDash(cells[i]), column.width))
		line.WriteString(searchColumnGap)
	}
	line.WriteString(render.Truncate(compactSearchCell(text), textWidth))
	_, err := fmt.Fprintln(w, strings.TrimRight(line.String(), " "))
	return err
}

// searchCellOrDash keeps empty cells visible the way the shared table
// component does.
func searchCellOrDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func compactSearchCell(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func padSearchCell(value string, width int) string {
	if gap := width - render.DisplayWidth(value); gap > 0 {
		return value + strings.Repeat(" ", gap)
	}
	return value
}

func renderSearchEmpty(w io.Writer, query string) error {
	sentence := "No matches."
	if strings.TrimSpace(query) != "" {
		sentence = fmt.Sprintf("No matches for %q.", query)
	}
	_, err := fmt.Fprintln(w, sentence)
	return err
}

func searchHeading(query string, shown, total int) string {
	if strings.TrimSpace(query) == "" {
		return fmt.Sprintf("Search filters: showing %d of %d, newest first.", shown, total)
	}
	return fmt.Sprintf("Search %q: showing %d of %d, newest first.", query, shown, total)
}

// searchDisplayRef degrades per row: the short alias when this row's
// source can resolve one, its full ref otherwise. One source without
// short refs never drags the whole table down to machine refs.
func searchDisplayRef(row SearchRow) string {
	if row.sourceShortRefs && strings.TrimSpace(row.ShortRef) != "" {
		return row.ShortRef
	}
	return row.Ref
}

func searchDate(row SearchRow) string {
	if row.timeOK {
		return render.ShortLocalTime(row.parsedTime)
	}
	return row.Time
}

// normalizeSelf makes every crawler's self-name read the same way.
func normalizeSelf(name string) string {
	if strings.EqualFold(strings.TrimSpace(name), "me") {
		return "me"
	}
	return strings.TrimSpace(name)
}

// moreCommand is the copy-pasteable next page: the same search, run
// with a doubled row budget.
func (c *SearchCmd) moreCommand(query, sourceScope string, shown []SearchRow) string {
	parts := []string{"trawl", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteExampleArg(query))
	}
	if sourceScope != "" {
		parts = append(parts, "--source", sourceScope)
	}
	if who := strings.TrimSpace(c.Who); who != "" {
		parts = append(parts, "--who", quoteExampleArg(who))
	}
	if after := strings.TrimSpace(c.After); after != "" {
		parts = append(parts, "--after", after)
	}
	if before := strings.TrimSpace(c.Before); before != "" {
		parts = append(parts, "--before", before)
	}
	limit := c.Limit
	if len(shown) > limit {
		limit = len(shown)
	}
	parts = append(parts, "--limit", strconv.Itoa(limit*2))
	return strings.Join(parts, " ")
}
