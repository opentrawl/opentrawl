package render

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

const (
	renderTableGap                  = "  "
	minPlainColumnWidth             = 3
	minimumStandardTableOutputWidth = 80
	maximumReadableTableOutputWidth = 240
)

type TableColumn struct {
	Header                                 string
	Width                                  int
	MinimumWidth                           int
	Wrap                                   bool
	KeepWholeTokensWhenTerminalWidthAllows bool
	NeverTruncateCellValues                bool
	CellValueIsTrawlCommandAction          bool
	AlignRight                             bool
	MaximumWrappedLines                    int
}

type renderColumn struct {
	Header                                            string
	Width                                             int
	MinimumWidth                                      int
	Wrap                                              bool
	KeepWholeTokensWhenTerminalWidthAllows            bool
	NeverTruncateCellValues                           bool
	CellValueIsTrawlCommandAction                     bool
	HideBeforeTruncatingOtherColumnsBelowMinimumWidth bool
	AlignRight                                        bool
	Clamp                                             int
	HiddenFromRenderedTable                           bool
}

func WriteTable(w io.Writer, columns []TableColumn, rows [][]string) error {
	if len(columns) == 0 || len(rows) == 0 {
		return nil
	}
	outputWidth := readableTableOutputWidth(w)
	renderColumns := tableRenderColumns(columns, rows, outputWidth)
	if tableNeedsFieldValueRows(renderColumns, outputWidth) {
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
	return min(OutputWidth(writer), maximumReadableTableOutputWidth)
}

func tableNeedsFieldValueRows(columns []renderColumn, outputWidth int) bool {
	renderedColumnCount := 0
	for _, column := range columns {
		if column.HiddenFromRenderedTable {
			return true
		}
		renderedColumnCount++
		if column.Width < DisplayWidth(column.Header) {
			return true
		}
	}
	return outputWidth < minimumStandardTableOutputWidth && renderedColumnCount >= 3
}

func writeFieldValueRows(w io.Writer, columns []renderColumn, rows [][]string, outputWidth int) error {
	for rowIndex, row := range rows {
		if rowIndex > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		for columnIndex, column := range columns {
			value := strings.TrimSpace(tableRowValue(row, columnIndex))
			if value == "" {
				continue
			}
			fieldLabel := DisplayLabel(column.Header)
			if column.CellValueIsTrawlCommandAction {
				if err := writeTrawlCommandHintAtOutputWidth(w, fieldLabel+": "+value, outputWidth); err != nil {
					return err
				}
				continue
			}
			fieldWidth := DisplayWidth(fieldLabel + ": " + value)
			if column.NeverTruncateCellValues &&
				fieldWidth > outputWidth &&
				DisplayWidth(value) <= outputWidth {
				if _, err := fmt.Fprintf(w, "%s:\n%s\n", fieldLabel, value); err != nil {
					return err
				}
				continue
			}
			if err := writeWrappedFieldAtOutputWidth(w, column.Header, value, outputWidth); err != nil {
				return err
			}
		}
	}
	return nil
}

func tableRenderColumns(columns []TableColumn, rows [][]string, outputWidth int) []renderColumn {
	out := make([]renderColumn, len(columns))
	for i, column := range columns {
		header := strings.ToLower(strings.TrimSpace(column.Header))
		naturalWidth := naturalTableColumnWidth(header, column.Wrap, rows, i)
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
		minimumWidth := column.MinimumWidth
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
			CellValueIsTrawlCommandAction:          column.CellValueIsTrawlCommandAction,
			AlignRight:                             column.AlignRight,
			Clamp:                                  column.MaximumWrappedLines,
		}
	}
	fitRenderColumns(out, outputWidth)
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

func fitRenderColumns(columns []renderColumn, outputWidth int) {
	hideColumnsBeforeTruncatingOtherColumnsBelowMinimumWidth(columns, outputWidth)
	for len(columns) > 0 && renderColumnsWidth(columns) > outputWidth {
		column := widestShrinkableRenderColumn(columns)
		if column < 0 {
			column = widestTruncatableRenderColumnWiderThanOneCell(columns)
		}
		if column < 0 {
			return
		}
		columns[column].Width--
	}
}

func hideColumnsBeforeTruncatingOtherColumnsBelowMinimumWidth(columns []renderColumn, outputWidth int) {
	for renderColumnsMinimumWidth(columns) > outputWidth {
		columnToHide := -1
		for columnIndex := range columns {
			if columns[columnIndex].HiddenFromRenderedTable ||
				!columns[columnIndex].HideBeforeTruncatingOtherColumnsBelowMinimumWidth {
				continue
			}
			columnToHide = columnIndex
			break
		}
		if columnToHide < 0 {
			return
		}
		columns[columnToHide].HiddenFromRenderedTable = true
	}
}

func renderColumnsWidth(columns []renderColumn) int {
	width := 0
	renderedColumnCount := 0
	for _, column := range columns {
		if column.HiddenFromRenderedTable {
			continue
		}
		if renderedColumnCount > 0 {
			width += len(renderTableGap)
		}
		width += column.Width
		renderedColumnCount++
	}
	return width
}

func renderColumnsMinimumWidth(columns []renderColumn) int {
	width := 0
	renderedColumnCount := 0
	for _, column := range columns {
		if column.HiddenFromRenderedTable {
			continue
		}
		if renderedColumnCount > 0 {
			width += len(renderTableGap)
		}
		width += minRenderColumnWidth(column)
		renderedColumnCount++
	}
	return width
}

func widestShrinkableRenderColumn(columns []renderColumn) int {
	column := -1
	for i := range columns {
		if columns[i].HiddenFromRenderedTable {
			continue
		}
		if columns[i].Width <= minRenderColumnWidth(columns[i]) {
			continue
		}
		if column == -1 || columns[i].Width > columns[column].Width {
			column = i
		}
	}
	return column
}

func widestTruncatableRenderColumnWiderThanOneCell(columns []renderColumn) int {
	column := -1
	for i := range columns {
		if columns[i].HiddenFromRenderedTable {
			continue
		}
		if columns[i].NeverTruncateCellValues {
			continue
		}
		if columns[i].Width <= 1 {
			continue
		}
		if column == -1 || columns[i].Width > columns[column].Width {
			column = i
		}
	}
	return column
}

func minRenderColumnWidth(column renderColumn) int {
	if column.MinimumWidth > 0 {
		return column.MinimumWidth
	}
	if column.Wrap {
		return minPlainColumnWidth
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
		if column.HiddenFromRenderedTable {
			continue
		}
		cells[i] = renderCellLines(tableRowValue(row, i), column, header)
		if len(cells[i]) > height {
			height = len(cells[i])
		}
	}
	renderedColumnCount := 0
	for _, column := range columns {
		if !column.HiddenFromRenderedTable {
			renderedColumnCount++
		}
	}
	for lineNo := 0; lineNo < height; lineNo++ {
		var line strings.Builder
		renderedColumnIndex := 0
		for i, column := range columns {
			if column.HiddenFromRenderedTable {
				continue
			}
			value := ""
			if lineNo < len(cells[i]) {
				value = cells[i][lineNo]
			}
			last := renderedColumnIndex == renderedColumnCount-1
			line.WriteString(formatRenderCell(value, column, last))
			if !last {
				line.WriteString(renderTableGap)
			}
			renderedColumnIndex++
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(line.String(), " ")); err != nil {
			return err
		}
	}
	return nil
}

func renderCellLines(value string, column renderColumn, header bool) []string {
	if header {
		return []string{Truncate(compactTableCell(value), column.Width)}
	}
	value = HumanCell(column.Header, value)
	if strings.TrimSpace(value) == "" {
		return []string{""}
	}
	if column.NeverTruncateCellValues {
		return []string{compactTableCell(value)}
	}
	if column.Wrap {
		value = strings.TrimRight(normalizeTableCell(value), "\n")
		value = elideWideTokens(value, column.Width)
		lines := Wrap(value, column.Width)
		return clampLines(lines, column.Clamp, column.Width)
	}
	value = compactTableCell(value)
	value = Truncate(value, column.Width)
	return []string{value}
}

// elideWideTokens clips any whitespace-delimited token wider than the column
// to fit, so a wrapped table cell never hard-splits an unbreakable token — an
// email or a URL — into mid-word fragments across lines. A clipped token keeps
// its leading text and ends in the ellipsis marker: a reader sees one cut
// token, not scattered pieces. Spacing and line breaks are preserved.
func elideWideTokens(value string, width int) string {
	if width <= 0 {
		return value
	}
	var out strings.Builder
	var token strings.Builder
	flush := func() {
		if token.Len() == 0 {
			return
		}
		out.WriteString(Truncate(token.String(), width))
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
