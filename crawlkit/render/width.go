package render

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

const defaultOutputWidth = 100

// OutputWidth returns the human-output line budget for the writer.
func OutputWidth(w io.Writer) int {
	if file, ok := w.(*os.File); ok {
		size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
		if err == nil && size != nil && size.Col > 0 {
			return int(size.Col)
		}
	}
	if width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && width > 0 {
		return width
	}
	return defaultOutputWidth
}

func DisplayWidth(value string) int {
	return runewidth.StringWidth(expandTabs(value))
}

func Truncate(value string, width int) string {
	value = strings.TrimSpace(value)
	if width <= 0 {
		return ""
	}
	if DisplayWidth(value) <= width {
		return value
	}
	ellipsis := "…"
	ellipsisWidth := DisplayWidth(ellipsis)
	if width <= ellipsisWidth {
		return ellipsis
	}
	limit := width - ellipsisWidth
	var out strings.Builder
	cellWidth := 0
	for _, r := range value {
		runeWidth := DisplayWidth(string(r))
		if cellWidth+runeWidth > limit {
			break
		}
		out.WriteRune(r)
		cellWidth += runeWidth
	}
	return strings.TrimRightFunc(out.String(), unicode.IsSpace) + ellipsis
}

func Wrap(value string, width int) []string {
	value = normalizeText(value)
	if width <= 0 {
		return []string{value}
	}
	lines := strings.Split(value, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, wrapLine(line, width)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func WrapWithIndent(prefix, value string, width int, continuationIndent string) []string {
	if continuationIndent == "" {
		continuationIndent = strings.Repeat(" ", DisplayWidth(prefix))
	}
	value = normalizeText(value)
	if strings.TrimSpace(value) == "" {
		return []string{strings.TrimRight(prefix, " ")}
	}
	var out []string
	nextPrefix := prefix
	for _, logicalLine := range strings.Split(value, "\n") {
		if len(out) > 0 && logicalLine == "" {
			out = append(out, strings.TrimRight(nextPrefix, " "))
			nextPrefix = continuationIndent
			continue
		}
		lineWidth := width - DisplayWidth(nextPrefix)
		if lineWidth < 1 {
			lineWidth = 1
		}
		for _, line := range wrapLine(logicalLine, lineWidth) {
			out = append(out, nextPrefix+line)
			nextPrefix = continuationIndent
		}
	}
	return out
}

func WriteWrappedField(w io.Writer, label, value string) error {
	prefix := strings.TrimSpace(label) + ": "
	for _, line := range WrapWithIndent(prefix, value, OutputWidth(w), "") {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func WriteSearchSummary(w io.Writer, query string, returned int, total int64) error {
	if strings.TrimSpace(query) == "" {
		_, err := fmt.Fprintf(w, "Search filters: showing %d of %d.\n", returned, total)
		return err
	}
	prefix := "Search \""
	suffix := fmt.Sprintf("\": showing %d of %d.", returned, total)
	queryWidth := OutputWidth(w) - DisplayWidth(prefix) - DisplayWidth(suffix)
	if queryWidth < 1 {
		queryWidth = 1
	}
	_, err := fmt.Fprintf(w, "%s%s%s\n", prefix, Truncate(query, queryWidth), suffix)
	return err
}

func normalizeText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return expandTabs(value)
}

func expandTabs(value string) string {
	return strings.ReplaceAll(value, "\t", "    ")
}

func wrapLine(line string, width int) []string {
	if line == "" || DisplayWidth(line) <= width {
		return []string{line}
	}
	var out []string
	for DisplayWidth(line) > width {
		partEnd, nextStart := splitLineAtWidth(line, width)
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

func splitLineAtWidth(line string, width int) (partEnd int, nextStart int) {
	cellWidth := 0
	lastSpaceStart := -1
	lastSpaceEnd := -1
	for index, r := range line {
		runeWidth := DisplayWidth(string(r))
		if unicode.IsSpace(r) {
			lastSpaceStart = index
			lastSpaceEnd = index + len(string(r))
		}
		if cellWidth+runeWidth > width {
			if lastSpaceStart > 0 {
				return lastSpaceStart, lastSpaceEnd
			}
			if index == 0 {
				end := index + len(string(r))
				return end, end
			}
			return index, index
		}
		cellWidth += runeWidth
	}
	return len(line), len(line)
}
