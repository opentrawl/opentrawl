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
)

type MetadataCmd struct{}

func (c *MetadataCmd) Run(r *Runtime) error {
	return r.print(controlManifest())
}

type metadataManifest struct {
	control.Manifest
	ContractVersion int    `json:"contract_version"`
	Version         string `json:"version"`
}

func controlManifest() metadataManifest {
	m := control.NewManifest("clawdex", "Contacts", "clawdex")
	m.Description = "Local-first contact identity layer backed by markdown and git."
	m.Paths = control.Paths{
		DefaultConfig:   repo.ResolveConfigPath(""),
		DefaultDatabase: repo.DefaultConfig().RepoPath,
	}
	m.Capabilities = []string{"status", "doctor"}
	m.Commands = map[string]control.Command{
		"metadata": {Title: "Metadata", Argv: []string{"clawdex", "metadata", "--json"}, JSON: true},
		"status":   {Title: "Status", Argv: []string{"clawdex", "status", "--json"}, JSON: true},
		"doctor":   {Title: "Doctor", Argv: []string{"clawdex", "doctor", "--json"}, JSON: true},
	}
	return metadataManifest{Manifest: m, ContractVersion: 1, Version: Version}
}

type StatusCmd struct{}

func (c *StatusCmd) Run(r *Runtime) error {
	return r.print(r.controlStatus())
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
	Checks []DoctorCheck `json:"checks"`
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
	return DoctorReport{Checks: []DoctorCheck{config, contacts, idx}}
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
	for _, check := range report.Checks {
		line := fmt.Sprintf("%s: %s", check.ID, check.State)
		if check.Message != "" {
			line += " - " + check.Message
		}
		if check.Remedy != "" {
			line += " remedy: " + check.Remedy
		}
		if _, err := fmt.Fprintln(r.stdout, line); err != nil {
			return err
		}
	}
	return nil
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

func printStatusText(w io.Writer, status control.Status) error {
	if _, err := fmt.Fprintf(w, "Status: %s\n%s\n", status.State, status.Summary); err != nil {
		return err
	}
	if _, err := io.WriteString(w, "\nCounts:\n"); err != nil {
		return err
	}
	if len(status.Counts) == 0 {
		if _, err := io.WriteString(w, "  none\n"); err != nil {
			return err
		}
	}
	for _, count := range status.Counts {
		if _, err := fmt.Fprintf(w, "  %s: %d\n", count.Label, count.Value); err != nil {
			return err
		}
	}
	if status.ConfigPath != "" || status.DatabasePath != "" {
		if _, err := io.WriteString(w, "\nPaths:\n"); err != nil {
			return err
		}
		if status.ConfigPath != "" {
			if _, err := fmt.Fprintf(w, "  Config: %s\n", status.ConfigPath); err != nil {
				return err
			}
		}
		if status.DatabasePath != "" {
			if _, err := fmt.Fprintf(w, "  Contacts repo: %s\n", status.DatabasePath); err != nil {
				return err
			}
		}
	}
	if len(status.Warnings) > 0 {
		if _, err := io.WriteString(w, "\nWarnings:\n"); err != nil {
			return err
		}
		for _, warning := range status.Warnings {
			if _, err := fmt.Fprintf(w, "  - %s\n", warning); err != nil {
				return err
			}
		}
	}
	if len(status.Errors) > 0 {
		if _, err := io.WriteString(w, "\nErrors:\n"); err != nil {
			return err
		}
		for _, msg := range status.Errors {
			if _, err := fmt.Fprintf(w, "  - %s\n", msg); err != nil {
				return err
			}
		}
	}
	return nil
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
