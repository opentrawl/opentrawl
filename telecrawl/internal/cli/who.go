package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
)

func (r *runtime) runWho(args []string) error {
	if len(args) == 0 {
		return usageErr(errors.New("who takes a name"))
	}
	query := normalizeCLIWords(strings.Join(args, " "))
	if query == "" {
		return usageErr(errors.New("who takes a name"))
	}
	return r.withReadOnlyStore(func(st *store.Store) error {
		candidates, err := st.ResolveWho(r.ctx, query)
		if err != nil {
			return err
		}
		return r.print(newWhoEnvelope(query, candidates))
	})
}

func (r *runtime) ambiguousWhoError(query, who string, candidates []store.WhoCandidate) error {
	body := contractErrorBody{
		Code:       "ambiguous_who",
		Message:    "--who matched more than one person",
		Remedy:     "Retry with one identifier from candidates.",
		Candidates: whoCandidates(candidates),
	}
	return r.contractBodyError(4, body, ambiguousWhoText(query, who, candidates))
}

func (r *runtime) unknownWhoError(who string, didYouMean []store.WhoCandidate) error {
	candidates := whoCandidates(didYouMean)
	body := contractErrorBody{
		Code:    "unknown_who",
		Message: "--who did not match a person",
		Remedy:  "Run telecrawl who <name>, or search without --who to check whether matching messages exist.",
		Hint:    "Search without --who to check whether matching messages exist.",
	}
	body.DidYouMean = &candidates
	return r.contractBodyError(5, body, unknownWhoText(who, didYouMean))
}

func ambiguousWhoText(query, who string, candidates []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "ambiguous --who %q: %d people match.\n\n", who, len(candidates))
	writeWhoTable(&out, candidates, terminalWidth())
	if retry := retrySearchExample(query, candidates); retry != "" {
		fmt.Fprintf(&out, "\nRetry with: %s", retry)
	}
	return strings.TrimRight(out.String(), "\n")
}

func unknownWhoText(who string, didYouMean []store.WhoCandidate) string {
	var out strings.Builder
	fmt.Fprintf(&out, "unknown --who %q: no person matched.", who)
	if len(didYouMean) == 0 {
		out.WriteString("\nSearch without --who to check whether matching messages exist.")
		return out.String()
	}
	out.WriteString("\n\nDid you mean:\n")
	writeWhoTable(&out, didYouMean, terminalWidth())
	if retry := retrySearchExample("", didYouMean); retry != "" {
		fmt.Fprintf(&out, "\nRetry with: %s", retry)
	}
	return strings.TrimRight(out.String(), "\n")
}

func retrySearchExample(query string, candidates []store.WhoCandidate) string {
	if len(candidates) == 0 {
		return ""
	}
	who := firstRetryIdentifier(candidates[0])
	if who == "" {
		return ""
	}
	parts := []string{"telecrawl", "search"}
	if strings.TrimSpace(query) != "" {
		parts = append(parts, quoteShellArg(query))
	}
	parts = append(parts, "--who", quoteShellArg(who))
	return strings.Join(parts, " ")
}

func firstRetryIdentifier(candidate store.WhoCandidate) string {
	for _, identifier := range candidate.Identifiers {
		if strings.TrimSpace(identifier) != "" {
			return identifier
		}
	}
	return candidate.Who
}

func quoteShellArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	if strings.ContainsAny(value, " \t\n\"'") {
		return strconv.Quote(value)
	}
	return value
}

func (r *runtime) printWho(value whoEnvelope) error {
	candidates := make([]store.WhoCandidate, 0, len(value.Candidates))
	for _, candidate := range value.Candidates {
		candidates = append(candidates, store.WhoCandidate{
			Who:         candidate.Who,
			Identifiers: candidate.Identifiers,
			LastSeen:    parseRenderTime(candidate.LastSeen),
			Messages:    candidate.Messages,
		})
	}
	writeWhoTable(r.stdout, candidates, terminalWidth())
	return nil
}

func writeWhoTable(w io.Writer, candidates []store.WhoCandidate, width int) {
	rows := make([][]string, 0, len(candidates)+1)
	rows = append(rows, []string{"Who", "Last seen", "Messages", "Identifiers"})
	for _, candidate := range candidates {
		rows = append(rows, []string{
			outputField(candidate.Who),
			formatOptionalTime(candidate.LastSeen),
			strconv.Itoa(candidate.Messages),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	writeFittedTable(w, rows, width)
}

func writeFittedTable(w io.Writer, rows [][]string, width int) {
	if width <= 0 {
		width = 100
	}
	if len(rows) == 0 {
		return
	}
	colWidths := whoTableColumnWidths(rows, width)
	for _, row := range rows {
		wrapped := wrapTableRow(row, colWidths)
		lineCount := 0
		if len(wrapped) > 0 {
			lineCount = len(wrapped[0])
		}
		for line := 0; line < lineCount; line++ {
			for col := 0; col < len(colWidths); col++ {
				value := ""
				if line < len(wrapped[col]) {
					value = wrapped[col][line]
				}
				if col == len(colWidths)-1 {
					_, _ = io.WriteString(w, value)
					continue
				}
				_, _ = fmt.Fprintf(w, "%-*s  ", colWidths[col], value)
			}
			_, _ = io.WriteString(w, "\n")
		}
	}
}

func whoTableColumnWidths(rows [][]string, width int) []int {
	messagesWidth := max(tableColumnWidth(rows, 2), len("Messages"))
	lastSeenWidth := max(tableColumnWidth(rows, 1), len("Last seen"))
	lastSeenWidth = min(max(lastSeenWidth, len(time.RFC3339)), 25)
	if width < 60 {
		lastSeenWidth = 10
	}
	whoWidth := min(max(tableColumnWidth(rows, 0), len("Who")), 30)
	identWidth := width - whoWidth - lastSeenWidth - messagesWidth - 6
	if identWidth < 8 {
		identWidth = 8
		whoWidth = max(8, width-lastSeenWidth-messagesWidth-identWidth-6)
	}
	return []int{whoWidth, lastSeenWidth, messagesWidth, identWidth}
}

func tableColumnWidth(rows [][]string, col int) int {
	width := 0
	for _, row := range rows {
		if col >= len(row) {
			continue
		}
		width = max(width, len([]rune(row[col])))
	}
	return width
}

func wrapTableRow(row []string, widths []int) [][]string {
	wrapped := make([][]string, len(widths))
	maxLines := 1
	for i, width := range widths {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		wrapped[i] = wrapTableCell(value, width)
		if len(wrapped[i]) > maxLines {
			maxLines = len(wrapped[i])
		}
	}
	for i := range wrapped {
		for len(wrapped[i]) < maxLines {
			wrapped[i] = append(wrapped[i], "")
		}
	}
	return wrapped
}

func wrapTableCell(value string, width int) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{""}
	}
	if width <= 0 {
		return []string{value}
	}
	var lines []string
	for _, field := range strings.Fields(value) {
		for len([]rune(field)) > width {
			lines = append(lines, string([]rune(field)[:width]))
			field = string([]rune(field)[width:])
		}
		if len(lines) == 0 || len([]rune(lines[len(lines)-1]))+1+len([]rune(field)) > width {
			lines = append(lines, field)
			continue
		}
		lines[len(lines)-1] += " " + field
	}
	return lines
}

func terminalWidth() int {
	width, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS")))
	if err == nil && width >= 40 {
		return width
	}
	return 100
}
