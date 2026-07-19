package contacts

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
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
