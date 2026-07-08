package clawdex

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	internalcli "github.com/openclaw/clawdex/internal/cli"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/crawlkit"
	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/output"
	"github.com/openclaw/crawlkit/whomatch"
)

type Crawler struct{}

func New() *Crawler {
	return &Crawler{}
}

func (c *Crawler) Info() crawlkit.Info {
	return crawlkit.Info{
		ID:          "contacts",
		Surface:     "contacts",
		Aliases:     []string{"clawdex"},
		DisplayName: "Contacts",
		Description: "Local-first contact identity layer.",
		DefaultPaths: crawlkit.Paths{
			Archive: repo.DefaultConfig().RepoPath,
			Config:  repo.ResolveConfigPath(""),
			Logs:    repo.DefaultLogDir(),
		},
		Privacy: control.Privacy{
			LocalOnlyScopes: []string{"contacts"},
		},
	}
}

func (c *Crawler) Verbs() []crawlkit.Verb {
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
		exportVCardVerb(),
		gitVerb(),
	}
}

func (c *Crawler) Status(ctx context.Context, req *crawlkit.Request) (*control.Status, error) {
	var status control.Status
	if err := runJSON(ctx, req, []string{"status"}, &status); err != nil {
		return nil, err
	}
	if status.AppID == "" {
		status.AppID = "contacts"
	}
	return &status, nil
}

func (c *Crawler) Doctor(ctx context.Context, req *crawlkit.Request) (*crawlkit.Doctor, error) {
	var report struct {
		Checks []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Message string `json:"message,omitempty"`
			Remedy  string `json:"remedy,omitempty"`
		} `json:"checks"`
	}
	if err := runJSON(ctx, req, []string{"doctor"}, &report); err != nil {
		return nil, err
	}
	doctor := &crawlkit.Doctor{Checks: make([]crawlkit.Check, 0, len(report.Checks))}
	for _, check := range report.Checks {
		doctor.Checks = append(doctor.Checks, crawlkit.Check{
			ID:      check.ID,
			State:   check.State,
			Message: check.Message,
			Remedy:  check.Remedy,
		})
	}
	return doctor, nil
}

func (c *Crawler) Search(ctx context.Context, req *crawlkit.Request, q crawlkit.Query) (crawlkit.SearchResult, error) {
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
	args := []string{"search", query}
	if q.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(q.Limit))
	} else {
		args = append(args, "--all")
	}
	var envelope struct {
		Results []struct {
			Kind      string    `json:"kind"`
			ID        string    `json:"id"`
			PersonID  string    `json:"person_id,omitempty"`
			Name      string    `json:"name,omitempty"`
			Snippet   string    `json:"snippet,omitempty"`
			Timestamp time.Time `json:"timestamp,omitzero"`
		} `json:"results"`
		TotalMatches int  `json:"total_matches"`
		Truncated    bool `json:"truncated"`
	}
	if err := runJSON(ctx, req, args, &envelope); err != nil {
		return crawlkit.SearchResult{}, err
	}
	result := crawlkit.SearchResult{
		Results:      make([]crawlkit.Hit, 0, len(envelope.Results)),
		TotalMatches: envelope.TotalMatches,
		Truncated:    envelope.Truncated,
	}
	for _, hit := range envelope.Results {
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

func (c *Crawler) Who(ctx context.Context, req *crawlkit.Request, person string) ([]whomatch.Candidate, error) {
	var envelope struct {
		Candidates []struct {
			Who         string   `json:"who"`
			Identifiers []string `json:"identifiers"`
			LastSeen    string   `json:"last_seen,omitempty"`
		} `json:"candidates"`
	}
	if err := runJSON(ctx, req, []string{"who", person}, &envelope); err != nil {
		return nil, err
	}
	candidates := make([]whomatch.Candidate, 0, len(envelope.Candidates))
	for _, candidate := range envelope.Candidates {
		candidates = append(candidates, whomatch.Candidate{
			Who:         candidate.Who,
			Identifiers: append([]string(nil), candidate.Identifiers...),
			LastSeen:    parseTime(candidate.LastSeen),
		})
	}
	return candidates, nil
}

func (c *Crawler) Open(ctx context.Context, req *crawlkit.Request, ref string) error {
	return runText(ctx, req, []string{"person", "show", contactQuery(ref)}, req.Format == output.JSON)
}

func (c *Crawler) ContactExport(ctx context.Context, req *crawlkit.Request) (*control.ContactExport, error) {
	var envelope struct {
		Contacts []struct {
			DisplayName  string   `json:"display_name"`
			PhoneNumbers []string `json:"phone_numbers,omitempty"`
		} `json:"contacts"`
	}
	if err := runJSON(ctx, req, []string{"contacts", "export"}, &envelope); err != nil {
		return nil, err
	}
	out := &control.ContactExport{Contacts: make([]control.Contact, 0, len(envelope.Contacts))}
	for _, contact := range envelope.Contacts {
		phones := cleanStrings(contact.PhoneNumbers)
		if strings.TrimSpace(contact.DisplayName) == "" || len(phones) == 0 {
			continue
		}
		out.Contacts = append(out.Contacts, control.Contact{
			DisplayName:  strings.TrimSpace(contact.DisplayName),
			PhoneNumbers: phones,
		})
	}
	return out, nil
}

func initVerb() crawlkit.Verb {
	var remote string
	var noConfig bool
	return crawlkit.Verb{
		Name:    "init",
		Help:    "Create the contacts repo",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&remote, "remote", "", "Git remote for contacts backup")
			fs.BoolVar(&noConfig, "no-config", false, "Do not write app config")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			args := append([]string{"init"}, req.Args...)
			if remote != "" {
				args = append(args, "--remote", remote)
			}
			if noConfig {
				args = append(args, "--no-config")
			}
			return runText(ctx, req, args, req.Format == output.JSON)
		},
	}
}

func configVerb() crawlkit.Verb {
	return crawlkit.Verb{
		Name:    "config",
		Help:    "Show or set contacts config",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			return runText(ctx, req, append([]string{"config"}, req.Args...), req.Format == output.JSON)
		},
	}
}

func personListVerb() crawlkit.Verb {
	var query string
	var limit int
	var all bool
	return crawlkit.Verb{
		Name:  "person list",
		Help:  "List people",
		Store: crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&query, "query", "", "Filter query")
			fs.StringVar(&query, "q", "", "Filter query")
			fs.IntVar(&limit, "limit", 0, "Number of people to show")
			fs.BoolVar(&all, "all", false, "Show every person")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			args := []string{"person", "list"}
			if query != "" {
				args = append(args, "--query", query)
			}
			if limit > 0 {
				args = append(args, "--limit", strconv.Itoa(limit))
			}
			if all {
				args = append(args, "--all")
			}
			return runText(ctx, req, args, req.Format == output.JSON)
		},
	}
}

func personShowVerb() crawlkit.Verb {
	return crawlkit.Verb{
		Name:  "person show",
		Help:  "Show a person",
		Store: crawlkit.StoreNone,
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			return runText(ctx, req, append([]string{"person", "show"}, req.Args...), req.Format == output.JSON)
		},
	}
}

func importVerb() crawlkit.Verb {
	var from, input, account string
	var fromAll, avatars, dryRun bool
	var minMessages int
	return crawlkit.Verb{
		Name:    "import",
		Help:    "Import contacts into local markdown",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&from, "from", "", "Crawler binary to import contacts from")
			fs.BoolVar(&fromAll, "from-all", false, "Import contacts from every contacts crawler")
			fs.StringVar(&input, "input", "", "JSON or NDJSON contact file")
			fs.BoolVar(&avatars, "avatars", false, "Import local avatar thumbnails")
			fs.StringVar(&account, "account", "", "Google account email")
			fs.IntVar(&minMessages, "min-messages", 0, "Minimum message count")
			fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			args := []string{"import"}
			args = append(args, req.Args...)
			if from != "" {
				args = append(args, "--from", from)
			}
			if fromAll {
				args = append(args, "--from-all")
			}
			if input != "" {
				args = append(args, "--input", input)
			}
			if avatars {
				args = append(args, "--avatars")
			}
			if account != "" {
				args = append(args, "--account", account)
			}
			if minMessages > 0 {
				args = append(args, "--min-messages", strconv.Itoa(minMessages))
			}
			return runTextWithGlobals(ctx, req, args, req.Format == output.JSON, dryRun)
		},
	}
}

func exportVCardVerb() crawlkit.Verb {
	var person, out string
	var all, includeAvatars bool
	return crawlkit.Verb{
		Name:    "export vcard",
		Help:    "Export vCard files",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&person, "person", "", "Person query")
			fs.BoolVar(&all, "all", false, "Export all people")
			fs.BoolVar(&includeAvatars, "include-avatars", false, "Include avatar photo fields")
			fs.StringVar(&out, "out", "", "Output .vcf path, or - for stdout")
			fs.StringVar(&out, "o", "", "Output .vcf path, or - for stdout")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			args := []string{"export", "vcard"}
			if person != "" {
				args = append(args, "--person", person)
			}
			if all {
				args = append(args, "--all")
			}
			if includeAvatars {
				args = append(args, "--include-avatars")
			}
			if out != "" {
				args = append(args, "--out", out)
			}
			return runText(ctx, req, args, req.Format == output.JSON)
		},
	}
}

func gitVerb() crawlkit.Verb {
	var message string
	return crawlkit.Verb{
		Name:    "git",
		Help:    "Run contacts repo git helpers",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&message, "message", "", "Commit message")
			fs.StringVar(&message, "m", "", "Commit message")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			args := append([]string{"git"}, req.Args...)
			if message != "" {
				args = append(args, "--message", message)
			}
			return runText(ctx, req, args, req.Format == output.JSON)
		},
	}
}

func runJSON(ctx context.Context, req *crawlkit.Request, args []string, target any) error {
	data, err := runCLI(ctx, req, args, true, false)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("decode contacts JSON: %w", err)
	}
	return nil
}

func runText(ctx context.Context, req *crawlkit.Request, args []string, jsonOut bool) error {
	return runTextWithGlobals(ctx, req, args, jsonOut, false)
}

func runTextWithGlobals(ctx context.Context, req *crawlkit.Request, args []string, jsonOut, dryRun bool) error {
	data, err := runCLI(ctx, req, args, jsonOut, dryRun)
	if len(data) > 0 {
		if _, writeErr := req.Out.Write(data); writeErr != nil {
			return writeErr
		}
	}
	return err
}

func runCLI(ctx context.Context, req *crawlkit.Request, args []string, jsonOut, dryRun bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cliArgs := baseArgs(req, jsonOut, dryRun)
	cliArgs = append(cliArgs, args...)
	var stdout, stderr bytes.Buffer
	err := internalcli.Execute(cliArgs, &stdout, &stderr)
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return stdout.Bytes(), fmt.Errorf("%w: %s", err, message)
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

func baseArgs(req *crawlkit.Request, jsonOut, dryRun bool) []string {
	var args []string
	if req != nil {
		if strings.TrimSpace(req.Paths.Config) != "" {
			args = append(args, "--config", req.Paths.Config)
		}
		if repoOverride := repoOverridePath(req.Paths.Archive); repoOverride != "" {
			args = append(args, "--repo", repoOverride)
		}
	}
	if jsonOut {
		args = append(args, "--json")
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}

func repoOverridePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	defaultPath := repo.DefaultConfig().RepoPath
	if filepath.Clean(path) == filepath.Clean(defaultPath) {
		return ""
	}
	return path
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

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
