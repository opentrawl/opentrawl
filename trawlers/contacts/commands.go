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
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func personListCommand() trawlkit.TrawlerCommand {
	var query string
	var limit int
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:               "people",
		TrawlerCommandHelpDescription:    "List people",
		TrawlerCommandDiscoveryPlacement: trawlkit.TrawlerCommandShownInBareTrawlOverviewAndTrawlerNamespaceHelp,
		TrawlerCommandArchiveAccess:      trawlkit.TrawlerCommandArchiveAccessRequired,
		RegisterTrawlerCommandFlags: func(fs *flag.FlagSet) {
			limit = 50
			fs.StringVar(&query, "query", "", "Show only people with a name or contact detail matching `QUERY`")
			fs.IntVar(&limit, "limit", 50, "Maximum number of people")
		},
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			if limit < 1 {
				return nil, usageError(render.LimitMustBeAtLeastOne())
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
		TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
		TrawlerCommandHelpDescription:         "Show one person",
		TrawlerCommandPositionalArgumentNames: []string{"QUERY"},
		TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
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
			var contactPerson model.Person
			if strings.HasPrefix(personLookupText, archive.AppID+":person/") {
				personID, _ := archive.PersonIDFromRef(personLookupText)
				contactPerson, err = st.Person(ctx, personID)
				if err != nil {
					return nil, personLinkLookupError(err)
				}
				return personCommandResponse(contactPerson)
			}
			matchingPeople, err := st.PeopleMatchingQuery(ctx, personLookupText)
			if err != nil {
				return nil, personQueryLookupError(err, personLookupText)
			}
			matchingPeople = peopleInHumanDisplayOrder(matchingPeople)
			switch len(matchingPeople) {
			case 0:
				_, err := st.FindPerson(ctx, personLookupText)
				return nil, personQueryLookupError(err, personLookupText)
			case 1:
				return personCommandResponse(matchingPeople[0])
			default:
				return personListCommandResponse(personListResponseValues{
					peopleInDisplayOrder:     matchingPeople,
					totalMatchingPersonCount: len(matchingPeople),
				})
			}
		},
	}
}

func annotatePersonRelationshipOrContextCommand() trawlkit.TrawlerCommand {
	return trawlkit.TrawlerCommand{
		TrawlerCommandName:                    "annotate",
		TrawlerCommandDiscoveryPlacement:      trawlkit.TrawlerCommandShownOnlyInTrawlerNamespaceHelp,
		TrawlerCommandHelpDescription:         "Add relationship or context to a person",
		TrawlerCommandPositionalArgumentNames: []string{"LINK", "DESCRIPTION"},
		TrawlerCommandChangesArchive:          true,
		TrawlerCommandArchiveAccess:           trawlkit.TrawlerCommandArchiveAccessRequired,
		ExecuteTrawlerCommand: func(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
			personRelationshipOrContextDescription := model.PersonRelationshipOrContextDescription(
				strings.TrimSpace(req.TrawlerCommandPositionalArguments[1]),
			)
			if personRelationshipOrContextDescription == "" {
				return nil, usageError(render.DescriptionMustNotBeEmpty())
			}
			personIdentifier, err := personIdentifierFromGloballyRoutableContactsLink(
				ctx,
				req,
				req.TrawlerCommandPositionalArguments[0],
			)
			if err != nil {
				return nil, err
			}
			archiveStore, err := archive.UseExisting(
				ctx,
				req.OpenedTrawlerArchiveStore,
				req.TrawlerArchivePaths.TrawlerArchivePath,
			)
			if err != nil {
				return nil, archiveErr(fmt.Errorf("open archive: %w", err))
			}
			today := time.Now()
			annotatedPersonIdentifier, err := archiveStore.SetPersonRelationshipOrContextDescription(
				ctx,
				personIdentifier,
				personRelationshipOrContextDescription,
				model.PersonRelationshipOrContextDescriptionStatedDate{
					CalendarYear:        int32(today.Year()),
					CalendarMonthNumber: int32(today.Month()),
					CalendarDayOfMonth:  int32(today.Day()),
				},
			)
			if err != nil {
				return nil, err
			}
			annotatedPerson, err := archiveStore.Person(ctx, annotatedPersonIdentifier)
			if err != nil {
				return nil, err
			}
			return personCommandResponse(annotatedPerson)
		},
	}
}

func personIdentifierFromGloballyRoutableContactsLink(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	globallyRoutableContactsLink string,
) (string, error) {
	localPersonShortReference, argumentWasGloballyRoutableContactsLink, err :=
		trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(
			globallyRoutableContactsLink,
			archive.AppID,
		)
	if err != nil {
		return "", err
	}
	if !argumentWasGloballyRoutableContactsLink {
		return "", usageError(render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounPerson))
	}
	personIdentifier, err := resolveOpenRef(
		ctx,
		req,
		trawlkit.NewLocalTrawlerShortReference(localPersonShortReference),
	)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", personSelectionContractError{
			personSelectionError:       render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounPerson),
			personSelectionFailureCode: "not_found",
		}
	}
	return personIdentifier, err
}
