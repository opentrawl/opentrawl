package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

type searchOutput struct {
	Query        string                 `json:"query"`
	Results      []archive.SearchResult `json:"results"`
	TotalMatches int64                  `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
}

func (r *runtime) runSearch(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"search"})
	}
	query, limit, afterValue, beforeValue, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	if query == "" {
		return usageErr(errors.New("search query is required"))
	}
	if limit <= 0 {
		return usageErr(errors.New("search --limit must be positive"))
	}
	capped := false
	if limit > archive.MaxSearchLimit {
		limit = archive.MaxSearchLimit
		capped = true
	}
	after, err := parseBound(afterValue, false)
	if err != nil {
		return usageErr(fmt.Errorf("invalid --after: %w", err))
	}
	before, err := parseBound(beforeValue, true)
	if err != nil {
		return usageErr(fmt.Errorf("invalid --before: %w", err))
	}
	st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	results, total, err := st.Search(r.ctx, query, limit, after, before)
	if err != nil {
		return err
	}
	return r.print(searchOutput{
		Query:        query,
		Results:      results,
		TotalMatches: total,
		Truncated:    int64(len(results)) < total || capped,
	})
}

func (r *runtime) runOpen(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"open"})
	}
	ref, err := oneArg(args, "open")
	if err != nil {
		return err
	}
	st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	event, err := st.OpenEvent(r.ctx, ref)
	if err != nil {
		return err
	}
	return r.print(event)
}

func parseSearchArgs(args []string) (string, int, string, string, error) {
	limit := archive.DefaultSearchLimit
	queryParts := []string{}
	after := ""
	before := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--limit":
			i++
			if i >= len(args) {
				return "", 0, "", "", usageErr(errors.New("search --limit requires a value"))
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return "", 0, "", "", usageErr(fmt.Errorf("search --limit must be a number: %w", err))
			}
			limit = value
		case strings.HasPrefix(arg, "--limit="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit="))
			if err != nil {
				return "", 0, "", "", usageErr(fmt.Errorf("search --limit must be a number: %w", err))
			}
			limit = value
		case arg == "--after":
			i++
			if i >= len(args) {
				return "", 0, "", "", usageErr(errors.New("search --after requires a value"))
			}
			after = args[i]
		case strings.HasPrefix(arg, "--after="):
			after = strings.TrimPrefix(arg, "--after=")
		case arg == "--before":
			i++
			if i >= len(args) {
				return "", 0, "", "", usageErr(errors.New("search --before requires a value"))
			}
			before = args[i]
		case strings.HasPrefix(arg, "--before="):
			before = strings.TrimPrefix(arg, "--before=")
		default:
			queryParts = append(queryParts, arg)
		}
	}
	return strings.TrimSpace(strings.Join(queryParts, " ")), limit, after, before, nil
}

func parseBound(value string, endOfDay bool) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t.Unix(), nil
	}
	t, err := time.ParseInLocation("2006-01-02", value, time.Local)
	if err != nil {
		return 0, err
	}
	if endOfDay {
		t = t.Add(24*time.Hour - time.Second)
	}
	return t.Unix(), nil
}
