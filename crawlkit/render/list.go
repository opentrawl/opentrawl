package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	listDateWidth    = 16
	listWhoWidth     = 24
	listWhereWidth   = 20
	listMinTextWidth = 16
)

type ListItem struct {
	Time  time.Time
	Who   string
	Where string
	Ref   string
	Text  string
}

type List struct {
	Heading   string
	Hints     []string
	Items     []ListItem
	ClampText int
	Empty     string
}

func ShortLocalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

func WriteList(w io.Writer, l List) error {
	if len(l.Items) == 0 {
		// Headings, counters and Open/More hints would all refer to rows
		// that do not exist; the empty sentence carries the whole answer.
		empty := strings.TrimSpace(l.Empty)
		if empty == "" {
			empty = "No results."
		}
		_, err := fmt.Fprintln(w, empty)
		return err
	}
	if err := writeListIntro(w, l.Heading, l.Hints); err != nil {
		return err
	}
	columns := listRenderColumns(l, OutputWidth(w))
	rows := listRows(l.Items, columns)
	if err := writeRenderHeader(w, columns); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeRenderRow(w, columns, row); err != nil {
			return err
		}
	}
	return nil
}

func writeListIntro(w io.Writer, heading string, hints []string) error {
	if _, err := fmt.Fprintln(w, strings.TrimSpace(heading)); err != nil {
		return err
	}
	for _, hint := range hints {
		hint = strings.TrimSpace(hint)
		if hint == "" {
			continue
		}
		if _, err := fmt.Fprintln(w, hint); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w)
	return err
}

func listRenderColumns(l List, outputWidth int) []renderColumn {
	columns := make([]renderColumn, 0, 5)
	if listHasDate(l.Items) {
		columns = append(columns, renderColumn{Header: "date", Width: listDateWidth})
	}
	if listHasValue(l.Items, func(item ListItem) string { return item.Who }) {
		columns = append(columns, renderColumn{
			Header: "who",
			Width:  boundedListColumnWidth("who", l.Items, listWhoWidth, func(item ListItem) string { return item.Who }),
		})
	}
	if listHasValue(l.Items, func(item ListItem) string { return item.Where }) {
		columns = append(columns, renderColumn{
			Header: "where",
			Width:  boundedListColumnWidth("where", l.Items, listWhereWidth, func(item ListItem) string { return item.Where }),
		})
	}
	if listHasValue(l.Items, func(item ListItem) string { return item.Ref }) {
		columns = append(columns, renderColumn{
			Header: "ref",
			Width:  naturalListColumnWidth("ref", l.Items, func(item ListItem) string { return item.Ref }),
		})
	}
	columns = append(columns, renderColumn{
		Header: "text",
		Width:  listMinTextWidth,
		Wrap:   true,
		Clamp:  l.ClampText,
	})
	fitListColumns(columns, outputWidth)
	return columns
}

func fitListColumns(columns []renderColumn, outputWidth int) {
	text := len(columns) - 1
	for text > 0 && listFixedBudget(columns)+listMinTextWidth > outputWidth {
		column := widestListShrinkColumn(columns[:text], true)
		if column < 0 {
			column = widestListShrinkColumn(columns[:text], false)
		}
		if column < 0 {
			break
		}
		columns[column].Width--
	}
	textWidth := outputWidth - listFixedBudget(columns)
	if textWidth < listMinTextWidth {
		textWidth = listMinTextWidth
	}
	columns[text].Width = textWidth
}

func listFixedBudget(columns []renderColumn) int {
	if len(columns) <= 1 {
		return 0
	}
	width := 0
	for _, column := range columns[:len(columns)-1] {
		width += column.Width
	}
	width += len(renderTableGap) * (len(columns) - 1)
	return width
}

func widestListShrinkColumn(columns []renderColumn, preferredOnly bool) int {
	column := -1
	for i := range columns {
		preferred := columns[i].Header == "who" || columns[i].Header == "where"
		if preferredOnly && !preferred {
			continue
		}
		if columns[i].Width <= minPlainColumnWidth {
			continue
		}
		if column == -1 || columns[i].Width > columns[column].Width {
			column = i
		}
	}
	return column
}

func listRows(items []ListItem, columns []renderColumn) [][]string {
	rows := make([][]string, 0, len(items))
	for _, item := range items {
		row := make([]string, 0, len(columns))
		for _, column := range columns {
			switch column.Header {
			case "date":
				row = append(row, ShortLocalTime(item.Time))
			case "who":
				row = append(row, item.Who)
			case "where":
				row = append(row, item.Where)
			case "ref":
				row = append(row, item.Ref)
			case "text":
				row = append(row, collapseBlankLines(item.Text))
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// collapseBlankLines keeps paragraph text scannable inside a table cell: a
// blank line mid-cell reads as a row break, so runs of newlines collapse to
// one. Detail views (open) keep the original formatting.
func collapseBlankLines(value string) string {
	lines := strings.Split(value, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

func listHasDate(items []ListItem) bool {
	for _, item := range items {
		if !item.Time.IsZero() {
			return true
		}
	}
	return false
}

func listHasValue(items []ListItem, value func(ListItem) string) bool {
	for _, item := range items {
		if strings.TrimSpace(value(item)) != "" {
			return true
		}
	}
	return false
}

func boundedListColumnWidth(header string, items []ListItem, limit int, value func(ListItem) string) int {
	width := DisplayWidth(header)
	for _, item := range items {
		cell := Truncate(value(item), limit)
		if cellWidth := DisplayWidth(cell); cellWidth > width {
			width = cellWidth
		}
	}
	if width > limit {
		return limit
	}
	return width
}

func naturalListColumnWidth(header string, items []ListItem, value func(ListItem) string) int {
	width := DisplayWidth(header)
	for _, item := range items {
		cell := compactTableCell(value(item))
		if cellWidth := DisplayWidth(cell); cellWidth > width {
			width = cellWidth
		}
	}
	return width
}
