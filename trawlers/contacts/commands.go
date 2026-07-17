package clawdex

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/birdclaw"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/discrawl"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckconfig "github.com/opentrawl/opentrawl/trawlkit/config"
)

func personListVerb() trawlkit.Verb {
	var query string
	var limit int
	return trawlkit.Verb{
		Name:  "person list",
		Help:  "List people in the contacts archive.",
		Store: trawlkit.StoreRequired,
		Flags: func(fs *flag.FlagSet) {
			limit = 50
			fs.StringVar(&query, "query", "", "Filter query")
			fs.StringVar(&query, "q", "", "Filter query")
			fs.IntVar(&limit, "limit", 50, "Number of people to show")
		},
		Run: func(ctx context.Context, req *trawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("person list takes no arguments"))
			}
			if limit < 1 {
				return usageError(errors.New("--limit must be at least 1"))
			}
			st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
			if err != nil {
				return archiveErr(fmt.Errorf("open archive: %w", err))
			}
			people, err := st.People(ctx)
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

func personShowVerb() trawlkit.Verb {
	return trawlkit.Verb{
		Name:  "person show",
		Help:  "Show one person from the contacts archive.",
		Args:  []string{"QUERY"},
		Store: trawlkit.StoreRequired,
		Run: func(ctx context.Context, req *trawlkit.Request) error {
			if len(req.Args) != 1 {
				return usageError(errors.New("person show needs one query"))
			}
			st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
			if err != nil {
				return archiveErr(fmt.Errorf("open archive: %w", err))
			}
			person, err := st.FindPerson(ctx, req.Args[0])
			if err != nil {
				return err
			}
			return writePerson(req, person)
		},
	}
}

func personAnnotateVerb() trawlkit.Verb {
	return trawlkit.Verb{
		Name:    "person annotate",
		Help:    "Record the user's stated correction for a person.",
		Args:    []string{"PERSON_ID", "ANNOTATION"},
		Mutates: true,
		Store:   trawlkit.StoreRequired,
		Run: func(ctx context.Context, req *trawlkit.Request) error {
			if len(req.Args) != 2 {
				return usageError(errors.New("person annotate needs PERSON_ID and one quoted annotation"))
			}
			st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
			if err != nil {
				return archiveErr(fmt.Errorf("open archive: %w", err))
			}
			personID, err := st.AnnotatePerson(ctx, req.Args[0], req.Args[1], time.Now().UTC().Format("2006-01-02"))
			if err != nil {
				return err
			}
			person, err := st.Person(ctx, personID)
			if err != nil {
				return err
			}
			return writePersonAnnotation(req, person)
		},
	}
}

func importVerb() trawlkit.Verb {
	var input, dbPath string
	var avatars, dryRun bool
	var minMessages int
	return trawlkit.Verb{
		Name:    "import",
		Help:    "Import contacts into the local archive.",
		Args:    []string{"SOURCE"},
		Mutates: true,
		Store:   trawlkit.StoreRequired,
		Flags: func(fs *flag.FlagSet) {
			minMessages = 4
			fs.StringVar(&input, "input", "", "JSON or NDJSON contact file")
			fs.BoolVar(&avatars, "avatars", false, "Import avatar metadata")
			fs.StringVar(&dbPath, "db", "", "Source SQLite database path")
			fs.IntVar(&minMessages, "min-messages", 4, "Minimum message count")
			fs.BoolVar(&dryRun, "dry-run", false, "Preview changes without writing")
		},
		Run: func(ctx context.Context, req *trawlkit.Request) error {
			if len(req.Args) != 1 {
				return usageError(errors.New("import needs one source: apple, discrawl, or birdclaw"))
			}
			source, contacts, err := importSourceContacts(ctx, req.Args[0], importOptions{
				input:       input,
				dbPath:      dbPath,
				avatars:     avatars,
				minMessages: minMessages,
			})
			if err != nil {
				return err
			}
			st, err := archive.Use(ctx, req.Store, req.Paths.Archive)
			if err != nil {
				return archiveErr(fmt.Errorf("open archive: %w", err))
			}
			changes, err := st.ImportContacts(ctx, source, contacts, dryRun, time.Now())
			if err != nil {
				return err
			}
			return writeImportChanges(req, importChangesEnvelope{Changes: changes})
		},
	}
}

type importOptions struct {
	input       string
	dbPath      string
	avatars     bool
	minMessages int
}

func importSourceContacts(ctx context.Context, source string, opts importOptions) (string, []model.SourceContact, error) {
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

func importLegacyVerb() trawlkit.Verb {
	var from string
	return trawlkit.Verb{
		Name:    "import-legacy",
		Help:    "Import the old contacts share directory into the local archive.",
		Mutates: true,
		Store:   trawlkit.StoreRequired,
		Flags: func(fs *flag.FlagSet) {
			fs.StringVar(&from, "from", "", "Legacy contacts share directory")
		},
		Run: func(ctx context.Context, req *trawlkit.Request) error {
			if len(req.Args) > 0 {
				return usageError(errors.New("import-legacy takes no arguments"))
			}
			sourcePath := strings.TrimSpace(from)
			if sourcePath == "" {
				sourcePath = defaultLegacyPath(req.Paths.Config)
			}
			st, err := archive.Use(ctx, req.Store, req.Paths.Archive)
			if err != nil {
				return archiveErr(fmt.Errorf("open archive: %w", err))
			}
			summary, err := st.ImportLegacy(ctx, sourcePath)
			if err != nil {
				return err
			}
			return writeLegacyImport(req, legacyImportEnvelope{From: sourcePath, Summary: summary})
		},
	}
}

func defaultLegacyPath(configPath string) string {
	type legacyConfig struct {
		RepoPath string `toml:"repo_path"`
	}
	var cfg legacyConfig
	if strings.TrimSpace(configPath) != "" {
		if err := ckconfig.LoadTOML(configPath, &cfg); err == nil && strings.TrimSpace(cfg.RepoPath) != "" {
			return ckconfig.ExpandHome(cfg.RepoPath)
		}
		return filepath.Join(filepath.Dir(configPath), "share")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".opentrawl", "contacts", "share")
	}
	return filepath.Join(home, ".opentrawl", "contacts", "share")
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

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func formatCount(count int) string {
	return fmt.Sprintf("%d", count)
}
