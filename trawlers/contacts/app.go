package clawdex

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
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
	cfg Config
}

type Crawler = App

var (
	_ trawlkit.Crawler          = (*App)(nil)
	_ trawlkit.Searcher         = (*App)(nil)
	_ trawlkit.WhoMatcher       = (*App)(nil)
	_ trawlkit.Opener           = (*App)(nil)
	_ trawlkit.ContactExporter  = (*App)(nil)
	_ trawlkit.ShortRefProvider = (*App)(nil)
)

func New() *App {
	return &App{}
}

func (a *App) Info() trawlkit.Info {
	return trawlkit.Info{
		ID:          archive.AppID,
		Surface:     "contacts",
		DisplayName: archive.DisplayName,
		Description: "People merged from your other sources",
		Config:      &a.cfg,
		Privacy: control.Privacy{
			LocalOnlyScopes: []string{"contacts", "sqlite", "contact-search", "contact-export"},
		},
	}
}

func (a *App) Verbs() []trawlkit.Verb {
	return []trawlkit.Verb{
		personListVerb(),
		personShowVerb(),
		personAnnotateVerb(),
		importVerb(a),
		importLegacyVerb(),
		syncAppleVerb(),
		syncGoogleVerb(a),
		exportVCardVerb(),
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

func (a *App) Doctor(ctx context.Context, req *trawlkit.Request) (*trawlkit.Doctor, error) {
	return &trawlkit.Doctor{Checks: []trawlkit.Check{
		checkArchivePresent(req),
		checkArchiveSchema(ctx, req),
	}}, nil
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
		hits = append(hits, trawlkit.Hit{
			Ref:      result.Ref,
			Time:     result.Time,
			Who:      result.Who,
			Snippet:  result.Snippet,
			ShortRef: result.ShortRef,
		})
	}
	return trawlkit.SearchResult{
		Results:      hits,
		TotalMatches: total,
		Truncated:    q.Limit > 0 && len(results) < total,
	}, nil
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
			LastSeen:    candidate.LastSeen,
		})
	}
	return out, nil
}

func (a *App) Open(ctx context.Context, req *trawlkit.Request, ref string) error {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolved, err := resolveOpenRef(ctx, req, ref)
	if err != nil {
		return err
	}
	person, err := st.FindPerson(ctx, resolved)
	if err != nil {
		return err
	}
	return writeOpenPerson(req, person)
}

func (a *App) ContactExport(ctx context.Context, req *trawlkit.Request) (*control.ContactExport, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	people, err := st.People(ctx)
	if err != nil {
		return nil, err
	}
	contacts := make([]control.Contact, 0, len(people))
	for _, person := range people {
		phones := cleanPhones(person.Phones)
		if strings.TrimSpace(person.Name) == "" || len(phones) == 0 {
			continue
		}
		contacts = append(contacts, control.Contact{
			DisplayName:  strings.TrimSpace(person.Name),
			PhoneNumbers: phones,
		})
	}
	return &control.ContactExport{Contacts: contacts}, nil
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

func checkArchivePresent(req *trawlkit.Request) trawlkit.Check {
	if req.Store == nil {
		return trawlkit.Check{
			ID:      "archive",
			State:   "fail",
			Message: "contacts archive has not been created",
			Remedy:  "run trawl contacts import-legacy or import a source",
		}
	}
	return trawlkit.Check{ID: "archive", State: "ok"}
}

func checkArchiveSchema(ctx context.Context, req *trawlkit.Request) trawlkit.Check {
	if req.Store == nil {
		return trawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "contacts archive schema is not current",
			Remedy:  "run trawl contacts import-legacy or import a source",
		}
	}
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return trawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "contacts archive schema is not current",
			Remedy:  "run trawl contacts import-legacy or import a source",
		}
	}
	if _, err := st.Status(ctx); err != nil {
		return trawlkit.Check{
			ID:      "schema",
			State:   "fail",
			Message: "contacts archive could not be inspected",
			Remedy:  "run trawl contacts import-legacy or import a source",
		}
	}
	return trawlkit.Check{ID: "schema", State: "ok"}
}

func peopleStatusSummary(count int) string {
	if count == 1 {
		return "Contacts archive has 1 person."
	}
	return fmt.Sprintf("Contacts archive has %s people.", formatCount(count))
}

func cleanPhones(values []model.ContactValue) []string {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		phone := strings.TrimSpace(value.Value)
		if phone == "" || seen[phone] {
			continue
		}
		seen[phone] = true
		out = append(out, phone)
	}
	return out
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
