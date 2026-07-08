package render

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// TranscriptRow is one pre-rendered line in an ordered transcript.
type TranscriptRow struct {
	Time               time.Time
	Line               string
	Prefix             string
	Text               string
	ContinuationIndent string
}

type TranscriptHeader struct {
	Title        string
	Ref          string
	Participants []string
}

func WriteTranscriptHeader(w io.Writer, header TranscriptHeader) error {
	title := strings.TrimSpace(header.Title)
	if title != "" {
		for _, line := range WrapWithIndent("Transcript: ", title, OutputWidth(w), "") {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}
	if ref := strings.TrimSpace(header.Ref); ref != "" {
		if err := WriteWrappedField(w, "Ref", ref); err != nil {
			return err
		}
	}
	if len(header.Participants) > 0 {
		parts := make([]string, 0, len(header.Participants))
		for _, participant := range header.Participants {
			if participant = HumanIdentity(participant); participant != "" {
				parts = append(parts, participant)
			}
		}
		if len(parts) > 0 {
			if err := WriteWrappedField(w, "Participants", strings.Join(parts, ", ")); err != nil {
				return err
			}
		}
	}
	return nil
}

// WriteTranscript writes ordered transcript rows with a day separator whenever
// the row date changes.
func WriteTranscript(w io.Writer, rows []TranscriptRow) error {
	var currentDay time.Time
	width := OutputWidth(w)
	for _, row := range rows {
		day := transcriptDay(row.Time)
		if !day.IsZero() && (currentDay.IsZero() || !sameTranscriptDay(currentDay, day)) {
			if _, err := fmt.Fprintf(w, "— %s —\n", row.Time.Format("Mon 2 Jan 2006")); err != nil {
				return err
			}
			currentDay = day
		}
		if row.Prefix != "" || row.Text != "" {
			for _, line := range WrapWithIndent(row.Prefix, row.Text, width, row.ContinuationIndent) {
				if _, err := io.WriteString(w, line); err != nil {
					return err
				}
				if _, err := io.WriteString(w, "\n"); err != nil {
					return err
				}
			}
			continue
		}
		line := strings.TrimRight(row.Line, "\n")
		if line == "" {
			continue
		}
		for _, wrapped := range Wrap(line, width) {
			if _, err := io.WriteString(w, wrapped); err != nil {
				return err
			}
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func transcriptDay(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	year, month, day := value.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, value.Location())
}

func sameTranscriptDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
