package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/imsgcrawl/internal/archive"
)

func printWhoText(w io.Writer, value archive.WhoResolution) error {
	if value.Truncated {
		if _, err := fmt.Fprintf(w, "Who %q: showing %d of %d candidates.\n", value.Query, len(value.Candidates), value.TotalMatches); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintf(w, "Who %q: %d candidates.\n", value.Query, len(value.Candidates)); err != nil {
			return err
		}
	}
	return renderWhoCandidatesTable(w, value.Candidates)
}

func renderWhoCandidatesTable(w io.Writer, candidates []archive.WhoCandidate) error {
	width := normalizeTextTableWidth(render.OutputWidth(w))
	columns := whoTextColumns(width)
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.Who,
			strings.Join(candidate.Identifiers, ", "),
			formatArchiveTime(candidate.LastSeen),
			strconv.FormatInt(candidate.Messages, 10),
		})
	}
	return renderTextTable(w, columns, rows)
}

func whoTextColumns(width int) []textColumn {
	whoWidth := 24
	lastSeenWidth := 16
	messagesWidth := 8
	gaps := 3 * len(textTableGap)
	identifiersWidth := width - whoWidth - lastSeenWidth - messagesWidth - gaps
	if identifiersWidth < 22 {
		needed := 22 - identifiersWidth
		whoReduction := minInt(needed, whoWidth-14)
		whoWidth -= whoReduction
		identifiersWidth = width - whoWidth - lastSeenWidth - messagesWidth - gaps
	}
	if identifiersWidth < 22 {
		identifiersWidth = 22
	}
	return []textColumn{
		{header: "who", width: whoWidth, wrap: true},
		{header: "identifiers", width: identifiersWidth, wrap: true},
		{header: "last seen", width: lastSeenWidth},
		{header: "messages", width: messagesWidth},
	}
}
