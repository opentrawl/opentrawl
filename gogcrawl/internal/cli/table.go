package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const (
	minTableWidth = 72
	tableGap      = "  "
)

type tableColumn struct {
	header string
	width  int
	wrap   bool
}

func renderWhoTable(w io.Writer, candidates []archive.WhoCandidate) error {
	width := normalizeTableWidth(render.OutputWidth(w))
	columns := whoTableColumns(width, candidates)
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.Who,
			strings.Join(candidate.Identifiers, ", "),
			candidate.LastSeen,
			fmt.Sprint(candidate.Messages),
		})
	}
	return renderTable(w, columns, rows)
}

func whoTableColumns(width int, candidates []archive.WhoCandidate) []tableColumn {
	lastSeenWidth := 20
	messagesWidth := 8
	whoWidth := 12
	for _, candidate := range candidates {
		if length := render.DisplayWidth(candidate.Who) + 2; length > whoWidth {
			whoWidth = length
		}
	}
	if whoWidth > 28 {
		whoWidth = 28
	}
	gaps := 3 * len(tableGap)
	identifiersWidth := width - whoWidth - lastSeenWidth - messagesWidth - gaps
	if identifiersWidth < 18 {
		identifiersWidth = 18
		whoWidth = width - identifiersWidth - lastSeenWidth - messagesWidth - gaps
		if whoWidth < 12 {
			whoWidth = 12
		}
	}
	return []tableColumn{
		{header: "who", width: whoWidth, wrap: true},
		{header: "identifiers", width: identifiersWidth, wrap: true},
		{header: "last_seen", width: lastSeenWidth},
		{header: "messages", width: messagesWidth},
	}
}

func normalizeTableWidth(width int) int {
	if width < minTableWidth {
		return minTableWidth
	}
	return width
}

func renderTable(w io.Writer, columns []tableColumn, rows [][]string) error {
	header := make([]string, 0, len(columns))
	for _, column := range columns {
		header = append(header, column.header)
	}
	if err := renderTableRow(w, columns, header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := renderTableRow(w, columns, row); err != nil {
			return err
		}
	}
	return nil
}

func renderTableRow(w io.Writer, columns []tableColumn, row []string) error {
	cells := make([][]string, len(columns))
	height := 1
	for i, column := range columns {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		if column.wrap {
			value = normalizeTableCell(value)
			if value == "" {
				cells[i] = []string{"-"}
			} else {
				cells[i] = render.Wrap(value, column.width)
			}
		} else {
			value = compactTableCell(value)
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
			} else if _, err := io.WriteString(w, padTableCell(value, column.width)); err != nil {
				return err
			}
			if i < len(columns)-1 {
				if _, err := io.WriteString(w, tableGap); err != nil {
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

func padTableCell(value string, width int) string {
	if render.DisplayWidth(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-render.DisplayWidth(value))
}

func normalizeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func compactTableCell(value string) string {
	return strings.Join(strings.Fields(normalizeTableCell(value)), " ")
}
