package cli

import (
	"io"
	"strings"

	"github.com/openclaw/crawlkit/render"
)

const (
	minTextTableWidth = 72
	textTableGap      = "  "
)

type textColumn struct {
	header string
	width  int
	wrap   bool
}

func normalizeTextTableWidth(width int) int {
	if width < minTextTableWidth {
		return minTextTableWidth
	}
	return width
}

func renderTextTable(w io.Writer, columns []textColumn, rows [][]string) error {
	header := make([]string, 0, len(columns))
	for _, column := range columns {
		header = append(header, column.header)
	}
	if err := renderTextRow(w, columns, header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := renderTextRow(w, columns, row); err != nil {
			return err
		}
	}
	return nil
}

func renderTextRow(w io.Writer, columns []textColumn, row []string) error {
	cells := make([][]string, len(columns))
	height := 1
	for i, column := range columns {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		if column.header == "" && strings.TrimSpace(value) == "" {
			cells[i] = []string{""}
		} else if column.wrap {
			value = strings.TrimRight(normalizeCellText(value), "\n")
			if strings.TrimSpace(value) == "" {
				cells[i] = []string{"(empty)"}
			} else {
				cells[i] = render.Wrap(value, column.width)
			}
		} else {
			value = compactCellText(value)
			if value == "" {
				value = "-"
			} else {
				value = render.Truncate(value, column.width)
			}
			cells[i] = []string{value}
		}
		if len(cells[i]) > height {
			height = len(cells[i])
		}
	}
	for line := 0; line < height; line++ {
		for i, column := range columns {
			value := ""
			if line < len(cells[i]) {
				value = cells[i][line]
			}
			if i == len(columns)-1 {
				if _, err := io.WriteString(w, value); err != nil {
					return err
				}
			} else if _, err := io.WriteString(w, padCell(value, column.width)); err != nil {
				return err
			}
			if i < len(columns)-1 {
				if _, err := io.WriteString(w, textTableGap); err != nil {
					return err
				}
			}
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return err
		}
	}
	return nil
}

func padCell(value string, width int) string {
	cellWidth := render.DisplayWidth(value)
	if cellWidth >= width {
		return value
	}
	return value + strings.Repeat(" ", width-cellWidth)
}

func compactCellText(value string) string {
	return strings.Join(strings.Fields(normalizeCellText(value)), " ")
}

func normalizeCellText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	return strings.ReplaceAll(value, "\r", "\n")
}

func tableRows(count int) [][]string {
	return make([][]string, 0, count)
}

func textColumnWidth(totalWidth int, fixedColumns ...int) int {
	fixed := 0
	for _, width := range fixedColumns {
		fixed += width
	}
	gaps := len(fixedColumns) * len(textTableGap)
	width := totalWidth - fixed - gaps
	if width < 16 {
		return 16
	}
	return width
}
