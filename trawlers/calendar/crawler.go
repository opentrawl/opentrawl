package calcrawl

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/calcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/calcrawl/internal/calendarstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

const staleAfter = 24 * time.Hour

type Crawler struct{}

var (
	_ trawlkit.Crawler         = (*Crawler)(nil)
	_ trawlkit.Syncer          = (*Crawler)(nil)
	_ trawlkit.Searcher        = (*Crawler)(nil)
	_ trawlkit.WhoMatcher      = (*Crawler)(nil)
	_ trawlkit.Opener          = (*Crawler)(nil)
	_ trawlkit.ContactExporter = (*Crawler)(nil)
)

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          archive.AppID,
		Surface:     "calendar",
		DisplayName: archive.DisplayName,
		Headlines:   []string{"events", "calendars"},
		Privacy: control.Privacy{
			ContainsPrivateMessages: true,
			ExportsSecrets:          false,
			LocalOnlyScopes:         []string{"apple-calendar", "sqlite", "calendar-event-search", "contact-export"},
		},
	}
}

func (c *Crawler) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		{
			Name:    "calendars annotate",
			Help:    "Record the user's stated meaning for a calendar. This writes to the local archive.",
			Args:    []string{"CALENDAR_ID", "MEANING"},
			Mutates: true,
			Store:   trawlkit.StoreRequired,
			Run:     c.annotateCalendar,
		},
		{
			Name:  "calendars",
			Help:  "List archived calendars.",
			Store: trawlkit.StoreRequired,
			Run:   c.calendars,
		},
	}
}

func (c *Crawler) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
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
		status.Summary = "Needs sync."
	default:
		status.State = "ok"
		status.Summary = "Recently synced."
	}
	return &status, nil
}

func (c *Crawler) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	return &trawlkit.Doctor{Checks: []trawlkit.Check{
		checkSourceStore(ctx),
		checkArchivePresent(req),
		checkArchiveSchema(ctx, req),
	}}, nil
}

func (c *Crawler) Search(ctx context.Context, req *trawlkit.Request, query trawlkit.Query) (trawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	var whoFilter *archive.WhoFilter
	var whoResolved *trawlkit.WhoResolved
	if strings.TrimSpace(query.Who) != "" {
		candidate, err := resolveArchiveWho(ctx, st, query.Text, query.Who)
		if err != nil {
			return trawlkit.SearchResult{}, err
		}
		filter := candidate.Filter()
		whoFilter = filter
		resolved := candidate.Resolved()
		whoResolved = &trawlkit.WhoResolved{Who: resolved.Who, Identifiers: append([]string(nil), resolved.Identifiers...)}
	}
	results, total, err := st.Search(ctx, query.Text, archive.SearchOptions{
		Limit:  query.Limit,
		After:  unixOrZero(query.After),
		Before: unixOrZero(query.Before),
		Who:    whoFilter,
	})
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	hits := make([]trawlkit.Hit, 0, len(results))
	for _, result := range results {
		hit, err := searchHit(result)
		if err != nil {
			return trawlkit.SearchResult{}, err
		}
		hits = append(hits, hit)
	}
	_ = req.Log.Info("search_complete", fmt.Sprintf("returned=%d total=%d", len(results), total))
	return trawlkit.SearchResult{
		WhoResolved:  whoResolved,
		Results:      hits,
		TotalMatches: int(total),
		Truncated:    query.Limit > 0 && int64(len(results)) < total,
	}, nil
}

func (c *Crawler) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
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

func (c *Crawler) ContactExport(ctx context.Context, req *trawlkit.Request) (*control.ContactExport, error) {
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

func checkSourceStore(ctx context.Context) trawlkit.Check {
	if err := calendarstore.CanaryRead(ctx, calendarstore.DefaultPath()); err != nil {
		return trawlkit.Check{
			ID:      "source_store",
			State:   "fail",
			Message: "cannot read the Calendar database",
			Remedy:  fullDiskAccessRemedy,
		}
	}
	return trawlkit.Check{ID: "source_store", State: "ok"}
}

func checkArchivePresent(req *trawlkit.Request) trawlkit.Check {
	if req.Store == nil {
		return trawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "archive has not been synced",
			Remedy:  "run trawl calendar sync",
		}
	}
	return trawlkit.Check{ID: "archive", State: "ok"}
}

func checkArchiveSchema(ctx context.Context, req *trawlkit.Request) trawlkit.Check {
	if req.Store == nil {
		return trawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema is not current",
			Remedy:  "run trawl calendar sync",
		}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema is not current",
			Remedy:  "run trawl calendar sync",
		}
	}
	if _, err := st.Status(ctx); err != nil {
		return trawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "archive schema could not be inspected",
			Remedy:  "run trawl calendar sync",
		}
	}
	return trawlkit.Check{ID: "schema", State: "ok"}
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

func searchHit(result archive.SearchResult) (trawlkit.Hit, error) {
	t, err := parseEventTime(result.Time)
	if err != nil {
		return trawlkit.Hit{}, err
	}
	title := strings.TrimSpace(result.Snippet)
	if title == "" {
		title = "Calendar event"
	}
	evidence := calendarSearchEvidence(result.Matches)
	anchorID := "summary"
	if len(result.Matches) > 0 {
		anchorID = result.Matches[0].Field
	} else {
		evidence = []trawlkit.EvidenceFragment{{Label: "Event preview", Field: &trawlkit.FieldEvidence{Name: "summary", Value: []trawlkit.TextRun{{Text: title}}}}}
	}
	return trawlkit.Hit{
		Ref:          result.Ref,
		ShortRef:     result.ShortRef,
		Time:         t,
		AnchorID:     anchorID,
		Summary:      trawlkit.ResultSummary{Title: title},
		Archive:      []trawlkit.ArchiveContext{calendarArchiveContext(result.Calendar)},
		Evidence:     evidence,
		AllDay:       result.AllDay,
		Availability: result.Availability,
	}, nil
}

func calendarArchiveContext(calendar string) trawlkit.ArchiveContext {
	calendar = strings.TrimSpace(calendar)
	if calendar == "" {
		return trawlkit.ArchiveContext{Kind: "calendar", Label: "In Calendar"}
	}
	return trawlkit.ArchiveContext{Kind: "calendar", Label: "In " + calendar}
}

func calendarSearchEvidence(matches []archive.SearchMatch) []trawlkit.EvidenceFragment {
	evidence := make([]trawlkit.EvidenceFragment, 0, len(matches))
	for _, match := range matches {
		label := map[string]string{
			"summary":     "Title",
			"description": "Description",
			"location":    "Location",
			"participant": "Participant",
		}[match.Field]
		runs := make([]trawlkit.TextRun, 0, len(match.Runs))
		for _, run := range match.Runs {
			runs = append(runs, trawlkit.TextRun{Text: run.Text, Matched: run.Matched})
		}
		evidence = append(evidence, trawlkit.EvidenceFragment{
			Label: label,
			Field: &trawlkit.FieldEvidence{Name: match.Field, Value: runs},
		})
	}
	return evidence
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
