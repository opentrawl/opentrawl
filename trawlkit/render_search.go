package trawlkit

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type searchOutput struct {
	Query       string `json:"query"`
	SourceID    string `json:"-"`
	SupportsWho bool   `json:"-"`
	SearchResult
}

func writeSearchText(w io.Writer, value searchOutput) error {
	items := make([]render.ListItem, 0, len(value.Results))
	for _, hit := range value.Results {
		items = append(items, render.ListItem{
			Time:     hit.Time,
			DateOnly: hit.AllDay,
			Source:   hit.Source,
			// The reader copies a short ref, never a long machine ref. The kit
			// fills ShortRef from the shared index; a hit with no alias yet (a
			// source that indexes none) falls back to the full ref.
			Ref:  firstText(hit.ShortRef, hit.Ref),
			Text: SearchResultText(hit.Summary, hit.Evidence),
		})
	}
	hints := []string{}
	sourceID := strings.TrimSpace(value.SourceID)
	if sourceID != "" {
		hints = append(hints, "Open: trawl "+sourceID+" open REF")
	}
	if value.Truncated {
		if sourceID != "" && strings.TrimSpace(value.Query) != "" {
			hints = append(hints, fmt.Sprintf("More: trawl %s search %s --limit %d", sourceID, quoteSearchArg(value.Query), nextSearchLimit(len(value.Results))))
		}
		hints = append(hints, narrowSearchHint(value.SupportsWho))
	}
	return render.WriteList(w, render.List{
		Heading:   searchHeading(value.Query, resolvedWhoName(value.WhoResolved), len(value.Results), max(value.TotalMatches, len(value.Results))),
		Hints:     hints,
		Items:     items,
		ClampText: 2,
		Empty:     searchEmptyText(value.Query),
	})
}

func SearchResultText(summary ResultSummary, evidence []EvidenceFragment) string {
	title := strings.TrimSpace(summary.Title)
	if subtitle := strings.TrimSpace(summary.Subtitle); subtitle != "" {
		title = title + " — " + subtitle
	}
	detail := SearchEvidenceText(evidence)
	if title == "" {
		return detail
	}
	if detail == "" {
		return title
	}
	return title + " · " + detail
}

func SearchEvidenceText(values []EvidenceFragment) string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		var runs []TextRun
		switch {
		case value.Text != nil:
			runs = value.Text.Runs
		case value.Field != nil:
			runs = value.Field.Value
		case value.Media != nil:
			runs = value.Media.Description
		case value.Relation != nil:
			runs = value.Relation.Target
		}
		var text strings.Builder
		for _, run := range runs {
			text.WriteString(run.Text)
		}
		label := strings.TrimSpace(value.Label)
		content := strings.TrimSpace(text.String())
		switch {
		case label != "" && content != "":
			parts = append(parts, label+": "+content)
		case content != "":
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, " · ")
}

func narrowSearchHint(supportsWho bool) string {
	if supportsWho {
		return "Narrow results with --who, --after, or --before."
	}
	return "Narrow results with --after or --before."
}

func nextSearchLimit(shown int) int {
	if shown < 1 {
		return 20
	}
	return shown * 2
}

func quoteSearchArg(value string) string {
	return strconv.Quote(strings.TrimSpace(value))
}

func searchHeading(query, who string, returned, total int) string {
	query = strings.TrimSpace(query)
	who = strings.TrimSpace(who)
	shown := render.FormatInteger(int64(returned))
	matches := render.FormatInteger(int64(total))
	switch {
	case query != "" && who != "":
		return fmt.Sprintf("Search %q with %s: showing %s of %s, newest first.", query, who, shown, matches)
	case query != "":
		return fmt.Sprintf("Search %q: showing %s of %s, newest first.", query, shown, matches)
	case who != "":
		return fmt.Sprintf("Search with %s: showing %s of %s, newest first.", who, shown, matches)
	default:
		return fmt.Sprintf("Search filters: showing %s of %s, newest first.", shown, matches)
	}
}

func searchEmptyText(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "No matching items."
	}
	return fmt.Sprintf("No matches for %q.", query)
}

func resolvedWhoName(who *WhoResolved) string {
	if who == nil {
		return ""
	}
	return strings.Join(strings.Fields(who.Who), " ")
}
