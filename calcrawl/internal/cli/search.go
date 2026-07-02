package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/shortref"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

type searchOutput struct {
	Query        string                 `json:"query"`
	WhoMatched   []string               `json:"who_matched,omitempty"`
	Results      []archive.SearchResult `json:"results"`
	TotalMatches int64                  `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
	Who          string                 `json:"-"`
}

func (r *runtime) runSearch(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"search"})
	}
	query, limit, afterValue, beforeValue, whoValue, whoPassed, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	if query == "" {
		return usageErr(errors.New("search query is required"))
	}
	if limit <= 0 {
		return usageErr(errors.New("search --limit must be positive"))
	}
	whoValue = normalizeIdentity(whoValue)
	if whoPassed && whoValue == "" {
		return usageErr(errors.New("search --who requires an identity"))
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
	st, rebuilt, err := r.openArchiveWithShortRefs()
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	if rebuilt {
		_ = r.log.Info("short_refs_rebuilt", "reason=missing_or_stale")
	}
	var whoMatched []archive.WhoMatch
	if whoPassed {
		whoMatched, err = st.ResolveWho(r.ctx, whoValue)
		if err != nil {
			return err
		}
		if len(whoMatched) == 0 {
			_ = r.log.Info("search_complete", "returned=0 total=0")
			return r.print(searchOutput{Query: query, Who: whoValue, Results: []archive.SearchResult{}, TotalMatches: 0, Truncated: false})
		}
	}
	results, total, err := st.Search(r.ctx, query, archive.SearchOptions{Limit: limit, After: after, Before: before, Who: whoMatched})
	if err != nil {
		return err
	}
	_ = r.log.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(results), total))
	return r.print(searchOutput{
		Query:        query,
		Who:          whoValue,
		WhoMatched:   ambiguousWhoMatches(whoMatched),
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
	st, rebuilt, err := r.openArchiveForRef(ref)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	if rebuilt {
		_ = r.log.Info("short_refs_rebuilt", "reason=missing_or_stale")
	}
	ref, err = r.resolveOpenRef(st, ref)
	if err != nil {
		return err
	}
	event, err := st.OpenEvent(r.ctx, ref)
	if err != nil {
		return err
	}
	_ = r.log.Info("open_complete", "result=event")
	return r.print(event)
}

func parseSearchArgs(args []string) (string, int, string, string, string, bool, error) {
	limit := archive.DefaultSearchLimit
	queryParts := []string{}
	after := ""
	before := ""
	who := ""
	whoPassed := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--limit":
			i++
			if i >= len(args) {
				return "", 0, "", "", "", false, usageErr(errors.New("search --limit requires a value"))
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return "", 0, "", "", "", false, usageErr(fmt.Errorf("search --limit must be a number: %w", err))
			}
			limit = value
		case strings.HasPrefix(arg, "--limit="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit="))
			if err != nil {
				return "", 0, "", "", "", false, usageErr(fmt.Errorf("search --limit must be a number: %w", err))
			}
			limit = value
		case arg == "--after":
			i++
			if i >= len(args) {
				return "", 0, "", "", "", false, usageErr(errors.New("search --after requires a value"))
			}
			after = args[i]
		case strings.HasPrefix(arg, "--after="):
			after = strings.TrimPrefix(arg, "--after=")
		case arg == "--before":
			i++
			if i >= len(args) {
				return "", 0, "", "", "", false, usageErr(errors.New("search --before requires a value"))
			}
			before = args[i]
		case strings.HasPrefix(arg, "--before="):
			before = strings.TrimPrefix(arg, "--before=")
		case arg == "--who":
			i++
			whoPassed = true
			if i >= len(args) {
				return "", 0, "", "", "", false, usageErr(errors.New("search --who requires an identity"))
			}
			who = args[i]
		case strings.HasPrefix(arg, "--who="):
			whoPassed = true
			who = strings.TrimPrefix(arg, "--who=")
		default:
			queryParts = append(queryParts, arg)
		}
	}
	return strings.TrimSpace(strings.Join(queryParts, " ")), limit, after, before, who, whoPassed, nil
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

func (r *runtime) openArchiveForRef(ref string) (*archive.Store, bool, error) {
	if strings.Contains(ref, ":") {
		st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
		return st, false, err
	}
	return r.openArchiveWithShortRefs()
}

func (r *runtime) openArchiveWithShortRefs() (*archive.Store, bool, error) {
	st, err := archive.OpenExisting(r.ctx, archive.DefaultPath())
	if err != nil {
		return nil, false, err
	}
	current, err := st.ShortRefsCurrent(r.ctx)
	if err != nil {
		_ = st.Close()
		return nil, false, err
	}
	if current {
		return st, false, nil
	}
	_ = st.Close()
	st, err = archive.OpenExistingWritable(r.ctx, archive.DefaultPath())
	if err != nil {
		return nil, false, err
	}
	rebuilt, err := st.EnsureShortRefs(r.ctx)
	if err != nil {
		_ = st.Close()
		return nil, false, err
	}
	return st, rebuilt, nil
}

func (r *runtime) resolveOpenRef(st *archive.Store, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return ref, nil
	}
	if !shortref.ValidAlias(ref) {
		return "", commandErr(1, "unknown_short_ref", fmt.Errorf("unknown short ref %q", ref), "rerun search or use the full ref")
	}
	matches, err := st.ResolveShortRef(r.ctx, ref)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", commandErr(1, "unknown_short_ref", fmt.Errorf("unknown short ref %q", ref), "rerun search or use the full ref")
	case 1:
		return matches[0], nil
	default:
		return "", commandErr(1, "ambiguous_short_ref", fmt.Errorf("short ref %q matches %d events", ref, len(matches)), "rerun search or use the full ref")
	}
}

func ambiguousWhoMatches(matches []archive.WhoMatch) []string {
	if len(matches) <= 1 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		out = append(out, whoMatchLabel(match))
	}
	return out
}

func normalizeIdentity(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func whoMatchLabel(match archive.WhoMatch) string {
	for _, value := range []string{match.DisplayName, match.Email, match.PhoneNumber, match.Address} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "unknown"
}
