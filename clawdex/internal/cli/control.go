package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/openclaw/clawdex/internal/index"
	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
)

type MetadataCmd struct{}

func (c *MetadataCmd) Run(r *Runtime) error {
	return r.print(controlManifest())
}

func controlManifest() control.Manifest {
	m := control.NewManifest("clawdex", "Contacts", "clawdex")
	m.Version = Version
	m.Description = "Local-first contact identity layer backed by markdown and git."
	m.Paths = control.Paths{
		DefaultConfig:   repo.ResolveConfigPath(""),
		DefaultDatabase: repo.DefaultConfig().RepoPath,
		DefaultLogs:     repo.DefaultLogDir(),
	}
	m.Capabilities = []string{"status", "doctor", "who", "verbose_logs"}
	m.Commands = map[string]control.Command{
		"metadata": {Title: "Metadata", Argv: []string{"clawdex", "metadata", "--json"}, JSON: true},
		"status":   {Title: "Status", Argv: []string{"clawdex", "status", "--json"}, JSON: true},
		"doctor":   {Title: "Doctor", Argv: []string{"clawdex", "doctor", "--json"}, JSON: true},
		"who":      {Title: "Who", Argv: []string{"clawdex", "who", "QUERY", "--json"}, JSON: true},
	}
	return m
}

type StatusCmd struct{}

func (c *StatusCmd) Run(r *Runtime) error {
	status := r.controlStatus()
	if r.root.JSON {
		return r.print(r.statusOutput(status))
	}
	return printStatusText(r.stdout, status, r.renderLogTail())
}

type statusOutput struct {
	control.Status
	LastRun     *logRunEnvelope   `json:"last_run,omitempty"`
	RecentError *logErrorEnvelope `json:"recent_error,omitempty"`
}

func (r *Runtime) statusOutput(status control.Status) statusOutput {
	lastRun, recentError := r.logTailEnvelope()
	return statusOutput{Status: status, LastRun: lastRun, RecentError: recentError}
}

func (r *Runtime) controlStatus() control.Status {
	if err := r.repo.Require(); err != nil {
		status := r.newControlStatus("contacts repo not initialised")
		status.Counts = []control.Count{control.NewCount("people", "people", 0)}
		if peopleDirMissing(r.repo.Path) {
			status.State = "missing"
			return status
		}
		status.State = "error"
		status.Summary = "contacts repo cannot be read"
		status.Errors = []string{err.Error()}
		return status
	}

	people, err := r.readOnlyStore().People()
	if err != nil {
		status := r.newControlStatus("contacts repo has errors")
		status.State = "error"
		status.Errors = []string{err.Error()}
		return status
	}
	repairProblems, err := r.personRepairProblemCount()
	if err != nil {
		status := r.newControlStatus("contacts repo has errors")
		status.State = "error"
		status.Errors = []string{err.Error()}
		return status
	}
	if repairProblems > 0 {
		status := r.newControlStatus(personRepairSummary(repairProblems))
		status.State = "error"
		status.Counts = statusCounts(people)
		status.Errors = []string{personRepairSummary(repairProblems)}
		return status
	}
	if len(people) == 0 {
		status := r.newControlStatus("contacts repo has no people yet")
		status.State = "empty"
		status.Counts = []control.Count{control.NewCount("people", "people", 0)}
		return status
	}

	status := r.newControlStatus(peopleStatusSummary(len(people)))
	status.State = "ok"
	status.Counts = statusCounts(people)
	return status
}

func (r *Runtime) newControlStatus(summary string) control.Status {
	status := control.NewStatus("clawdex", summary)
	status.ConfigPath = r.configPath
	status.DatabasePath = r.repo.Path
	return status
}

type DoctorReport struct {
	Checks      []DoctorCheck     `json:"checks"`
	LastRun     *logRunEnvelope   `json:"last_run,omitempty"`
	RecentError *logErrorEnvelope `json:"recent_error,omitempty"`
}

type DoctorCheck struct {
	ID      string `json:"id"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

func (r *Runtime) doctorReport() DoctorReport {
	config := r.configDoctorCheck()
	contacts, people, contactsOK, contactsMissing := r.contactsRepoDoctorCheck()
	idx := r.indexDoctorCheck(people, contactsOK, contactsMissing)
	lastRun, recentError := r.logTailEnvelope()
	return DoctorReport{Checks: []DoctorCheck{config, contacts, idx}, LastRun: lastRun, RecentError: recentError}
}

func (r *Runtime) configDoctorCheck() DoctorCheck {
	if _, err := os.Stat(r.configPath); errors.Is(err, os.ErrNotExist) {
		return okCheck("config")
	} else if err != nil {
		return failCheck("config", fmt.Sprintf("cannot read config at %s: %v", r.configPath, err), fmt.Sprintf("check %s is valid TOML and readable", r.configPath))
	}
	if _, err := repo.LoadConfig(r.configPath); err != nil {
		return failCheck("config", fmt.Sprintf("config at %s is invalid: %v", r.configPath, err), fmt.Sprintf("check %s is valid TOML and readable", r.configPath))
	}
	return okCheck("config")
}

func (r *Runtime) contactsRepoDoctorCheck() (DoctorCheck, []model.Person, bool, bool) {
	if err := r.repo.Require(); err != nil {
		return failCheck("contacts_repo", fmt.Sprintf("contacts repo not initialised at %s", r.repo.Path), fmt.Sprintf("run clawdex init %s", r.repo.Path)), nil, false, true
	}
	if _, err := os.Stat(filepath.Join(r.repo.Path, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return failCheck("contacts_repo", fmt.Sprintf("contacts repo at %s is not a git repo", r.repo.Path), fmt.Sprintf("run clawdex init %s", r.repo.Path)), nil, false, false
		}
		return failCheck("contacts_repo", fmt.Sprintf("cannot inspect git repo at %s: %v", r.repo.Path, err), fmt.Sprintf("check %s is readable or run clawdex init %s", r.repo.Path, r.repo.Path)), nil, false, false
	}
	people, err := r.readOnlyStore().People()
	if err != nil {
		return failCheck("contacts_repo", fmt.Sprintf("contacts repo cannot be read: %v", err), "run clawdex doctor --repair"), nil, false, false
	}
	repairProblems, err := r.personRepairProblemCount()
	if err != nil {
		return failCheck("contacts_repo", fmt.Sprintf("person markdown parse failed: %v", err), "run clawdex doctor --repair"), people, false, false
	}
	if repairProblems > 0 {
		return failCheck("contacts_repo", personRepairSummary(repairProblems), "run clawdex doctor --repair"), people, false, false
	}
	return okCheck("contacts_repo"), people, true, false
}

func (r *Runtime) indexDoctorCheck(people []model.Person, contactsOK, contactsMissing bool) DoctorCheck {
	if !contactsOK {
		if contactsMissing {
			return failCheck("index", "cannot check index without a contacts repo", "fix contacts_repo first")
		}
		return failCheck("index", "cannot check index until contacts_repo passes", "fix contacts_repo first")
	}
	status, err := r.readOnlyStore().IndexStatus()
	if err != nil {
		return failCheck("index", fmt.Sprintf("index database cannot be opened: %v", err), "fix contacts_repo first")
	}
	if status.People != len(people) {
		return failCheck("index", fmt.Sprintf("index has %d people, markdown has %d", status.People, len(people)), "rerun clawdex doctor")
	}
	return okCheck("index")
}

func (r *Runtime) printDoctorReport(report DoctorReport) error {
	if r.root.JSON {
		return r.print(report)
	}
	return render.WriteDoctor(r.stdout, renderDoctorChecks(report), r.renderLogTail())
}

func (r *Runtime) readOnlyStore() index.Store {
	store := r.store
	store.Repo.Config.Repair.AutoRepair = false
	return store
}

func (r *Runtime) personRepairProblemCount() (int, error) {
	entries, err := os.ReadDir(r.repo.PeopleDir())
	if err != nil {
		return 0, err
	}
	var problems int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(r.repo.PeopleDir(), entry.Name(), "person.md")
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return problems, err
		}
		if _, report, err := markdown.ReadPerson(path); err != nil {
			return problems, err
		} else if report.Needed {
			problems++
		}
	}
	return problems, nil
}

func peopleDirMissing(repoPath string) bool {
	if strings.TrimSpace(repoPath) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(repoPath, "people"))
	return errors.Is(err, os.ErrNotExist)
}

func statusCounts(people []model.Person) []control.Count {
	counts := []control.Count{control.NewCount("people", "people", int64(len(people)))}
	if len(people) > 0 {
		counts = append(counts, control.NewCount("sources", "sources", int64(distinctSourceCount(people))))
	}
	return counts
}

func printStatusText(w io.Writer, status control.Status, tail render.LogTail) error {
	return render.WriteStatus(w, renderStatus(status, tail))
}

func renderStatus(status control.Status, tail render.LogTail) render.Status {
	return render.Status{
		State:    renderStatusState(status.State),
		Summary:  humanSentence(status.Summary),
		Sections: statusSections(status),
		Warnings: cleanMessages(status.Warnings),
		Errors:   cleanMessages(status.Errors),
		Log:      tail,
	}
}

func statusSections(status control.Status) []render.Section {
	var sections []render.Section
	if len(status.Counts) > 0 {
		fields := make([]render.Field, 0, len(status.Counts))
		for _, count := range status.Counts {
			fields = append(fields, render.Field{
				Label: humanLabel(firstNonEmpty(count.Label, count.ID)),
				Value: fmt.Sprint(count.Value),
			})
		}
		sections = append(sections, render.Section{Title: "Contacts", Fields: fields})
	}
	return sections
}

func renderStatusState(state string) render.StatusState {
	switch strings.TrimSpace(state) {
	case "ok":
		return render.StatusOK
	case "stale":
		return render.StatusStale
	case "empty":
		return render.StatusEmpty
	case "error":
		return render.StatusError
	case "missing":
		return render.StatusMissing
	default:
		return render.StatusUnknown
	}
}

func renderDoctorChecks(report DoctorReport) []render.Check {
	checks := make([]render.Check, 0, len(report.Checks))
	for _, check := range report.Checks {
		checks = append(checks, render.Check{
			Name:    doctorCheckName(check.ID),
			State:   doctorCheckState(check),
			Message: humanDoctorMessage(check),
			Remedy:  humanDoctorRemedy(check),
		})
	}
	return checks
}

func doctorCheckName(id string) string {
	switch strings.TrimSpace(id) {
	case "config":
		return "Config"
	case "contacts_repo":
		return "Contacts repo"
	case "index":
		return "Index"
	default:
		return humanLabel(id)
	}
}

func doctorCheckState(check DoctorCheck) render.CheckState {
	switch strings.TrimSpace(check.State) {
	case "ok":
		return render.CheckOK
	case "empty":
		return render.CheckEmpty
	case "missing":
		return render.CheckMissing
	case "fail":
		if doctorFailureIsMissing(check) {
			return render.CheckMissing
		}
		return render.CheckFail
	default:
		return render.CheckFail
	}
}

func doctorFailureIsMissing(check DoctorCheck) bool {
	message := strings.ToLower(check.Message)
	return strings.Contains(message, "not initialised") ||
		strings.Contains(message, "without a contacts repo") ||
		strings.Contains(message, "not a git repo")
}

func humanDoctorMessage(check DoctorCheck) string {
	message := strings.TrimSpace(check.Message)
	switch strings.TrimSpace(check.ID) {
	case "config":
		if strings.Contains(message, "cannot read config") {
			return "config file cannot be read"
		}
		if strings.Contains(message, "is invalid") {
			return "config file is invalid"
		}
	case "contacts_repo":
		switch {
		case strings.Contains(message, "not initialised"):
			return "contacts repo not initialised"
		case strings.Contains(message, "is not a git repo"):
			return "contacts repo is not a git repo"
		case strings.Contains(message, "cannot inspect git repo"):
			return "contacts repo cannot be inspected"
		}
	}
	return strings.ReplaceAll(strings.TrimSpace(message), "contacts_repo", "contacts repo")
}

func humanDoctorRemedy(check DoctorCheck) string {
	remedy := strings.TrimSpace(check.Remedy)
	if remedy == "" || remedy == "fix contacts_repo first" {
		return ""
	}
	switch {
	case strings.HasPrefix(remedy, "run clawdex init "):
		return "run clawdex init"
	case strings.HasPrefix(remedy, "check ") && strings.Contains(remedy, "valid TOML"):
		return "check the config file is valid TOML and readable"
	case strings.HasPrefix(remedy, "check ") && strings.Contains(remedy, "run clawdex init"):
		return "check the contacts repo is readable or run clawdex init"
	}
	return strings.ReplaceAll(remedy, "contacts_repo", "contacts repo")
}

func cleanMessages(messages []string) []string {
	cleaned := make([]string, 0, len(messages))
	for _, message := range messages {
		message = strings.TrimSpace(message)
		if message != "" {
			cleaned = append(cleaned, strings.ReplaceAll(message, "contacts_repo", "contacts repo"))
		}
	}
	return cleaned
}

func humanSentence(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if value[0] >= 'a' && value[0] <= 'z' {
		value = string(value[0]-('a'-'A')) + value[1:]
	}
	switch value[len(value)-1] {
	case '.', '!', '?':
		return value
	default:
		return value + "."
	}
}

func humanLabel(value string) string {
	parts := strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "_", " "))
	for i, part := range parts {
		if part == "" {
			continue
		}
		if part[0] >= 'a' && part[0] <= 'z' {
			parts[i] = string(part[0]-('a'-'A')) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func distinctSourceCount(people []model.Person) int {
	sources := map[string]bool{}
	for _, person := range people {
		for source := range person.Sources {
			source = strings.TrimSpace(source)
			if source != "" {
				sources[source] = true
			}
		}
	}
	return len(sources)
}

func personRepairSummary(count int) string {
	if count == 1 {
		return "1 person markdown file needs repair"
	}
	return fmt.Sprintf("%d person markdown files need repair", count)
}

func peopleStatusSummary(count int) string {
	if count == 1 {
		return "1 person, initialised"
	}
	return fmt.Sprintf("%d people, initialised", count)
}

func okCheck(id string) DoctorCheck {
	return DoctorCheck{ID: id, State: "ok"}
}

func failCheck(id, message, remedy string) DoctorCheck {
	return DoctorCheck{ID: id, State: "fail", Message: message, Remedy: remedy}
}
