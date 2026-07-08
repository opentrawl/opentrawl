package calcrawl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/whomatch"
	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/calendarstore"
)

const staleAfter = 24 * time.Hour

type Crawler struct{}

var _ crawlkit.FullCrawler = (*Crawler)(nil)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          archive.AppID,
		Surface:     "calendar",
		DisplayName: archive.DisplayName,
		Description: "Local-first Apple Calendar archive crawler.",
		ShortRefs:   true,
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"apple-calendar", "sqlite", "calendar-event-search", "contact-export"},
		},
	}
}

func (c *Crawler) Verbs() []crawlkit.Verb {
	return nil
}

func (c *Crawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(archive.AppID, "Archive has not been synced.")
	status.State = "missing"
	if req.Store == nil {
		return &status, nil
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Archive could not be inspected."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.ArchivePath
	status.LastSyncAt = localRFC3339(archiveStatus.LastSyncAt)
	status.Counts = []control.Count{
		control.NewCount("events", "events", archiveStatus.Events),
		control.NewCount("calendars", "calendars", archiveStatus.Calendars),
		control.NewCount("since", "since", archive.YearFromUnix(archiveStatus.EarliestUnix)),
	}
	switch {
	case archiveStatus.Events == 0:
		status.State = "empty"
		status.Summary = "Archive is empty."
	case isStale(archiveStatus):
		status.State = "stale"
		status.Summary = "Archive is stale."
	default:
		status.State = "ok"
		status.Summary = "Archive is fresh."
	}
	return &status, nil
}

func (c *Crawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	return &crawlkit.Doctor{Checks: []crawlkit.Check{
		checkSourceStore(ctx),
		checkArchivePresent(req),
		checkArchiveSchema(ctx, req),
	}}, nil
}

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, query crawlkit.Query) (crawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return crawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	var whoFilter *archive.WhoFilter
	var whoResolved *crawlkit.WhoResolved
	if strings.TrimSpace(query.Who) != "" {
		candidate, err := resolveArchiveWho(ctx, st, query.Text, query.Who)
		if err != nil {
			return crawlkit.SearchResult{}, err
		}
		filter := candidate.Filter()
		whoFilter = filter
		resolved := candidate.Resolved()
		whoResolved = &crawlkit.WhoResolved{Who: resolved.Who, Identifiers: append([]string(nil), resolved.Identifiers...)}
	}
	results, total, err := st.Search(ctx, query.Text, archive.SearchOptions{
		Limit:  query.Limit,
		After:  unixOrZero(query.After),
		Before: unixOrZero(query.Before),
		Who:    whoFilter,
	})
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	hits := make([]crawlkit.Hit, 0, len(results))
	for _, result := range results {
		hit, err := searchHit(result)
		if err != nil {
			return crawlkit.SearchResult{}, err
		}
		hits = append(hits, hit)
	}
	_ = req.Log.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(results), total))
	return crawlkit.SearchResult{
		WhoResolved:  whoResolved,
		Results:      hits,
		TotalMatches: int(total),
		Truncated:    query.Limit > 0 && int64(len(results)) < total,
	}, nil
}

func (c *Crawler) Who(ctx context.Context, req *crawlkit.Request, person string) ([]whomatch.Candidate, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	candidates, err := st.ResolveWho(ctx, person)
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidate(candidate))
	}
	return out, nil
}

func (c *Crawler) ContactExport(ctx context.Context, req *crawlkit.Request) (*control.ContactExport, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	contacts, err := st.ExportContacts(ctx)
	if err != nil {
		return nil, err
	}
	return &control.ContactExport{Contacts: contacts}, nil
}

func checkSourceStore(ctx context.Context) crawlkit.Check {
	if err := calendarstore.CanaryRead(ctx, calendarstore.DefaultPath()); err != nil {
		return crawlkit.Check{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot read the Calendar database",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return crawlkit.Check{ID: "source_store", State: "ok"}
}

func checkArchivePresent(req *crawlkit.Request) crawlkit.Check {
	if req.Store == nil {
		return crawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "archive has not been synced",
			Remedy:  "run: trawl calcrawl sync",
		}
	}
	return crawlkit.Check{ID: "archive", State: "ok"}
}

func checkArchiveSchema(ctx context.Context, req *crawlkit.Request) crawlkit.Check {
	if req.Store == nil {
		return crawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema is not current",
			Remedy:  "run: trawl calcrawl sync",
		}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return crawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema is not current",
			Remedy:  "run: trawl calcrawl sync",
		}
	}
	if _, err := st.Status(ctx); err != nil {
		return crawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema could not be inspected",
			Remedy:  "run: trawl calcrawl sync",
		}
	}
	return crawlkit.Check{ID: "schema", State: "ok"}
}

func isStale(status archive.Status) bool {
	lastSync, err := time.Parse(time.RFC3339Nano, status.LastSyncAt)
	if err != nil || time.Since(lastSync) > staleAfter {
		return true
	}
	sourceModified, err := calendarstore.ModifiedAt(calendarstore.DefaultPath())
	if err != nil {
		return true
	}
	syncedSource, err := time.Parse(time.RFC3339Nano, status.SourceModifiedAt)
	if err != nil {
		return true
	}
	return sourceModified.UTC().After(syncedSource.Add(time.Second))
}

func localRFC3339(value string) string {
	if value == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return t.Local().Format(time.RFC3339)
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func searchHit(result archive.SearchResult) (crawlkit.Hit, error) {
	t, err := parseEventTime(result.Time)
	if err != nil {
		return crawlkit.Hit{}, err
	}
	return crawlkit.Hit{
		Ref:      result.Ref,
		ShortRef: result.ShortRef,
		Time:     t,
		Who:      result.Who,
		Where:    result.Where,
		Snippet:  result.Snippet,
		AllDay:   result.AllDay,
	}, nil
}

func whoCandidate(candidate archive.WhoCandidate) whomatch.Candidate {
	lastSeen, _ := parseEventTime(candidate.LastSeen)
	return whomatch.Candidate{
		Who:         candidate.Who,
		Identifiers: append([]string(nil), candidate.Identifiers...),
		LastSeen:    lastSeen,
		Messages:    candidate.Messages,
	}
}

func resolveArchiveWho(ctx context.Context, st *archive.Store, query, who string) (archive.WhoCandidate, error) {
	candidates, err := st.ResolveWho(ctx, who)
	if err != nil {
		return archive.WhoCandidate{}, err
	}
	resolved := make([]archive.WhoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ResolvesWho(who) {
			resolved = append(resolved, candidate)
		}
	}
	switch len(resolved) {
	case 0:
		return archive.WhoCandidate{}, unknownWhoError(query, who, candidates)
	case 1:
		return resolved[0], nil
	default:
		return archive.WhoCandidate{}, ambiguousWhoError(who, resolved)
	}
}

func ambiguousWhoError(who string, candidates []archive.WhoCandidate) error {
	return commandError{
		code:    4,
		name:    "ambiguous_who",
		message: "--who matched more than one person",
		remedy:  "Retry with one identifier from candidates.",
		err:     fmt.Errorf("ambiguous --who %q", who),
	}
}

func unknownWhoError(query, who string, didYouMean []archive.WhoCandidate) error {
	remedy := "Run who <name>, or search without --who to check whether matching items exist."
	if len(didYouMean) == 0 && strings.TrimSpace(query) != "" {
		remedy = "Search without --who to check whether the text exists."
	}
	return commandError{
		code:    5,
		name:    "unknown_who",
		message: "--who did not match a person",
		remedy:  remedy,
		err:     fmt.Errorf("unknown --who %q", who),
	}
}

func parseEventTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("invalid event time %q", value)
}
