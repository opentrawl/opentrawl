package render

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

const (
	renderTableGap      = "  "
	minPlainColumnWidth = 3
	// Below this output width no list fits as a table, so each row is
	// written as one block of labelled fields. At this width and wider every
	// list is a table: rows, never cards.
	narrowestOutputWidthThatHoldsAListTable = 60
	// The main content, such as a message or a note's title, is never
	// narrower than this unless all of it is shorter or the output is too
	// narrow for any other layout.
	primaryHumanContentColumnMinimumWidth = 16
	// When width is short, the main content keeps about this many times the
	// width of each other wrapped column, so it gets the spare width.
	primaryHumanContentColumnWidthShareComparedWithOtherColumns = 2
)

type TableColumn struct {
	Header                                 string
	Width                                  int
	MinimumWidth                           int
	Wrap                                   bool
	KeepWholeTokensWhenTerminalWidthAllows bool
	NeverTruncateCellValues                bool
	PreserveCellLineBreaks                 bool
	AlignRight                             bool
	MaximumWrappedLines                    int
}

type renderColumn struct {
	Header                                 string
	Width                                  int
	MinimumWidth                           int
	Wrap                                   bool
	KeepWholeTokensWhenTerminalWidthAllows bool
	NeverTruncateCellValues                bool
	PreserveCellLineBreaks                 bool
	AlignRight                             bool
	Clamp                                  int
	// An opened record's own text keeps every web address whole, even past
	// the column, so the address still works when copied.
	KeepWebAddressesWhole bool
}

func writeHumanRecordRowsWithPrimaryContentColumn(
	w io.Writer,
	columns []TableColumn,
	rows [][]string,
	primaryHumanContentColumnIndex int,
) error {
	if primaryHumanContentColumnIndex < 0 || primaryHumanContentColumnIndex >= len(columns) {
		return fmt.Errorf(
			"primary human content column index %d is outside %d table columns",
			primaryHumanContentColumnIndex,
			len(columns),
		)
	}
	return writeTable(w, columns, rows, primaryHumanContentColumnIndex)
}

func writeTable(
	w io.Writer,
	columns []TableColumn,
	rows [][]string,
	primaryHumanContentColumnIndex int,
) error {
	if len(columns) == 0 || len(rows) == 0 {
		return nil
	}
	outputWidth := readableTableOutputWidth(w)
	renderColumns := tableRenderColumnsWithPrimaryHumanContentColumn(
		columns,
		rows,
		outputWidth,
		primaryHumanContentColumnIndex,
	)
	if !listRowsFitAsTable(renderColumns, outputWidth) {
		return writeFieldValueRows(w, renderColumns, rows, outputWidth)
	}
	if err := writeRenderHeader(w, renderColumns); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeRenderRow(w, renderColumns, row); err != nil {
			return err
		}
	}
	return nil
}

func readableTableOutputWidth(writer io.Writer) int {
	return OutputWidth(writer)
}

// listRowsFitAsTable is false only when the output is narrower than
// narrowestOutputWidthThatHoldsAListTable, or so narrow that even columns cut
// to their least width overflow it. Such a list is written as one block of
// labelled fields per row instead, so no column that holds data is dropped
// and no time is broken across lines.
func listRowsFitAsTable(columns []renderColumn, outputWidth int) bool {
	return outputWidth >= narrowestOutputWidthThatHoldsAListTable &&
		renderColumnsLeastWidth(columns) <= outputWidth
}

func writeFieldValueRows(w io.Writer, columns []renderColumn, rows [][]string, outputWidth int) error {
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			if err := writeLine(w, ""); err != nil {
				return err
			}
		}
		for columnIndex, column := range columns {
			value := strings.TrimSpace(tableRowValue(row, columnIndex))
			if value == "" {
				continue
			}
			fieldLabel := displayLabel(column.Header)
			if fieldLabel == "" {
				if err := writeLine(w, value); err != nil {
					return err
				}
				continue
			}
			fieldWidth := DisplayWidth(fieldLabel + ": " + value)
			if column.NeverTruncateCellValues && fieldWidth > outputWidth {
				if err := writeLines(w, []string{fieldLabel + ":", value}); err != nil {
					return err
				}
				continue
			}
			fieldLines := WrapWithIndent(fieldLabel+": ", value, outputWidth, "")
			if err := writeLines(w, clampLines(fieldLines, column.Clamp, outputWidth)); err != nil {
				return err
			}
		}
	}
	return nil
}

func tableRenderColumns(columns []TableColumn, rows [][]string, outputWidth int) []renderColumn {
	return tableRenderColumnsWithPrimaryHumanContentColumn(columns, rows, outputWidth, -1)
}

func tableRenderColumnsWithPrimaryHumanContentColumn(
	columns []TableColumn,
	rows [][]string,
	outputWidth int,
	primaryHumanContentColumnIndex int,
) []renderColumn {
	out := make([]renderColumn, len(columns))
	for i, column := range columns {
		header := strings.ToLower(strings.TrimSpace(column.Header))
		naturalWidth := naturalTableColumnWidth(header, column.Wrap || column.PreserveCellLineBreaks, rows, i)
		width := column.Width
		if width <= 0 ||
			(column.KeepWholeTokensWhenTerminalWidthAllows || column.NeverTruncateCellValues) &&
				naturalWidth > width {
			width = naturalWidth
		}
		if headerWidth := DisplayWidth(header); headerWidth > width {
			width = headerWidth
		}
		if width < 1 {
			width = 1
		}
		minimumWidth := max(column.MinimumWidth, DisplayWidth(header))
		if i == primaryHumanContentColumnIndex {
			minimumWidth = max(minimumWidth, min(primaryHumanContentColumnMinimumWidth, naturalWidth))
			// Allocate the terminal budget before narrowing. Starting from the
			// text's natural width leaves short matches squeezed beside long
			// addresses, even though the match is the main content.
			width = max(width, outputWidth)
		}
		if (column.KeepWholeTokensWhenTerminalWidthAllows || column.NeverTruncateCellValues) &&
			naturalWidth > minimumWidth {
			minimumWidth = naturalWidth
		}
		out[i] = renderColumn{
			Header:                                 header,
			Width:                                  width,
			MinimumWidth:                           minimumWidth,
			Wrap:                                   column.Wrap,
			KeepWholeTokensWhenTerminalWidthAllows: column.KeepWholeTokensWhenTerminalWidthAllows,
			NeverTruncateCellValues:                column.NeverTruncateCellValues,
			PreserveCellLineBreaks:                 column.PreserveCellLineBreaks,
			AlignRight:                             column.AlignRight,
			Clamp:                                  column.MaximumWrappedLines,
		}
	}
	fitRenderColumnsWithPrimaryHumanContentColumn(out, outputWidth, primaryHumanContentColumnIndex)
	return out
}

func naturalTableColumnWidth(header string, wrap bool, rows [][]string, column int) int {
	width := DisplayWidth(header)
	for _, row := range rows {
		for _, line := range naturalTableCellLines(tableRowValue(row, column), wrap) {
			if lineWidth := DisplayWidth(line); lineWidth > width {
				width = lineWidth
			}
		}
	}
	return width
}

func naturalTableCellLines(value string, wrap bool) []string {
	if wrap {
		value = strings.TrimRight(normalizeTableCell(value), "\n")
		if strings.TrimSpace(value) == "" {
			return []string{""}
		}
		return strings.Split(value, "\n")
	}
	value = compactTableCell(value)
	return []string{value}
}

// fitRenderColumnsWithPrimaryHumanContentColumn narrows a table to the
// output. Times, links and commands keep their whole width. Wrapped columns
// give way first, the main content keeping about twice the width of each
// other column, down to each column's minimum. Only if that is still too wide
// do all columns that can be cut give way, down to their least width.
func fitRenderColumnsWithPrimaryHumanContentColumn(
	columns []renderColumn,
	outputWidth int,
	primaryHumanContentColumnIndex int,
) {
	narrowColumnsUntilTheyFit(columns, outputWidth, primaryHumanContentColumnIndex, func(column renderColumn) (int, bool) {
		return minRenderColumnWidth(column), column.Wrap && !column.NeverTruncateCellValues
	})
	narrowColumnsUntilTheyFit(columns, outputWidth, primaryHumanContentColumnIndex, func(column renderColumn) (int, bool) {
		return leastRenderColumnWidth(column), !column.NeverTruncateCellValues
	})
}

func narrowColumnsUntilTheyFit(
	columns []renderColumn,
	outputWidth int,
	primaryHumanContentColumnIndex int,
	narrowestWidthAndWhetherColumnCanNarrow func(renderColumn) (int, bool),
) {
	for renderColumnsWidth(columns) > outputWidth {
		columnToNarrow := -1
		columnToNarrowComparableWidth := 0
		for columnIndex, column := range columns {
			narrowestWidth, canNarrow := narrowestWidthAndWhetherColumnCanNarrow(column)
			if !canNarrow || column.Width <= narrowestWidth {
				continue
			}
			comparableWidth := column.Width * primaryHumanContentColumnWidthShareComparedWithOtherColumns
			if columnIndex == primaryHumanContentColumnIndex {
				comparableWidth = column.Width
			}
			if comparableWidth > columnToNarrowComparableWidth {
				columnToNarrow = columnIndex
				columnToNarrowComparableWidth = comparableWidth
			}
		}
		if columnToNarrow < 0 {
			return
		}
		columns[columnToNarrow].Width--
	}
}

func renderColumnsWidth(columns []renderColumn) int {
	width := 0
	renderedColumnCount := 0
	for _, column := range columns {
		if renderedColumnCount > 0 {
			width += len(renderTableGap)
		}
		width += column.Width
		renderedColumnCount++
	}
	return width
}

func renderColumnsLeastWidth(columns []renderColumn) int {
	width := 0
	for columnIndex, column := range columns {
		if columnIndex > 0 {
			width += len(renderTableGap)
		}
		width += leastRenderColumnWidth(column)
	}
	return width
}

// leastRenderColumnWidth is as narrow as a column can be cut when the output
// is too narrow for its minimum. A time, link or command is never cut.
func leastRenderColumnWidth(column renderColumn) int {
	if column.NeverTruncateCellValues {
		return column.Width
	}
	return min(column.Width, minPlainColumnWidth)
}

func minRenderColumnWidth(column renderColumn) int {
	if column.MinimumWidth > 0 {
		return column.MinimumWidth
	}
	return minPlainColumnWidth
}

func writeRenderHeader(w io.Writer, columns []renderColumn) error {
	row := make([]string, 0, len(columns))
	for _, column := range columns {
		row = append(row, column.Header)
	}
	return writeRenderRowWithMode(w, columns, row, true)
}

func writeRenderRow(w io.Writer, columns []renderColumn, row []string) error {
	return writeRenderRowWithMode(w, columns, row, false)
}

func writeRenderRowWithMode(w io.Writer, columns []renderColumn, row []string, header bool) error {
	cells := make([][]string, len(columns))
	height := 1
	for i, column := range columns {
		cells[i] = renderCellLines(tableRowValue(row, i), column, header)
		if len(cells[i]) > height {
			height = len(cells[i])
		}
	}
	for lineNo := 0; lineNo < height; lineNo++ {
		var line strings.Builder
		for i, column := range columns {
			value := ""
			if lineNo < len(cells[i]) {
				value = cells[i][lineNo]
			}
			last := i == len(columns)-1
			line.WriteString(formatRenderCell(value, column, last))
			if !last {
				line.WriteString(renderTableGap)
			}
		}
		if err := writeLine(w, strings.TrimRight(line.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}

func renderCellLines(value string, column renderColumn, header bool) []string {
	if header {
		return []string{Truncate(compactTableCell(value), column.Width)}
	}
	value = strings.TrimSpace(value)
	if strings.TrimSpace(value) == "" {
		return []string{""}
	}
	if column.NeverTruncateCellValues {
		if column.PreserveCellLineBreaks {
			return strings.Split(normalizeTableCell(value), "\n")
		}
		return []string{compactTableCell(value)}
	}
	if column.Wrap {
		value = strings.TrimRight(normalizeTableCell(value), "\n")
		value = elideWideTokens(value, column.Width, column.KeepWebAddressesWhole)
		lines := nonEmptyWrappedTableCellLines(Wrap(value, column.Width))
		return clampLines(lines, column.Clamp, column.Width)
	}
	value = compactTableCell(value)
	value = Truncate(value, column.Width)
	return []string{value}
}

func nonEmptyWrappedTableCellLines(lines []string) []string {
	nonEmptyLines := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines = append(nonEmptyLines, line)
		}
	}
	if len(nonEmptyLines) == 0 {
		return []string{""}
	}
	return nonEmptyLines
}

// elideWideTokens clips any whitespace-delimited token wider than the column
// to fit, so a wrapped table cell never hard-splits an unbreakable token — an
// email or a URL — into mid-word fragments across lines. A clipped token keeps
// its leading text and ends in the ellipsis marker: a reader sees one cut
// token, not scattered pieces. Spacing and line breaks are preserved.
func elideWideTokens(value string, width int, keepWebAddressesWhole bool) string {
	if width <= 0 {
		return value
	}
	var out strings.Builder
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		if keepWebAddressesWhole && strings.Contains(token.String(), "://") {
			out.WriteString(token.String())
		} else {
			out.WriteString(Truncate(token.String(), width))
		}
		token.Reset()
	}
	for _, r := range value {
		if unicode.IsSpace(r) {
			flush()
			out.WriteRune(r)
			continue
		}
		token.WriteRune(r)
	}
	flush()
	return out.String()
}

func clampLines(lines []string, limit int, width int) []string {
	if limit <= 0 || len(lines) <= limit {
		return lines
	}
	out := append([]string(nil), lines[:limit]...)
	out[len(out)-1] = withTrailingEllipsis(out[len(out)-1], width)
	if len(out) > 1 && strings.TrimSpace(out[len(out)-1]) == "…" {
		out[len(out)-2] = withTrailingEllipsis(out[len(out)-2], width)
		return out[:len(out)-1]
	}
	return out
}

func withTrailingEllipsis(value string, width int) string {
	marker := "…"
	markerWidth := DisplayWidth(marker)
	if width <= 0 {
		return ""
	}
	if width <= markerWidth {
		return marker
	}
	// The clamp hid at least one more line, so the last shown line always
	// ends in the marker — even when it exactly fills the column, where a
	// bare fit would read as a complete, un-truncated cell.
	clipped := clipToWidth(value, width-markerWidth)
	if strings.HasSuffix(clipped, marker) {
		// An elided wide-rune token can already carry the marker and
		// still fit under the budget (wide cells pack unevenly);
		// appending another would print "……".
		return clipped
	}
	return clipped + marker
}

func formatRenderCell(value string, column renderColumn, last bool) string {
	if column.AlignRight {
		return padLeftCell(value, column.Width)
	}
	if last {
		return value
	}
	return padRightCell(value, column.Width)
}

func padRightCell(value string, width int) string {
	if gap := width - DisplayWidth(value); gap > 0 {
		return value + strings.Repeat(" ", gap)
	}
	return value
}

func padLeftCell(value string, width int) string {
	if gap := width - DisplayWidth(value); gap > 0 {
		return strings.Repeat(" ", gap) + value
	}
	return value
}

func tableRowValue(row []string, column int) string {
	if column < len(row) {
		return row[column]
	}
	return ""
}

func compactTableCell(value string) string {
	return strings.Join(strings.Fields(normalizeTableCell(value)), " ")
}

func normalizeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}
