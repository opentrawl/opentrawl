package clawdex

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openclaw/clawdex/internal/apple"
	"github.com/openclaw/clawdex/internal/avatar"
	"github.com/openclaw/clawdex/internal/birdclaw"
	"github.com/openclaw/clawdex/internal/discrawl"
	"github.com/openclaw/clawdex/internal/google"
	"github.com/openclaw/clawdex/internal/markdown"
	"github.com/openclaw/clawdex/internal/model"
	"github.com/openclaw/clawdex/internal/repo"
	"github.com/openclaw/clawdex/internal/vcard"
	"github.com/openclaw/crawlkit"
	ckoutput "github.com/openclaw/crawlkit/output"
)

func initVerb() crawlkit.Verb {
	var remote string
	var noConfig bool
	return crawlkit.Verb{
		Name:    "init",
		Help:    "Create the contacts repo",
		Args:    []string{"DIR"},
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&remote, "remote", "", "Git remote for contacts backup")
			fs.BoolVar(&noConfig, "no-config", false, "Do not write app config")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			if len(req.Args) > 1 {
				return usageError(errors.New("init takes at most one directory"))
			}
			cfg, err := repo.LoadConfig(req.Paths.Config)
			if err != nil {
				return err
			}
			if len(req.Args) == 1 {
				cfg.RepoPath = req.Args[0]
			}
			if remote != "" {
				cfg.Git.Remote = remote
			}
			cfg.Normalize()
			dataRepo := repo.Open(cfg.RepoPath, cfg)
			if err := dataRepo.Init(ctx); err != nil {
				return err
			}
			if !noConfig {
				if err := repo.WriteConfig(req.Paths.Config, cfg); err != nil {
					return err
				}
			}
			return writeMap(req, map[string]any{
				"config_path": req.Paths.Config,
				"remote":      cfg.Git.Remote,
				"repo_path":   cfg.RepoPath,
			})
		},
	}
}

func configVerb() crawlkit.Verb {
	var dryRun bool
	return crawlkit.Verb{
		Name:    "config",
		Help:    "Show or set contacts config",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing")
		},
		Run: func(_ context.Context, req *crawlkit.Request) error {
			cfg, err := repo.LoadConfig(req.Paths.Config)
			if err != nil {
				return err
			}
			switch len(req.Args) {
			case 0:
				return writeConfig(req, cfg)
			case 1:
				if req.Args[0] != "show" {
					return usageError(fmt.Errorf("unknown config action %q", req.Args[0]))
				}
				return writeConfig(req, cfg)
			case 3:
				if req.Args[0] != "set" {
					return usageError(fmt.Errorf("unknown config action %q", req.Args[0]))
				}
				if err := setConfigValue(&cfg, req.Args[1], req.Args[2]); err != nil {
					return err
				}
				if dryRun {
					return writeConfig(req, cfg)
				}
				if err := repo.WriteConfig(req.Paths.Config, cfg); err != nil {
					return err
				}
				return writeMap(req, map[string]any{"config_path": req.Paths.Config, "set": req.Args[1]})
			default:
				return usageError(errors.New("config usage: config [show] or config set KEY VALUE"))
			}
		},
	}
}

func setConfigValue(cfg *repo.Config, key, value string) error {
	switch key {
	case "repo_path":
		cfg.RepoPath = value
	case "git.remote":
		cfg.Git.Remote = value
	case "git.branch":
		cfg.Git.Branch = value
	case "google.default_account":
		cfg.Google.DefaultAccount = value
	default:
		return usageError(fmt.Errorf("unsupported config key %q", key))
	}
	cfg.Normalize()
	return nil
}

func personListVerb() crawlkit.Verb {
	var query string
	var limit int
	return crawlkit.Verb{
		Name:  "person list",
		Help:  "List people",
		Store: crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			limit = 50
			fs.StringVar(&query, "query", "", "Filter query")
			fs.StringVar(&query, "q", "", "Filter query")
			fs.IntVar(&limit, "limit", 50, "Number of people to show")
		},
		Run: func(_ context.Context, req *crawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("person list takes no arguments"))
			}
			if limit < 1 {
				return usageError(errors.New("--limit must be at least 1"))
			}
			rt, err := newRuntime(req)
			if err != nil {
				return err
			}
			people, err := rt.store.People()
			if err != nil {
				return err
			}
			if query != "" {
				people = filterPeople(people, query)
			}
			total := len(people)
			if len(people) > limit {
				people = people[:limit]
			}
			return writePeople(req, peopleEnvelope{
				Query:     query,
				People:    people,
				Total:     total,
				Truncated: total > len(people),
				limit:     limit,
			})
		},
	}
}

func personShowVerb() crawlkit.Verb {
	return crawlkit.Verb{
		Name:  "person show",
		Help:  "Show a person",
		Args:  []string{"QUERY"},
		Store: crawlkit.StoreNone,
		Run: func(_ context.Context, req *crawlkit.Request) error {
			if len(req.Args) != 1 {
				return usageError(errors.New("person show needs one query"))
			}
			rt, err := newRuntime(req)
			if err != nil {
				return err
			}
			person, err := rt.store.FindPerson(req.Args[0])
			if err != nil {
				return err
			}
			return writePerson(req, person)
		},
	}
}

func importVerb() crawlkit.Verb {
	var input, account, dbPath string
	var avatars, dryRun bool
	var minMessages int
	return crawlkit.Verb{
		Name:    "import",
		Help:    "Import contacts into local markdown",
		Args:    []string{"SOURCE"},
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			minMessages = 4
			fs.StringVar(&input, "input", "", "JSON or NDJSON contact file")
			fs.BoolVar(&avatars, "avatars", false, "Import avatar thumbnails")
			fs.StringVar(&account, "account", "", "Google account email")
			fs.StringVar(&dbPath, "db", "", "Source SQLite database path")
			fs.IntVar(&minMessages, "min-messages", 4, "Minimum message count")
			fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			if len(req.Args) != 1 {
				return usageError(errors.New("import needs one source: apple, google, discrawl, or birdclaw"))
			}
			rt, err := newRuntime(req)
			if err != nil {
				return err
			}
			source, contacts, err := importSourceContacts(ctx, rt.cfg, req.Args[0], importOptions{
				input:       input,
				account:     account,
				dbPath:      dbPath,
				avatars:     avatars,
				minMessages: minMessages,
			})
			if err != nil {
				return err
			}
			changes, err := rt.store.ImportContacts(source, contacts, dryRun, time.Now())
			if err != nil {
				return err
			}
			return writeImportChanges(req, importChangesEnvelope{Changes: changes})
		},
	}
}

type importOptions struct {
	input       string
	account     string
	dbPath      string
	avatars     bool
	minMessages int
}

func importSourceContacts(ctx context.Context, cfg repo.Config, source string, opts importOptions) (string, []model.SourceContact, error) {
	switch source {
	case "apple":
		var contacts []apple.Contact
		var err error
		if opts.input != "" {
			contacts, err = apple.ReadFile(opts.input)
		} else {
			contacts, err = apple.ReadSystem(ctx)
		}
		if err != nil {
			return "", nil, err
		}
		return "apple", apple.ToSourceContacts(contacts, opts.avatars), nil
	case "google":
		account := firstText(opts.account, cfg.Google.DefaultAccount)
		contacts, err := (google.GogAdapter{}).ListContactsWithOptions(ctx, account, google.Options{IncludeAvatars: opts.avatars})
		return "google", contacts, err
	case "discrawl":
		contacts, err := (discrawl.Adapter{DBPath: opts.dbPath}).ListDMContacts(ctx, opts.minMessages)
		return "discord", contacts, err
	case "birdclaw":
		contacts, err := (birdclaw.Adapter{DBPath: opts.dbPath}).ListDMContacts(ctx, opts.minMessages)
		return "x", contacts, err
	case "contacts":
		return "", nil, usageError(errors.New("import contacts from crawler binaries has been removed"))
	default:
		return "", nil, usageError(fmt.Errorf("unknown import source %q", source))
	}
}

func repairVerb() crawlkit.Verb {
	var dryRun bool
	return crawlkit.Verb{
		Name:    "repair",
		Help:    "Repair contacts markdown and rebuild the index",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.BoolVar(&dryRun, "dry-run", false, "Preview repair without writing")
		},
		Run: func(_ context.Context, req *crawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("repair takes no arguments"))
			}
			rt, err := newRuntime(req)
			if err != nil {
				return err
			}
			doctor, err := rt.repairDoctor(dryRun)
			if err != nil {
				return err
			}
			return writeDoctor(req, doctor)
		},
	}
}

func syncAppleVerb() crawlkit.Verb {
	return crawlkit.Verb{
		Name:  "sync apple",
		Help:  "Preview Apple Contacts sync",
		Store: crawlkit.StoreNone,
		Run: func(_ context.Context, req *crawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("sync apple takes no arguments"))
			}
			return writeMap(req, map[string]any{
				"dry_run": true,
				"status":  "remote writes not implemented yet; use import apple for local markdown projection",
			})
		},
	}
}

func syncGoogleVerb() crawlkit.Verb {
	var account string
	return crawlkit.Verb{
		Name:  "sync google",
		Help:  "Preview Google Contacts sync",
		Store: crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&account, "account", "", "Google account email")
		},
		Run: func(_ context.Context, req *crawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("sync google takes no arguments"))
			}
			cfg, err := repo.LoadConfig(req.Paths.Config)
			if err != nil {
				return err
			}
			return writeMap(req, map[string]any{
				"account": firstText(account, cfg.Google.DefaultAccount),
				"dry_run": true,
				"status":  "remote writes not implemented yet; use import google for local markdown projection",
			})
		},
	}
}

func exportVCardVerb() crawlkit.Verb {
	var person, out string
	var includeAvatars bool
	return crawlkit.Verb{
		Name:    "export vcard",
		Help:    "Export vCard files",
		Mutates: true,
		Store:   crawlkit.StoreNone,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&person, "person", "", "Person query")
			fs.BoolVar(&includeAvatars, "include-avatars", false, "Include avatar photo fields")
			fs.StringVar(&out, "out", "", "Output .vcf path, or - for stdout")
			fs.StringVar(&out, "o", "", "Output .vcf path, or - for stdout")
		},
		Run: func(_ context.Context, req *crawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("export vcard takes flags only"))
			}
			if strings.TrimSpace(person) == "" {
				return usageError(errors.New("provide --person"))
			}
			if strings.TrimSpace(out) == "" {
				return usageError(errors.New("provide --out"))
			}
			rt, err := newRuntime(req)
			if err != nil {
				return err
			}
			p, err := rt.store.FindPerson(person)
			if err != nil {
				return err
			}
			if out == "-" {
				return vcard.WriteWithOptions(req.Out, []model.Person{p}, vcard.Options{IncludeAvatars: includeAvatars})
			}
			f, err := os.Create(out)
			if err != nil {
				return err
			}
			if err := vcard.WriteWithOptions(f, []model.Person{p}, vcard.Options{IncludeAvatars: includeAvatars}); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
			return writeMap(req, map[string]any{"exported": 1, "out": out})
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
			fs.StringVar(&message, "message", "sync: update contacts", "Commit message")
			fs.StringVar(&message, "m", "sync: update contacts", "Commit message")
		},
		Run: func(ctx context.Context, req *crawlkit.Request) error {
			rt, err := newRuntime(req)
			if err != nil {
				return err
			}
			action := "status"
			if len(req.Args) > 0 {
				action = req.Args[0]
			}
			switch action {
			case "status":
				if len(req.Args) > 1 {
					return usageError(errors.New("git status takes no arguments"))
				}
				return runGitStatus(ctx, req, rt.repo.Path)
			case "pull":
				if len(req.Args) > 1 {
					return usageError(errors.New("git pull takes no arguments"))
				}
				if err := rt.repo.Pull(ctx); err != nil {
					return err
				}
				return writeMap(req, map[string]any{"pulled": true})
			case "push":
				if len(req.Args) > 1 {
					return usageError(errors.New("git push takes no arguments"))
				}
				if err := rt.repo.Push(ctx); err != nil {
					return err
				}
				return writeMap(req, map[string]any{"pushed": true})
			case "commit":
				if len(req.Args) > 1 {
					return usageError(errors.New("git commit takes no arguments"))
				}
				committed, err := rt.repo.Commit(ctx, message)
				if err != nil {
					return err
				}
				return writeMap(req, map[string]any{"committed": committed})
			default:
				return usageError(fmt.Errorf("unknown git action %q", action))
			}
		},
	}
}

func runGitStatus(ctx context.Context, req *crawlkit.Request, repoPath string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "status", "--short", "--branch")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("git status: %w: %s", err, msg)
		}
		return fmt.Errorf("git status: %w", err)
	}
	if req.Format == ckoutput.JSON {
		lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = []string{}
		}
		return writeMap(req, map[string]any{"lines": lines})
	}
	_, err := req.Out.Write(stdout.Bytes())
	if err != nil {
		return err
	}
	return nil
}

func filterPeople(people []model.Person, query string) []model.Person {
	query = strings.ToLower(strings.Join(strings.Fields(query), " "))
	filtered := people[:0]
	for _, person := range people {
		text := strings.ToLower(person.Name + " " + person.ID + " " + strings.Join(person.Tags, " "))
		if strings.Contains(text, query) {
			filtered = append(filtered, person)
		}
	}
	return filtered
}

func (rt appRuntime) repairDoctor(dryRun bool) (*crawlkit.Doctor, error) {
	if err := rt.repo.Require(); err != nil {
		return &crawlkit.Doctor{Checks: []crawlkit.Check{rt.contactsRepoCheck()}}, nil
	}
	store := rt.store
	people, err := store.People()
	if err != nil {
		return nil, err
	}
	var repaired int
	var avatarRepaired int
	for _, person := range people {
		loaded, report, err := markdown.ReadPerson(person.Path)
		if err != nil {
			return nil, err
		}
		if report.Needed {
			repaired++
			if !dryRun {
				if err := markdown.RepairPerson(person.Path, rt.repo.RepairDir(), loaded, report, rt.cfg.Repair.BackupBeforeRepair); err != nil {
					return nil, err
				}
			}
		}
		if len(avatar.Validate(loaded)) > 0 {
			avatarRepaired++
			if !dryRun {
				fixed, changed, err := avatar.RepairMetadata(loaded, time.Now())
				if err != nil {
					return nil, err
				}
				if changed {
					if err := markdown.WritePerson(fixed.Path, fixed); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	checks := []crawlkit.Check{
		{ID: "contacts_repo", State: "ok", Message: countNoun(len(people), "person", "people")},
		{ID: "markdown_repair", State: repairState(repaired), Message: repairMessage("person markdown file", repaired, dryRun)},
	}
	if avatarRepaired > 0 {
		checks = append(checks, crawlkit.Check{ID: "avatar_metadata", State: repairState(avatarRepaired), Message: repairMessage("avatar metadata entry", avatarRepaired, dryRun)})
	}
	if !dryRun {
		if err := rt.store.Rebuild(); err != nil {
			return nil, err
		}
	}
	return &crawlkit.Doctor{Checks: checks}, nil
}

func (rt appRuntime) personRepairProblemCount() (int, error) {
	entries, err := os.ReadDir(rt.repo.PeopleDir())
	if err != nil {
		return 0, err
	}
	var problems int
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(rt.repo.PeopleDir(), entry.Name(), "person.md")
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

func personRepairSummary(count int) string {
	return countNoun(count, "person markdown file", "person markdown files") + " need repair"
}

func repairState(count int) string {
	if count == 0 {
		return "empty"
	}
	return "ok"
}

func repairMessage(item string, count int, dryRun bool) string {
	if count == 0 {
		return "no " + pluralItem(item) + " needed repair"
	}
	action := "repaired"
	if dryRun {
		action = "would be repaired"
	}
	return countNoun(count, item, pluralItem(item)) + " " + action
}

func pluralItem(item string) string {
	if strings.HasSuffix(item, "entry") {
		return strings.TrimSuffix(item, "entry") + "entries"
	}
	return item + "s"
}
