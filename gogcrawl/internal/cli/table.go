package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
)

const (
	defaultTableWidth = 100
	minTableWidth     = 72
	tableGap          = "  "
)

type tableColumn struct {
	header string
	width  int
	wrap   bool
}

func renderWhoTable(w io.Writer, candidates []archive.WhoCandidate) error {
	width := tableOutputWidth()
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
		if length := textWidth(candidate.Who) + 2; length > whoWidth {
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

func tableOutputWidth() int {
	raw := strings.TrimSpace(os.Getenv("COLUMNS"))
	if raw == "" {
		return defaultTableWidth
	}
	width, err := strconv.Atoi(raw)
	if err != nil || width <= 0 {
		return defaultTableWidth
	}
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
			cells[i] = wrapTableCell(value, column.width)
		} else {
			cells[i] = []string{truncateTableCell(value, column.width)}
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

func wrapTableCell(value string, width int) []string {
	value = normalizeTableCell(value)
	if value == "" {
		return []string{"-"}
	}
	var out []string
	for _, line := range strings.Split(value, "\n") {
		out = append(out, wrapTableLine(line, width)...)
	}
	return out
}

func wrapTableLine(line string, width int) []string {
	if line == "" || width <= 0 {
		return []string{line}
	}
	var out []string
	for textWidth(line) > width {
		partEnd, nextStart := splitTableLine(line, width)
		part := strings.TrimRightFunc(line[:partEnd], unicode.IsSpace)
		if part == "" {
			part = line[:nextStart]
		}
		out = append(out, part)
		line = strings.TrimLeftFunc(line[nextStart:], unicode.IsSpace)
		if line == "" {
			return out
		}
	}
	return append(out, line)
}

func splitTableLine(line string, width int) (partEnd int, nextStart int) {
	cellWidth := 0
	lastSpaceStart := -1
	lastSpaceEnd := -1
	for index, r := range line {
		if unicode.IsSpace(r) {
			lastSpaceStart = index
			lastSpaceEnd = index + len(string(r))
		}
		if cellWidth+1 > width {
			if lastSpaceStart > 0 {
				return lastSpaceStart, lastSpaceEnd
			}
			if index == 0 {
				end := index + len(string(r))
				return end, end
			}
			return index, index
		}
		cellWidth++
	}
	return len(line), len(line)
}

func truncateTableCell(value string, width int) string {
	value = compactTableCell(value)
	if value == "" {
		return "-"
	}
	if width <= 0 || textWidth(value) <= width {
		return value
	}
	if width <= 3 {
		return strings.Repeat(".", width)
	}
	runes := []rune(value)
	if len(runes) > width-3 {
		runes = runes[:width-3]
	}
	return strings.TrimRightFunc(string(runes), unicode.IsSpace) + "..."
}

func padTableCell(value string, width int) string {
	if textWidth(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-textWidth(value))
}

func normalizeTableCell(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func compactTableCell(value string) string {
	return strings.Join(strings.Fields(normalizeTableCell(value)), " ")
}

func textWidth(value string) int {
	return len([]rune(value))
}
