package notes

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	results, total, err := st.Search(ctx, query.Text, archive.SearchOptions{
		Limit:  query.Limit,
		After:  query.After,
		Before: query.Before,
	})
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	hits := make([]trawlkit.Hit, 0, len(results))
	for _, result := range results {
		title := strings.TrimSpace(result.Title)
		if title == "" {
			title = "Note"
		}
		anchorID := trawlkit.MatchAnchorID
		if len(result.Matches) > 0 {
			anchorID = result.Matches[0].Field
		}
		hits = append(hits, trawlkit.Hit{
			Ref:      result.Ref,
			Time:     parseContractTime(result.Time),
			AnchorID: anchorID,
			Summary:  trawlkit.ResultSummary{Title: title, Subtitle: noteWhere(result)},
			Evidence: noteSearchEvidence(result.Matches),
		})
	}
	if req.Log != nil {
		_ = req.Log.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(results), total))
	}
	return trawlkit.SearchResult{
		Results:      hits,
		TotalMatches: int(total),
		Truncated:    query.Limit > 0 && len(results) < int(total),
	}, nil
}

func noteSearchEvidence(matches []archive.SearchMatch) []trawlkit.EvidenceFragment {
	evidence := make([]trawlkit.EvidenceFragment, 0, len(matches))
	for _, match := range matches {
		label := "Note body"
		if match.Field == "title" {
			label = "Title"
		}
		runs := make([]trawlkit.TextRun, 0, len(match.Runs))
		for _, run := range match.Runs {
			runs = append(runs, trawlkit.TextRun{Text: run.Text, Matched: run.Matched})
		}
		evidence = append(evidence, trawlkit.EvidenceFragment{Label: label, Field: &trawlkit.FieldEvidence{Name: match.Field, Value: runs}})
	}
	return evidence
}

func noteWhere(result archive.SearchResult) string {
	if strings.TrimSpace(result.Folder) != "" {
		return strings.TrimSpace(result.Folder)
	}
	return "Notes"
}

func parseContractTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, value); err == nil {
			return t
		}
	}
	return time.Time{}
}

// humanTime turns a stored RFC3339 timestamp into the short local form a reader
// scans (2006-01-02 15:04), matching search output. An unparseable or empty
// value falls back to the raw string rather than an empty cell.
func humanTime(value string) string {
	t := parseContractTime(value)
	if t.IsZero() {
		return strings.TrimSpace(value)
	}
	return render.ShortLocalTime(t)
}

// archiveErr turns a failed archive.UseExisting into the one-line, truthful
// message a read verb shows. An older-schema archive names what sync will do
// about it (park the old file and rebuild); a newer-schema archive says this
// build cannot touch it; anything else falls back to the generic message.
func archiveErr(err error) error {
	switch {
	case errors.Is(err, archive.ErrSchemaOutdated):
		return commandErr("archive_schema_outdated",
			"Archive is from an older build; trawl notes sync will park it and rebuild.",
			"run trawl notes sync", err)
	case errors.Is(err, archive.ErrSchemaNewer):
		return commandErr("archive_schema_newer",
			"Archive was written by a newer build of trawl notes than this one.",
			"update trawl notes, then run trawl notes sync", err)
	default:
		return commandErr("archive_unreadable", "Notes archive could not be read", "run trawl notes sync", err)
	}
}
