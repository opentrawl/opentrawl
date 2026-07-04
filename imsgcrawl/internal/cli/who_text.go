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
	if len(value.Candidates) == 0 {
		_, err := fmt.Fprintf(w, "No people matched %q.\n", value.Query)
		return err
	}
	return writeWhoTable(w, value.Candidates)
}

func writeWhoTable(w io.Writer, candidates []archive.WhoCandidate) error {
	rows := make([][]string, 0, len(candidates))
	for _, candidate := range candidates {
		rows = append(rows, []string{
			candidate.Who,
			shortArchiveTime(candidate.LastSeen),
			strconv.FormatInt(candidate.Messages, 10),
			strings.Join(candidate.Identifiers, ", "),
		})
	}
	return render.WriteTable(w, []render.TableColumn{
		{Header: "who"},
		{Header: "last seen"},
		{Header: "messages", AlignRight: true},
		{Header: "identifiers", Wrap: true},
	}, rows)
}
