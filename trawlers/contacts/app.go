package clawdex

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/index"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/whomatch"
)

const appID = "contacts"

type App struct{}

type Crawler = App

var (
	_ crawlkit.Crawler         = (*App)(nil)
	_ crawlkit.Searcher        = (*App)(nil)
	_ crawlkit.WhoMatcher      = (*App)(nil)
	_ crawlkit.Opener          = (*App)(nil)
	_ crawlkit.ContactExporter = (*App)(nil)
)

func New() *App {
	return &App{}
}

func (a *App) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          appID,
		Surface:     appID,
		Aliases:     []string{"clawdex"},
		DisplayName: "Contacts",
		Description: "Local-first contact identity layer.",
		DefaultPaths: crawlkit.Paths{
			Archive: repo.DefaultConfig().RepoPath,
			Config:  repo.ResolveConfigPath(""),
			Logs:    repo.DefaultLogDir(),
		},
		Privacy: control.Privacy{
			LocalOnlyScopes: []string{"contacts", "markdown", "git"},
		},
	}
}

func (a *App) Verbs() []crawlkit.Verb {
	return []crawlkit.Verb{
		{Name: "status", Store: crawlkit.StoreNone},
		{Name: "doctor", Store: crawlkit.StoreNone},
		{Name: "search", Store: crawlkit.StoreNone},
		{Name: "who", Store: crawlkit.StoreNone},
		{Name: "open", Store: crawlkit.StoreNone},
		{Name: "contacts_export", Store: crawlkit.StoreNone},
		initVerb(),
		configVerb(),
		personListVerb(),
		personShowVerb(),
		importVerb(),
		repairVerb(),
		syncAppleVerb(),
		syncGoogleVerb(),
		exportVCardVerb(),
		gitVerb(),
	}
}

func (a *App) Status(_ context.Context, req *crawlkit.Request) (*control.Status, error) {
	rt, err := newRuntime(req)
	if err != nil {
		return nil, err
	}
	return rt.status(), nil
}

func (a *App) Doctor(_ context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	rt, err := newRuntime(req)
	if err != nil {
		return &crawlkit.Doctor{Checks: []crawlkit.Check{{
			ID:      "config",
			State:   "fail",
			Message: err.Error(),
			Remedy:  "check the contacts config file",
		}}}, nil
	}
	return &crawlkit.Doctor{Checks: []crawlkit.Check{
		rt.configCheck(),
		rt.contactsRepoCheck(),
		rt.indexCheck(),
	}}, nil
}

func (a *App) Search(_ context.Context, req *crawlkit.Request, q crawlkit.Query) (crawlkit.SearchResult, error) {
	rt, err := newRuntime(req)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	query := strings.Join(strings.Fields(q.Text), " ")
	if query == "" && q.WhoResolved != nil {
		query = strings.Join(strings.Fields(q.WhoResolved.Who), " ")
	}
	if query == "" {
		query = strings.Join(strings.Fields(q.Who), " ")
	}
	if query == "" {
		return crawlkit.SearchResult{Results: []crawlkit.Hit{}}, nil
	}
	hits, err := rt.store.Search(query)
	if err != nil {
		return crawlkit.SearchResult{}, err
	}
	total := len(hits)
	if q.Limit > 0 && len(hits) > q.Limit {
		hits = hits[:q.Limit]
	}
	result := crawlkit.SearchResult{
		Results:      make([]crawlkit.Hit, 0, len(hits)),
		TotalMatches: total,
		Truncated:    total > len(hits),
	}
	for _, hit := range hits {
		if !withinRange(hit.Timestamp, q.After, q.Before) {
			continue
		}
		refID := firstText(hit.PersonID, hit.ID)
		if refID == "" {
			continue
		}
		result.Results = append(result.Results, crawlkit.Hit{
			Ref:     "contacts:person/" + refID,
			Time:    hit.Timestamp,
			Who:     hit.Name,
			Snippet: hit.Snippet,
		})
	}
	if result.TotalMatches < len(result.Results) {
		result.TotalMatches = len(result.Results)
	}
	return result, nil
}

func (a *App) Who(_ context.Context, req *crawlkit.Request, person string) ([]whomatch.Candidate, error) {
	rt, err := newRuntime(req)
	if err != nil {
		return nil, err
	}
	candidates, err := rt.store.ResolvePeople(person)
	if err != nil {
		return nil, err
	}
	out := make([]whomatch.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whomatch.Candidate{
			Who:         candidate.Who,
			Identifiers: append([]string(nil), candidate.Identifiers...),
			LastSeen:    parseTime(candidate.LastSeen),
		})
	}
	return out, nil
}

func (a *App) Open(_ context.Context, req *crawlkit.Request, ref string) error {
	rt, err := newRuntime(req)
	if err != nil {
		return err
	}
	person, err := rt.store.FindPerson(contactQuery(ref))
	if err != nil {
		return err
	}
	return writePerson(req, person)
}

func (a *App) ContactExport(_ context.Context, req *crawlkit.Request) (*control.ContactExport, error) {
	rt, err := newRuntime(req)
	if err != nil {
		return nil, err
	}
	people, err := rt.store.People()
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

type appRuntime struct {
	configPath string
	cfg        repo.Config
	repo       repo.Repo
	store      index.Store
}

func newRuntime(req *crawlkit.Request) (appRuntime, error) {
	paths := crawlkit.Paths{}
	if req != nil {
		paths = req.Paths
	}
	if strings.TrimSpace(paths.Config) == "" {
		paths.Config = repo.ResolveConfigPath("")
	}
	cfg, err := repo.LoadConfig(paths.Config)
	if err != nil {
		return appRuntime{}, err
	}
	repoPath, err := repo.ResolveRepoPath("", cfg)
	if err != nil {
		repoPath = cfg.RepoPath
	}
	if strings.TrimSpace(repoPath) == "" {
		return appRuntime{}, errors.New("contacts repo path is required")
	}
	dataRepo := repo.Open(repoPath, cfg)
	return appRuntime{
		configPath: paths.Config,
		cfg:        cfg,
		repo:       dataRepo,
		store:      index.New(dataRepo),
	}, nil
}

func (rt appRuntime) status() *control.Status {
	status := control.NewStatus(appID, "contacts repo not initialised")
	status.ConfigPath = rt.configPath
	status.DatabasePath = rt.repo.Path
	status.Counts = []control.Count{control.NewCount("people", "people", 0)}
	if err := rt.repo.Require(); err != nil {
		if peopleDirMissing(rt.repo.Path) {
			status.State = "missing"
			return &status
		}
		status.State = "error"
		status.Summary = "contacts repo cannot be read"
		status.Errors = []string{err.Error()}
		return &status
	}
	people, err := rt.store.People()
	if err != nil {
		status.State = "error"
		status.Summary = "contacts repo has errors"
		status.Errors = []string{err.Error()}
		return &status
	}
	problems, err := rt.personRepairProblemCount()
	if err != nil {
		status.State = "error"
		status.Summary = "contacts repo has errors"
		status.Errors = []string{err.Error()}
		return &status
	}
	status.Counts = statusCounts(people)
	if problems > 0 {
		status.State = "error"
		status.Summary = personRepairSummary(problems)
		status.Errors = []string{status.Summary}
		return &status
	}
	if len(people) == 0 {
		status.State = "empty"
		status.Summary = "contacts repo has no people yet"
		return &status
	}
	status.State = "ok"
	status.Summary = peopleStatusSummary(len(people))
	return &status
}

func (rt appRuntime) configCheck() crawlkit.Check {
	if _, err := os.Stat(rt.configPath); errors.Is(err, os.ErrNotExist) {
		return crawlkit.Check{ID: "config", State: "ok"}
	} else if err != nil {
		return crawlkit.Check{ID: "config", State: "fail", Message: "cannot read config", Remedy: "check " + rt.configPath}
	}
	if _, err := repo.LoadConfig(rt.configPath); err != nil {
		return crawlkit.Check{ID: "config", State: "fail", Message: err.Error(), Remedy: "check " + rt.configPath}
	}
	return crawlkit.Check{ID: "config", State: "ok"}
}

func (rt appRuntime) contactsRepoCheck() crawlkit.Check {
	if err := rt.repo.Require(); err != nil {
		return crawlkit.Check{ID: "contacts_repo", State: "missing", Message: "contacts repo not initialised", Remedy: "run trawl contacts init " + rt.repo.Path}
	}
	if _, err := os.Stat(filepath.Join(rt.repo.Path, ".git")); err != nil {
		return crawlkit.Check{ID: "contacts_repo", State: "fail", Message: "contacts repo is not a git repo", Remedy: "run trawl contacts init " + rt.repo.Path}
	}
	if _, err := rt.store.People(); err != nil {
		return crawlkit.Check{ID: "contacts_repo", State: "fail", Message: err.Error(), Remedy: "run trawl contacts repair"}
	}
	if problems, err := rt.personRepairProblemCount(); err != nil {
		return crawlkit.Check{ID: "contacts_repo", State: "fail", Message: err.Error(), Remedy: "run trawl contacts repair"}
	} else if problems > 0 {
		return crawlkit.Check{ID: "contacts_repo", State: "fail", Message: personRepairSummary(problems), Remedy: "run trawl contacts repair"}
	}
	return crawlkit.Check{ID: "contacts_repo", State: "ok"}
}

func (rt appRuntime) indexCheck() crawlkit.Check {
	people, err := rt.store.People()
	if err != nil {
		return crawlkit.Check{ID: "index", State: "fail", Message: err.Error(), Remedy: "fix contacts_repo first"}
	}
	status, err := rt.store.IndexStatus()
	if err != nil {
		return crawlkit.Check{ID: "index", State: "fail", Message: err.Error(), Remedy: "fix contacts_repo first"}
	}
	if status.People != len(people) {
		return crawlkit.Check{ID: "index", State: "fail", Message: fmt.Sprintf("index has %s people, markdown has %s", strconv.Itoa(status.People), strconv.Itoa(len(people))), Remedy: "run trawl contacts repair"}
	}
	return crawlkit.Check{ID: "index", State: "ok"}
}

func statusCounts(people []model.Person) []control.Count {
	counts := []control.Count{control.NewCount("people", "people", int64(len(people)))}
	if len(people) > 0 {
		counts = append(counts, control.NewCount("sources", "sources", int64(distinctSourceCount(people))))
	}
	return counts
}

func distinctSourceCount(people []model.Person) int {
	seen := map[string]bool{}
	for _, person := range people {
		for source := range person.Sources {
			if strings.TrimSpace(source) != "" {
				seen[source] = true
			}
		}
	}
	return len(seen)
}

func peopleStatusSummary(count int) string {
	if count == 1 {
		return "contacts repo has 1 person"
	}
	return fmt.Sprintf("contacts repo has %s people", strconv.Itoa(count))
}

func peopleDirMissing(repoPath string) bool {
	if strings.TrimSpace(repoPath) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(repoPath, "people"))
	return errors.Is(err, os.ErrNotExist)
}

func contactQuery(ref string) string {
	ref = strings.TrimSpace(ref)
	if _, path, ok := strings.Cut(ref, ":"); ok {
		ref = path
	}
	for _, prefix := range []string{"person/", "people/"} {
		if strings.HasPrefix(ref, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(ref, prefix))
		}
	}
	return ref
}

func withinRange(t, after, before time.Time) bool {
	if t.IsZero() {
		return true
	}
	if !after.IsZero() && t.Before(after) {
		return false
	}
	if !before.IsZero() && !t.Before(before) {
		return false
	}
	return true
}

func parseTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
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
