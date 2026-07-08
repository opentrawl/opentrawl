package notes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, query crawlkit.Query) (crawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return crawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	results, total, err := st.Search(ctx, query.Text, archive.SearchOptions{
		Limit:  query.Limit,
		After:  query.After,
		Before: query.Before,
	})
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	hits := make([]crawlkit.Hit, 0, len(results))
	for _, result := range results {
		hits = append(hits, crawlkit.Hit{
			Ref:     result.Ref,
			Time:    parseContractTime(result.Time),
			Who:     "me",
			Where:   noteWhere(result),
			Snippet: result.Snippet,
		})
	}
	if req.Log != nil {
		_ = req.Log.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(results), total))
	}
	return crawlkit.SearchResult{
		Results:      hits,
		TotalMatches: int(total),
		Truncated:    query.Limit > 0 && len(results) < int(total),
	}, nil
}

func noteWhere(result archive.SearchResult) string {
	if strings.TrimSpace(result.Title) != "" {
		return strings.TrimSpace(result.Title)
	}
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

func archiveErr(err error) error {
	return commandErr("archive_unreadable", "Notes archive could not be read", "run trawl notes sync", err)
}
