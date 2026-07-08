package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	listDateWidth     = 10 // 2006-01-02
	listDateTimeWidth = 16 // 2006-01-02 15:04
	listWhoWidth      = 24
	listWhereWidth    = 20
	listMinTextWidth  = 16
)

type ListItem struct {
	Time time.Time
	// DateOnly marks Time as a calendar date with no meaningful time of
	// day (an all-day event): the date column shows the date alone,
	// without zone conversion — a date is the same wall-clock day
	// everywhere.
	DateOnly bool
	Source   string
	Who      string
	Where    string
	Calendar string
	Ref      string
	Text     string
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
	columns, shedRefs := listRenderColumns(l, OutputWidth(w))
	rows := listRows(l.Items, columns)
	if err := writeRenderHeader(w, columns); err != nil {
		return err
	}
	for i, row := range rows {
		if err := writeRenderRow(w, columns, row); err != nil {
			return err
		}
		if !shedRefs {
			continue
		}
		if ref := strings.TrimSpace(l.Items[i].Ref); ref != "" {
			if _, err := fmt.Fprintf(w, "  open: %s\n", ref); err != nil {
				return err
			}
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

func listRenderColumns(l List, outputWidth int) ([]renderColumn, bool) {
	columns := make([]renderColumn, 0, 7)
	if listHasDate(l.Items) {
		columns = append(columns, renderColumn{Header: "date", Width: listDateColumnWidth(l.Items)})
	}
	if listHasValue(l.Items, func(item ListItem) string { return item.Source }) {
		columns = append(columns, renderColumn{
			Header: "source",
			Width:  naturalListColumnWidth("source", l.Items, func(item ListItem) string { return item.Source }),
		})
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
	if listHasValue(l.Items, func(item ListItem) string { return item.Calendar }) {
		columns = append(columns, renderColumn{
			Header: "calendar",
			Width:  boundedListColumnWidth("calendar", l.Items, listWhereWidth, func(item ListItem) string { return item.Calendar }),
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
	return fitListColumns(columns, outputWidth)
}

// fitListColumns makes room for a readable text column. The who, where and
// calendar columns shrink first; if that is not enough, the ref column sheds
// to a per-row "open:" line. Date, source and ref cells are never truncated:
// a clipped timestamp or ref is garbage.
func fitListColumns(columns []renderColumn, outputWidth int) ([]renderColumn, bool) {
	text := len(columns) - 1
	for text > 0 && listFixedBudget(columns)+listMinTextWidth > outputWidth {
		column := widestListShrinkColumn(columns[:text])
		if column < 0 {
			break
		}
		columns[column].Width--
	}
	shedRefs := false
	if listFixedBudget(columns)+listMinTextWidth > outputWidth {
		if trimmed := dropListColumn(columns, "ref"); len(trimmed) < len(columns) {
			columns = trimmed
			shedRefs = true
		}
	}
	textWidth := outputWidth - listFixedBudget(columns)
	if textWidth < listMinTextWidth {
		textWidth = listMinTextWidth
	}
	columns[len(columns)-1].Width = textWidth
	return columns, shedRefs
}

func dropListColumn(columns []renderColumn, header string) []renderColumn {
	kept := make([]renderColumn, 0, len(columns))
	for _, column := range columns {
		if column.Header != header {
			kept = append(kept, column)
		}
	}
	return kept
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

func widestListShrinkColumn(columns []renderColumn) int {
	column := -1
	for i := range columns {
		if columns[i].Header != "who" && columns[i].Header != "where" && columns[i].Header != "calendar" {
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
				row = append(row, listDate(item))
			case "source":
				row = append(row, item.Source)
			case "who":
				row = append(row, HumanIdentity(item.Who))
			case "where":
				row = append(row, HumanIdentity(item.Where))
			case "calendar":
				row = append(row, HumanIdentity(item.Calendar))
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

func listDate(item ListItem) string {
	if item.Time.IsZero() {
		return ""
	}
	if item.DateOnly {
		return item.Time.Format("2006-01-02")
	}
	return ShortLocalTime(item.Time)
}

// listDateColumnWidth sizes the date column for its rows: a list of
// all-day events never pays for a time-of-day column it does not show.
func listDateColumnWidth(items []ListItem) int {
	for _, item := range items {
		if !item.Time.IsZero() && !item.DateOnly {
			return listDateTimeWidth
		}
	}
	return listDateWidth
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
