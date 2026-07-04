package render

import (
	"fmt"
	"io"
	"strings"
)

const (
	renderTableGap      = "  "
	minPlainColumnWidth = 3
	emptyWrapCell       = "(empty)"
)

type TableColumn struct {
	Header     string
	Width      int
	Wrap       bool
	AlignRight bool
}

type renderColumn struct {
	Header     string
	Width      int
	Wrap       bool
	AlignRight bool
	Clamp      int
}

func WriteTable(w io.Writer, columns []TableColumn, rows [][]string) error {
	if len(columns) == 0 || len(rows) == 0 {
		return nil
	}
	renderColumns := tableRenderColumns(columns, rows, OutputWidth(w))
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

func tableRenderColumns(columns []TableColumn, rows [][]string, outputWidth int) []renderColumn {
	out := make([]renderColumn, len(columns))
	for i, column := range columns {
		header := strings.ToLower(strings.TrimSpace(column.Header))
		width := column.Width
		if width <= 0 {
			width = naturalTableColumnWidth(header, column.Wrap, rows, i)
		}
		if headerWidth := DisplayWidth(header); headerWidth > width {
			width = headerWidth
		}
		if width < 1 {
			width = 1
		}
		out[i] = renderColumn{
			Header:     header,
			Width:      width,
			Wrap:       column.Wrap,
			AlignRight: column.AlignRight,
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
			return []string{emptyWrapCell}
		}
		return strings.Split(value, "\n")
	}
	value = compactTableCell(value)
	if value == "" {
		return []string{"-"}
	}
	return []string{value}
}

func fitRenderColumns(columns []renderColumn, outputWidth int) {
	for len(columns) > 0 && renderColumnsWidth(columns) > outputWidth {
		column := widestShrinkableRenderColumn(columns)
		if column < 0 {
			return
		}
		columns[column].Width--
	}
}

func renderColumnsWidth(columns []renderColumn) int {
	width := 0
	for i, column := range columns {
		width += column.Width
		if i < len(columns)-1 {
			width += len(renderTableGap)
		}
	}
	return width
}

func widestShrinkableRenderColumn(columns []renderColumn) int {
	column := -1
	for i := range columns {
		if columns[i].Width <= minRenderColumnWidth(columns[i]) {
			continue
		}
		if column == -1 || columns[i].Width > columns[column].Width {
			column = i
		}
	}
	return column
}

func minRenderColumnWidth(column renderColumn) int {
	if column.Wrap {
		return DisplayWidth(emptyWrapCell)
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
	if column.Wrap {
		value = strings.TrimRight(normalizeTableCell(value), "\n")
		var lines []string
		if strings.TrimSpace(value) == "" {
			lines = []string{emptyWrapCell}
		} else {
			lines = Wrap(value, column.Width)
		}
		return clampLines(lines, column.Clamp, column.Width)
	}
	value = compactTableCell(value)
	if value == "" {
		value = "-"
	} else {
		value = Truncate(value, column.Width)
	}
	return []string{value}
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
	if width <= 0 {
		return ""
	}
	if DisplayWidth(value)+DisplayWidth("…") <= width {
		return value + "…"
	}
	return Truncate(value, width)
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
