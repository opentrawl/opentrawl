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
	"github.com/opentrawl/opentrawl/trawlkit/output"
	commandv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command/v1"
)

func personListCommand() trawlkit.TrawlerCommand {
	var query string
	var limit int
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:                     "people",
		TrawlerCommandHelpDescription:          "List people",
		TrawlerCommandShownInBareTrawlOverview: true,
		TrawlerCommandArchiveAccess:            trawlkit.TrawlerCommandArchiveAccessRequired,
		RegisterTrawlerCommandFlags: func(fs *flag.FlagSet) {
			limit = 50
			fs.StringVar(&query, "query", "", "Show only people with a name or contact detail matching `QUERY`")
			fs.IntVar(&limit, "limit", 50, "Maximum number of people")
		},
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
			if len(req.TrawlerCommandPositionalArguments) > 0 {
				return nil, usageError(errors.New("people takes no arguments"))
			}
			if limit < 1 {
				return nil, usageError(output.HumanFacingErrorMessage("--limit must be at least 1."))
			}
			st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
			if err != nil {
				return nil, archiveErr(fmt.Errorf("open archive: %w", err))
			}
			var people []model.Person
			if strings.TrimSpace(query) == "" {
				people, err = st.People(ctx)
			} else {
				people, err = st.PeopleMatchingQuery(ctx, query)
			}
			if err != nil {
				return nil, err
			}
			people = peopleInHumanDisplayOrder(people)
			total := len(people)
			if len(people) > limit {
				people = people[:limit]
			}
			return personListCommandResponse(personListResponseValues{
				peopleInDisplayOrder:     people,
				totalMatchingPersonCount: total,
				moreMatchingPeopleExist:  total > len(people),
			})
		},
	}
}

func personShowCommand() trawlkit.TrawlerCommand {
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:                    "person",
		TrawlerCommandHelpDescription:         "Show one person",
		TrawlerCommandPositionalArgumentNames: []string{"QUERY"},
		TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
			if len(req.TrawlerCommandPositionalArguments) != 1 {
				return nil, usageError(errors.New("person needs one query"))
			}
			st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
			if err != nil {
				return nil, archiveErr(fmt.Errorf("open archive: %w", err))
			}
			personLookupText, err := resolvePersonLookupTextFromPossibleGloballyRoutableContactsLink(
				ctx,
				req,
				req.TrawlerCommandPositionalArguments[0],
			)
			if err != nil {
				return nil, err
			}
			var person model.Person
			if strings.HasPrefix(personLookupText, archive.AppID+":person/") {
				personID, _ := archive.PersonIDFromRef(personLookupText)
				person, err = st.Person(ctx, personID)
			} else {
				person, err = st.FindPerson(ctx, personLookupText)
			}
			if err != nil {
				return nil, personLookupError(err)
			}
			return personCommandResponse(person), nil
		},
	}
}

func personAnnotationCommand() trawlkit.TrawlerCommand {
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:                    "annotate",
		TrawlerCommandHelpDescription:         "Record the user's stated correction for a person.",
		TrawlerCommandPositionalArgumentNames: []string{"PERSON_ID", "ANNOTATION"},
		TrawlerCommandChangesArchive:          true,
		TrawlerCommandHelpListing:             trawlkit.TrawlerCommandHiddenFromHumanHelp,
		TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*commandv1.TrawlerCommandResponse, error) {
			if len(req.TrawlerCommandPositionalArguments) != 2 {
				return nil, usageError(errors.New("annotate needs PERSON_ID and one quoted annotation"))
			}
			st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
			if err != nil {
				return nil, archiveErr(fmt.Errorf("open archive: %w", err))
			}
			personID, err := st.AnnotatePerson(ctx, req.TrawlerCommandPositionalArguments[0], req.TrawlerCommandPositionalArguments[1], time.Now().UTC().Format("2006-01-02"))
			if err != nil {
				return nil, err
			}
			person, err := st.Person(ctx, personID)
			if err != nil {
				return nil, err
			}
			return personAnnotationCommandResponse(person), nil
		},
	}
}
