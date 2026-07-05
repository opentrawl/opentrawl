package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/flags"
	"github.com/openclaw/crawlkit/shortref"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
)

// searchRequest is the parsed search command line, before the archive opens.
type searchRequest struct {
	query     string
	limit     int
	limitSet  bool
	all       bool
	after     string
	before    string
	who       string
	whoPassed bool
}

type searchOutput struct {
	Query        string                 `json:"query"`
	WhoResolved  *archive.WhoResolved   `json:"who_resolved,omitempty"`
	Results      []archive.SearchResult `json:"results"`
	TotalMatches int64                  `json:"total_matches"`
	Truncated    bool                   `json:"truncated"`
	WhoQuery     string                 `json:"-"`
	Limit        int                    `json:"-"`
	After        string                 `json:"-"`
	Before       string                 `json:"-"`
}

func (r *runtime) runSearch(args []string) error {
	if hasHelpFlag(args) {
		return printCommandUsage(r.stdout, []string{"search"})
	}
	req, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	if req.query == "" && !req.whoPassed && strings.TrimSpace(req.after) == "" && strings.TrimSpace(req.before) == "" {
		return usageErr(errors.New("search query is required"))
	}
	limit, err := flags.Limit(req.limit, req.limitSet, req.all)
	if err != nil {
		return usageErr(err)
	}
	whoValue := normalizeIdentity(req.who)
	if req.whoPassed && whoValue == "" {
		return usageErr(errors.New("search --who requires an identity"))
	}
	after, err := parseBound(req.after, false)
	if err != nil {
		return usageErr(fmt.Errorf("invalid --after: %w", err))
	}
	before, err := parseBound(req.before, true)
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
	var whoResolved *archive.WhoResolved
	var whoFilter *archive.WhoFilter
	if req.whoPassed {
		candidate, err := r.resolveSearchWho(st, req.query, whoValue)
		if err != nil {
			return err
		}
		resolved := candidate.Resolved()
		whoResolved = &resolved
		whoFilter = candidate.Filter()
	}
	results, total, err := st.Search(r.ctx, req.query, archive.SearchOptions{Limit: limit, After: after, Before: before, Who: whoFilter})
	if err != nil {
		return err
	}
	_ = r.log.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(results), total))
	return r.print(searchOutput{
		Query:        req.query,
		WhoQuery:     whoValue,
		WhoResolved:  whoResolved,
		Results:      results,
		TotalMatches: total,
		Truncated:    limit > 0 && int64(len(results)) < total,
		Limit:        limit,
		After:        strings.TrimSpace(req.after),
		Before:       strings.TrimSpace(req.before),
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

func parseSearchArgs(args []string) (searchRequest, error) {
	req := searchRequest{limit: archive.DefaultSearchLimit}
	queryParts := []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all":
			req.all = true
		case arg == "--limit":
			i++
			if i >= len(args) {
				return searchRequest{}, usageErr(errors.New("search --limit requires a value"))
			}
			value, err := strconv.Atoi(args[i])
			if err != nil {
				return searchRequest{}, usageErr(fmt.Errorf("search --limit must be a number: %w", err))
			}
			req.limit = value
			req.limitSet = true
		case strings.HasPrefix(arg, "--limit="):
			value, err := strconv.Atoi(strings.TrimPrefix(arg, "--limit="))
			if err != nil {
				return searchRequest{}, usageErr(fmt.Errorf("search --limit must be a number: %w", err))
			}
			req.limit = value
			req.limitSet = true
		case arg == "--after":
			i++
			if i >= len(args) {
				return searchRequest{}, usageErr(errors.New("search --after requires a value"))
			}
			req.after = args[i]
		case strings.HasPrefix(arg, "--after="):
			req.after = strings.TrimPrefix(arg, "--after=")
		case arg == "--before":
			i++
			if i >= len(args) {
				return searchRequest{}, usageErr(errors.New("search --before requires a value"))
			}
			req.before = args[i]
		case strings.HasPrefix(arg, "--before="):
			req.before = strings.TrimPrefix(arg, "--before=")
		case arg == "--who":
			i++
			req.whoPassed = true
			if i >= len(args) {
				return searchRequest{}, usageErr(errors.New("search --who requires an identity"))
			}
			req.who = args[i]
		case strings.HasPrefix(arg, "--who="):
			req.whoPassed = true
			req.who = strings.TrimPrefix(arg, "--who=")
		default:
			if strings.HasPrefix(arg, "-") {
				return searchRequest{}, usageErr(fmt.Errorf("unknown search flag %q", arg))
			}
			queryParts = append(queryParts, arg)
		}
	}
	req.query = strings.TrimSpace(strings.Join(queryParts, " "))
	return req, nil
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

func normalizeIdentity(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
