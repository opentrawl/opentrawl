package contacts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App struct {
	readApple func(context.Context) ([]apple.Contact, error)
}

type Crawler = App

var (
	_ trawlkit.Trawler                          = (*App)(nil)
	_ trawlkit.Syncer                           = (*App)(nil)
	_ trawlkit.Searcher                         = (*App)(nil)
	_ trawlkit.WhoMatcher                       = (*App)(nil)
	_ trawlkit.ShortReferenceAssignmentProvider = (*App)(nil)
	_ trawlkit.PeopleReconciler                 = (*App)(nil)
)

func New() *App {
	return &App{readApple: apple.ReadSystem}
}

func (a *App) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:                           trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
		RegisteredTrawlerCommandName:                "contacts",
		RegisteredTrawlerDisplayName:                archive.DisplayName,
		TrawlerCommandNamesShownInBareTrawlOverview: []string{"people"},
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Contacts on your Mac.",
			LeavesMachine:   "Nothing. Sync and search stay on your Mac.",
			NetworkRequests: "None. Contacts is local.",
		},
	}
}

func (a *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		personListCommand(),
		personShowCommand(),
		personAnnotationCommand(),
	}
}

func (a *App) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*statusv1.TrawlerStatusResponse, error) {
	status := &statusv1.TrawlerArchiveStatus{}
	response := &statusv1.TrawlerStatusResponse{TrawlerArchiveStatus: status}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return nil, err
	}
	status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "people", ArchiveContentKindDisplayName: "people", ArchiveContentCount: uint64(archiveStatus.People)},
	}
	if !archiveStatus.LastSuccessfullyCompletedArchiveSyncTime.IsZero() {
		status.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(archiveStatus.LastSuccessfullyCompletedArchiveSyncTime)
	}
	status.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (a *App) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*searchv1.TrawlerSearchResponse, error) {
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	normalizedSearchQuery := strings.Join(strings.Fields(query.Text), " ")
	if normalizedSearchQuery == "" && query.WhoResolved != nil {
		normalizedSearchQuery = strings.Join(strings.Fields(query.WhoResolved.Who), " ")
	}
	if normalizedSearchQuery == "" {
		normalizedSearchQuery = strings.Join(strings.Fields(query.Who), " ")
	}
	if normalizedSearchQuery == "" {
		return &searchv1.TrawlerSearchResponse{}, nil
	}
	archiveSearchResults, totalSearchMatches, err := archiveStore.Search(ctx, normalizedSearchQuery, archive.SearchOptions{
		Limit:  query.Limit,
		After:  query.After,
		Before: query.Before,
	})
	if err != nil {
		return nil, err
	}
	trawlerSearchMatches := make([]*searchv1.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		technicalIdentifiers := append([]string(nil), archiveSearchResult.PersonTechnicalIdentifiers...)
		technicalIdentifiers = append(technicalIdentifiers, contactSearchResultTechnicalIdentifiers(archiveSearchResult.Matches)...)
		technicalIdentifiers = append(technicalIdentifiers, archiveSearchResult.PersonID)
		searchMatchTextFields := contactSearchMatchTextFields(archiveSearchResult.Matches, technicalIdentifiers)
		name := humanReadablePersonDisplayName(archiveSearchResult.Who, archiveSearchResult.AlternativePersonNames, technicalIdentifiers)
		presentation := &searchv1.SearchMatchPresentation{
			MatchingRecordKindDisplayName:          "person",
			MatchingRecordDisplayName:              name,
			PeopleRelatedToMatchingRecord:          contactSearchResultPeopleRelatedToMatchingRecord(name),
			DigitalContainerNamesNearestToBroadest: contactSearchResultDigitalContainerNames(archiveSearchResult),
			PhysicalPlaceNamesSpecificToBroadest:   contactSearchResultPhysicalPlaceNames(archiveSearchResult),
			SearchMatchTextFieldsInDisplayOrder:    searchMatchTextFields,
		}
		if !archiveSearchResult.Time.IsZero() {
			presentation.MatchingRecordAssociatedTime = &presentationv1.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(archiveSearchResult.Time)},
			}
		}
		trawlerSearchMatches = append(trawlerSearchMatches, &searchv1.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchResult.Ref),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(archiveSearchResult.AnchorID),
			SearchMatchPresentation:  presentation,
		})
	}
	moreSearchMatchesExist := len(trawlerSearchMatches) < totalSearchMatches
	return &searchv1.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
		TotalSearchMatches:                 uint64(totalSearchMatches),
		MoreSearchMatchesExist:             moreSearchMatchesExist,
	}, nil
}

func contactSearchMatchTextFields(matches []archive.SearchMatch, technicalIdentifiers []string) []*searchv1.SearchMatchTextField {
	searchMatchTextFields := make([]*searchv1.SearchMatchTextField, 0, len(matches))
	seenNormalizedHumanEvidenceText := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		searchMatchText := contactSearchResultMatchText(match)
		searchMatchTextFieldName := ""
		switch match.Field {
		case openrecord.PersonDisplayNameAnchorID:
			if personDisplayNameIsHumanReadable(searchMatchText, technicalIdentifiers) {
				searchMatchTextFieldName = "Name"
			}
		case "sort_name":
			if personDisplayNameIsHumanReadable(searchMatchText, technicalIdentifiers) {
				searchMatchTextFieldName = "Sort name"
			}
		case "annotation":
			searchMatchTextFieldName = "Annotation"
		case "body":
			searchMatchTextFieldName = "Contact note"
		case openrecord.PersonAlternativeDisplayNameAnchorID:
			if personDisplayNameIsHumanReadable(searchMatchText, technicalIdentifiers) {
				searchMatchTextFieldName = "Known as"
			}
		case "tag":
			searchMatchTextFieldName = "Tag"
		case openrecord.PersonEmailAddressAnchorID:
			searchMatchTextFieldName = "Email"
		case openrecord.PersonPhoneNumberAnchorID:
			searchMatchTextFieldName = "Phone"
		case "note_kind":
			searchMatchTextFieldName = "Note kind"
		case "note_body":
			searchMatchTextFieldName = "Note"
		case "note_topic":
			searchMatchTextFieldName = "Note topic"
		}
		if searchMatchTextFieldName == "" {
			continue
		}
		normalizedHumanEvidenceText := strings.ToLower(strings.Join(strings.Fields(searchMatchText), " "))
		if normalizedHumanEvidenceText == "" {
			continue
		}
		if _, alreadyIncluded := seenNormalizedHumanEvidenceText[normalizedHumanEvidenceText]; alreadyIncluded {
			continue
		}
		seenNormalizedHumanEvidenceText[normalizedHumanEvidenceText] = struct{}{}
		searchMatchTextFragments := make(
			[]*searchv1.SearchMatchTextFragment,
			0,
			len(match.Runs),
		)
		for _, run := range match.Runs {
			if run.Text == "" {
				continue
			}
			searchMatchTextFragments = append(searchMatchTextFragments, &searchv1.SearchMatchTextFragment{
				SearchMatchTextFragmentContent:            run.Text,
				SearchMatchTextFragmentMatchesSearchQuery: run.Matched,
			})
		}
		searchMatchTextFields = append(searchMatchTextFields, &searchv1.SearchMatchTextField{
			SearchMatchTextFieldName:               searchMatchTextFieldName,
			SearchMatchTextFragmentsInDisplayOrder: searchMatchTextFragments,
		})
	}
	return searchMatchTextFields
}

func contactSearchResultPeopleRelatedToMatchingRecord(personDisplayName string) []*personv1.PersonRelatedToArchiveRecord {
	personDisplayName = strings.Join(strings.Fields(personDisplayName), " ")
	if personDisplayName == "" {
		return nil
	}
	return []*personv1.PersonRelatedToArchiveRecord{{
		PersonDisplayName: personDisplayName,
	}}
}

func contactSearchResultPhysicalPlaceNames(archiveSearchResult archive.SearchResult) []string {
	for _, match := range archiveSearchResult.Matches {
		if match.Field == openrecord.PersonPostalAddressAnchorID {
			address := strings.Join(strings.Fields(strings.ReplaceAll(contactSearchResultMatchText(match), "\n", ", ")), " ")
			if address != "" {
				return []string{address}
			}
		}
	}
	if physicalPlaceName := strings.TrimSpace(archiveSearchResult.PhysicalPlaceName); physicalPlaceName != "" {
		return []string{physicalPlaceName}
	}
	return nil
}

func contactSearchResultDigitalContainerNames(archiveSearchResult archive.SearchResult) []string {
	for _, match := range archiveSearchResult.Matches {
		if match.Field != openrecord.PersonAccountIdentifierAnchorID && match.Field != "note_source" {
			continue
		}
		accountProvider, _, _ := strings.Cut(strings.TrimSpace(contactSearchResultMatchText(match)), ":")
		if accountProviderDisplayName := contactSearchResultAccountProviderDisplayName(accountProvider); accountProviderDisplayName != "" {
			return []string{accountProviderDisplayName}
		}
	}
	if accountProviderDisplayName := contactSearchResultAccountProviderDisplayName(archiveSearchResult.AccountProviderName); accountProviderDisplayName != "" {
		return []string{accountProviderDisplayName}
	}
	return nil
}

func contactSearchResultAccountProviderDisplayName(accountProviderName string) string {
	accountProviderName = strings.TrimSpace(accountProviderName)
	switch strings.ToLower(accountProviderName) {
	case "apple":
		accountProviderName = "Apple"
	case "google":
		accountProviderName = "Google"
	case "imessage":
		accountProviderName = "iMessage"
	case "telegram":
		accountProviderName = "Telegram"
	case "whatsapp":
		accountProviderName = "WhatsApp"
	}
	if !personDisplayNameIsHumanReadable(accountProviderName, nil) {
		return ""
	}
	return accountProviderName
}

func contactSearchResultTechnicalIdentifiers(matches []archive.SearchMatch) []string {
	technicalIdentifiers := make([]string, 0, len(matches))
	for _, match := range matches {
		switch match.Field {
		case openrecord.PersonAccountIdentifierAnchorID, "identifier":
			technicalIdentifiers = append(technicalIdentifiers, contactSearchResultMatchText(match))
		}
	}
	return technicalIdentifiers
}

func contactSearchResultMatchText(match archive.SearchMatch) string {
	var text strings.Builder
	for _, run := range match.Runs {
		text.WriteString(run.Text)
	}
	return text.String()
}

func (a *App) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, person string) (*personv1.TrawlerPersonMatchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	person, err = resolvePersonLookupTextFromPossibleGloballyRoutableContactsLink(ctx, req, person)
	if err != nil {
		return nil, err
	}
	var candidates []archive.ResolvedPersonMatchCandidate
	if strings.HasPrefix(person, archive.AppID+":person/") {
		candidates, err = st.ResolveCanonicalPersonRecordReference(ctx, person)
	} else {
		candidates, err = st.ResolvePeople(ctx, person)
	}
	if err != nil {
		return nil, err
	}
	out := make([]*personv1.TrawlerPersonMatchCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		exactPersonFilterIdentifiers := candidate.ExactPersonFilterIdentifiersFromTrawlerArchives()
		personDisplayName := humanReadablePersonDisplayName(
			candidate.PersonDisplayName,
			candidate.AlternativePersonDisplayNames,
			exactPersonFilterIdentifiers,
		)
		if personDisplayName == "" {
			personDisplayName = "Contact"
		}
		personMatchFactsFromTrawlers := make(
			[]*personv1.PersonMatchFactsFromTrawler,
			0,
			len(candidate.PersonMatchFactsFromTrawlers),
		)
		for _, trawlerFacts := range candidate.PersonMatchFactsFromTrawlers {
			contributingTrawlerIdentity :=
				registeredTrawlerIdentityForContactsArchiveContributor(
					trawlkit.RegisteredTrawlerIdentityText(
						trawlerFacts.GetRegisteredTrawler(),
					),
				)
			if contributingTrawlerIdentity == "" {
				continue
			}
			personMatchFactsFromTrawlers = append(
				personMatchFactsFromTrawlers,
				trawlkit.NewPersonMatchFactsFromTrawler(
					trawlkit.NewRegisteredTrawlerIdentity(contributingTrawlerIdentity),
					trawlerFacts.GetExactPersonFilterIdentifiersObservedByTrawlerArchive(),
					trawlerFacts.GetPersonDisplayNamesObservedByTrawlerArchive()...,
				),
			)
		}
		personMatchCandidate := &personv1.TrawlerPersonMatchCandidate{
			PersonDisplayName: personDisplayName,
			AlternativePersonDisplayNames: humanReadableAlternativePersonDisplayNames(
				candidate.PersonDisplayName,
				candidate.AlternativePersonDisplayNames,
				personDisplayName,
				exactPersonFilterIdentifiers,
			),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.PersonNameOrHumanReadableContactValueThatMatchedQuery,
			PersonMatchFactsFromTrawlers:                          personMatchFactsFromTrawlers,
			CanonicalPersonRecordReference: trawlkit.NewCanonicalArchiveRecordReference(
				candidate.CanonicalPersonRecordReference,
			),
		}
		if !candidate.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers.IsZero() {
			personMatchCandidate.LatestMatchingArchiveRecordTime = timestamppb.New(
				candidate.LatestArchiveRecordTimeInvolvingPersonAcrossTrawlers,
			)
		}
		personMatchCandidate.MessageCountInvolvingPerson = candidate.MessageCountInvolvingPerson
		out = append(out, personMatchCandidate)
	}
	return &personv1.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func registeredTrawlerIdentityForContactsArchiveContributor(
	contactsArchiveContributorIdentity string,
) string {
	switch strings.TrimSpace(contactsArchiveContributorIdentity) {
	case archive.AppID:
		return ""
	case "apple":
		return archive.AppID
	default:
		return strings.TrimSpace(contactsArchiveContributorIdentity)
	}
}

func resolvePersonLookupTextFromPossibleGloballyRoutableContactsLink(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	personLookupText string,
) (string, error) {
	personLookupText, inputWasGloballyRoutableTrawlLinkForContacts, err :=
		trawlkit.ReplaceGloballyRoutableTrawlLinkWithLocalShortReferenceForSelectedTrawlerOrKeepFreeFormArgument(
			personLookupText,
			archive.AppID,
		)
	if err != nil {
		return "", err
	}
	if !inputWasGloballyRoutableTrawlLinkForContacts {
		return personLookupText, nil
	}
	canonicalPersonRecordReferences, err := req.ResolveShortReference(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(personLookupText),
	)
	if err != nil {
		return "", err
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(canonicalPersonRecordReferences[0]), nil
}

func (a *App) loadOpenPerson(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (openedPersonValuesLoadedFromContactsArchive, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return openedPersonValuesLoadedFromContactsArchive{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolved, err := resolveOpenRef(ctx, req, localShortReference)
	if err != nil {
		return openedPersonValuesLoadedFromContactsArchive{}, err
	}
	person, err := st.FindPerson(ctx, resolved)
	if err != nil {
		return openedPersonValuesLoadedFromContactsArchive{}, personLookupError(err)
	}
	return openedPersonValuesLoadedFromContactsArchive{
		canonicalPersonRecordReference: archive.PersonRef(person.ID),
		archivedPerson:                 person,
	}, nil
}

func (a *App) RecordReferencesForShortReferenceAssignment(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) ([]trawlkit.ShortReferenceAssignmentCandidate, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, err
	}
	return st.RecordReferencesForShortReferenceAssignment(ctx)
}

func resolveOpenRef(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (string, error) {
	localShortReferenceText := trawlkit.LocalTrawlerShortReferenceText(localShortReference)
	matches, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", usageError(output.HumanFacingErrorMessage(
			fmt.Sprintf("The Contacts link %q matches more than one person.", localShortReferenceText),
		))
	}
	if err != nil {
		return "", err
	}
	canonicalPersonRecordReference := trawlkit.CanonicalArchiveRecordReferenceText(matches[0])
	if id, ok := archive.PersonIDFromRef(canonicalPersonRecordReference); ok {
		return id, nil
	}
	return "", usageError(output.HumanFacingErrorMessage(
		fmt.Sprintf("The Contacts link %q is not valid.", localShortReferenceText),
	))
}

func archiveErr(err error) error {
	return err
}

func usageError(err error) error {
	return output.UsageError{Err: err}
}

type personNotFoundContractError struct {
	personNotFoundError error
}

func (e personNotFoundContractError) Error() string {
	return e.personNotFoundError.Error()
}

func (e personNotFoundContractError) Unwrap() error {
	return e.personNotFoundError
}

func (e personNotFoundContractError) ExitCode() int {
	return 1
}

func (e personNotFoundContractError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    "not_found",
		Message: e.Error(),
	}
}

func personLookupError(err error) error {
	switch {
	case errors.Is(err, archive.ErrPersonNotFound):
		return personNotFoundContractError{personNotFoundError: err}
	case errors.Is(err, archive.ErrPersonSearchMatchedMoreThanOnePerson):
		return usageError(err)
	}
	return err
}
