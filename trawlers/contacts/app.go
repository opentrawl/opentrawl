package clawdex

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
)

const appID = archive.AppID

type Config struct {
	Google GoogleConfig `toml:"google" json:"google"`
}

type GoogleConfig struct {
	DefaultAccount string `toml:"default_account" json:"default_account"`
}

type App struct {
	cfg       Config
	readApple func(context.Context) ([]apple.Contact, error)
}

type Crawler = App

var (
	_ trawlkit.Crawler          = (*App)(nil)
	_ trawlkit.Syncer           = (*App)(nil)
	_ trawlkit.Searcher         = (*App)(nil)
	_ trawlkit.WhoMatcher       = (*App)(nil)
	_ trawlkit.Opener           = (*App)(nil)
	_ trawlkit.ShortRefProvider = (*App)(nil)
)

func New() *App {
	return &App{readApple: apple.ReadSystem}
}

func (a *App) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          archive.AppID,
		Surface:     "contacts",
		DisplayName: archive.DisplayName,
		Headlines:   []string{"people"},
		Config:      &a.cfg,
		Privacy: control.Privacy{
			LocalOnlyScopes: []string{"contacts", "sqlite", "contact-search"},
		},
	}
}

func (a *App) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		peopleReconcileVerb(a),
		personListVerb(),
		personShowVerb(),
		personAnnotateVerb(),
		importVerb(a),
		importLegacyVerb(),
		syncGoogleVerb(a),
	}
}

func (a *App) Status(ctx context.Context, req *trawlkit.Request) (*control.Status, error) {
	status := control.NewStatus(appID, "Contacts archive has not been created.")
	status.State = "missing"
	status.DatabasePath = req.Paths.Archive
	status.Counts = []control.Count{control.NewCount("people", "people", 0)}
	if req.Store == nil {
		return &status, nil
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		status.State = "error"
		status.Summary = "Contacts archive cannot be read."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	archiveStatus, err := st.Status(ctx)
	if err != nil {
		status.State = "error"
		status.Summary = "Contacts archive cannot be inspected."
		status.Errors = []string{err.Error()}
		return &status, nil
	}
	status.DatabasePath = archiveStatus.ArchivePath
	status.DatabaseBytes = archiveStatus.ArchiveBytes
	status.LastSyncAt = formatTime(archiveStatus.UpdatedAt)
	status.Counts = []control.Count{
		control.NewCount("people", "people", archiveStatus.People),
		control.NewCount("sources", "sources", archiveStatus.Sources),
	}
	if archiveStatus.Notes > 0 {
		status.Counts = append(status.Counts, control.NewCount("notes", "notes", archiveStatus.Notes))
	}
	if archiveStatus.People == 0 {
		status.State = "empty"
		status.Summary = "Contacts archive has no people."
		return &status, nil
	}
	status.State = "ok"
	status.Summary = peopleStatusSummary(int(archiveStatus.People))
	return &status, nil
}

func (a *App) Search(ctx context.Context, req *trawlkit.Request, q trawlkit.Query) (trawlkit.SearchResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.SearchResult{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	query := strings.Join(strings.Fields(q.Text), " ")
	if query == "" && q.WhoResolved != nil {
		query = strings.Join(strings.Fields(q.WhoResolved.Who), " ")
	}
	if query == "" {
		query = strings.Join(strings.Fields(q.Who), " ")
	}
	if query == "" {
		return trawlkit.SearchResult{Results: []trawlkit.Hit{}}, nil
	}
	results, total, err := st.Search(ctx, query, archive.SearchOptions{Limit: q.Limit, After: q.After, Before: q.Before})
	if err != nil {
		return trawlkit.SearchResult{}, err
	}
	hits := make([]trawlkit.Hit, 0, len(results))
	for _, result := range results {
		title := strings.TrimSpace(result.Who)
		if title == "" {
			title = "Contact"
		}
		hits = append(hits, trawlkit.Hit{
			Ref:      result.Ref,
			Time:     result.Time,
			ShortRef: result.ShortRef,
			AnchorID: result.AnchorID,
			Summary:  trawlkit.ResultSummary{Title: title},
			Archive:  []trawlkit.ArchiveContext{{Kind: "contacts", Label: "In Contacts"}},
			Evidence: contactSearchEvidence(result.Matches),
		})
	}
	return trawlkit.SearchResult{
		Results:      hits,
		TotalMatches: total,
		Truncated:    q.Limit > 0 && len(results) < total,
	}, nil
}

func contactSearchEvidence(matches []archive.SearchMatch) []trawlkit.EvidenceFragment {
	labels := map[string]string{
		"name": "Name", "sort_name": "Sort name", "annotation": "Annotation", "body": "Contact note",
		"identifier": "Identifier", "aka": "Also known as", "tag": "Tag", "email": "Email",
		"phone": "Phone", "address": "Address", "account": "Account", "note_kind": "Note kind",
		"source_name": "Source name", "note_source": "Note source", "note_body": "Note body", "note_topic": "Note topic",
	}
	evidence := make([]trawlkit.EvidenceFragment, 0, len(matches))
	for _, match := range matches {
		runs := make([]trawlkit.TextRun, 0, len(match.Runs))
		for _, run := range match.Runs {
			runs = append(runs, trawlkit.TextRun{Text: run.Text, Matched: run.Matched})
		}
		evidence = append(evidence, trawlkit.EvidenceFragment{
			Label: labels[match.Field],
			Field: &trawlkit.FieldEvidence{Name: match.Field, Value: runs},
		})
	}
	return evidence
}

func (a *App) Who(ctx context.Context, req *trawlkit.Request, person string) ([]whomatch.Candidate, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	candidates, err := st.ResolvePeople(ctx, person)
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whomatch.Candidate{
			Who:         candidate.Who,
			Identifiers: append([]string(nil), candidate.Identifiers...),
			Aliases:     append([]string(nil), candidate.Aliases...),
			LastSeen:    candidate.LastSeen,
		})
	}
	return out, nil
}

func (a *App) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	value, err := a.loadOpenPerson(ctx, req, ref)
	if err != nil {
		return err
	}
	return writeOpenPerson(req, value.person)
}

func (a *App) loadOpenPerson(ctx context.Context, req *trawlkit.Request, ref string) (openValue, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return openValue{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolved, err := resolveOpenRef(ctx, req, ref)
	if err != nil {
		return openValue{}, err
	}
	person, err := st.FindPerson(ctx, resolved)
	if err != nil {
		return openValue{}, err
	}
	notes, err := st.Notes(ctx, person.ID)
	if err != nil {
		return openValue{}, err
	}
	return openValue{ref: archive.PersonRef(person.ID), person: person, notes: notes}, nil
}

func (a *App) ShortRefRecords(ctx context.Context, req *trawlkit.Request) ([]trawlkit.ShortRefRecord, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if errors.Is(err, archive.ErrSchemaOutdated) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return st.ShortRefRecords(ctx)
}

func resolveOpenRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		if id, ok := archive.PersonIDFromRef(ref); ok {
			return id, nil
		}
		return "", usageError(fmt.Errorf("invalid contacts ref %q", ref))
	}
	if trawlkit.ValidShortRef(ref) {
		matches, err := req.ResolveShortRef(ctx, ref)
		if errors.Is(err, trawlkit.ErrUnknownShortRef) {
			return ref, nil
		}
		if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
			return "", usageError(fmt.Errorf("short ref %q is ambiguous", ref))
		}
		if err != nil {
			return "", err
		}
		if len(matches) == 1 {
			if id, ok := archive.PersonIDFromRef(matches[0]); ok {
				return id, nil
			}
		}
	}
	return ref, nil
}

func peopleStatusSummary(count int) string {
	if count == 1 {
		return "Contacts archive has 1 person."
	}
	return fmt.Sprintf("Contacts archive has %s people.", formatCount(count))
}

func archiveErr(err error) error {
	return err
}

func usageError(err error) error {
	return output.UsageError{Err: err}
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
