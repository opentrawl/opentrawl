package contacts

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/apple"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/control"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/output"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type App struct {
	readApple func(context.Context) ([]apple.Contact, error)
}

type Crawler = App

var (
	_ trawlkit.Trawler          = (*App)(nil)
	_ trawlkit.Updater          = (*App)(nil)
	_ trawlkit.Searcher         = (*App)(nil)
	_ trawlkit.WhoMatcher       = (*App)(nil)
	_ trawlkit.PeopleReconciler = (*App)(nil)
)

func New() *App {
	return &App{readApple: apple.ReadSystem}
}

func (a *App) RegisteredTrawlerDeclaration() trawlkit.RegisteredTrawlerDeclaration {
	return trawlkit.RegisteredTrawlerDeclaration{
		RegisteredTrawler:            trawlkit.NewRegisteredTrawlerIdentity(archive.AppID),
		RegisteredTrawlerCommandName: "contacts",
		RegisteredTrawlerDisplayName: archive.DisplayName,
		RegisteredTrawlerPrivacyBoundary: control.Privacy{
			Reads:           "Apple Contacts on your Mac.",
			LeavesMachine:   "Nothing. Updates and searches stay on your Mac.",
			NetworkRequests: "None. Contacts is local.",
		},
	}
}

func (*App) LoadTrawlerConfiguration(trawlkit.TrawlerConfigurationFilePath) error {
	return nil
}

func (a *App) TrawlerCommands() []trawlkit.TrawlerCommand {
	return []trawlkit.TrawlerCommand{
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_STATUS, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_UPDATE, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_SEARCH, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_OPEN, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		{SharedTrawlerOperation: federation.SharedTrawlerOperation_SHARED_TRAWLER_OPERATION_WHO, TrawlerCommandHelpListing: trawlkit.TrawlerCommandHiddenFromHumanHelp},
		personListCommand(),
		personShowCommand(),
		personAnnotationCommand(),
	}
}

func (a *App) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
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
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "people", ArchiveContentKindDisplayName: "people", ArchiveContentCount: uint64(archiveStatus.People)},
	}
	if !archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}

func (a *App) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
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
		return &search.TrawlerSearchResponse{}, nil
	}
	archiveSearchResults, totalSearchMatches, err := archiveStore.Search(ctx, normalizedSearchQuery, archive.SearchOptions{
		Limit:  query.Limit,
		After:  query.After,
		Before: query.Before,
	})
	if err != nil {
		return nil, err
	}
	trawlerSearchMatches := make([]*search.TrawlerSearchMatch, 0, len(archiveSearchResults))
	for _, archiveSearchResult := range archiveSearchResults {
		identifierValuesNotSuitableAsPersonDisplayNames := append([]string(nil), archiveSearchResult.PersonTechnicalIdentifiers...)
		identifierValuesNotSuitableAsPersonDisplayNames = append(identifierValuesNotSuitableAsPersonDisplayNames, contactSearchResultIdentifierValuesNotSuitableAsPersonDisplayNames(archiveSearchResult.Matches)...)
		identifierValuesNotSuitableAsPersonDisplayNames = append(identifierValuesNotSuitableAsPersonDisplayNames, archiveSearchResult.PersonID)
		searchMatchTextFields := contactSearchMatchTextFields(archiveSearchResult.Matches, identifierValuesNotSuitableAsPersonDisplayNames)
		name := humanReadablePersonDisplayName(archiveSearchResult.Who, archiveSearchResult.AlternativePersonNames, identifierValuesNotSuitableAsPersonDisplayNames)
		searchMatchPresentation := &search.SearchMatchPresentation{
			MatchingRecordKindDisplayName:          "person",
			MatchingRecordDisplayName:              name,
			PeopleRelatedToMatchingRecord:          contactSearchResultPeopleRelatedToMatchingRecord(name),
			DigitalContainerNamesNearestToBroadest: contactSearchResultDigitalContainerNames(archiveSearchResult),
			PhysicalPlaceNamesSpecificToBroadest:   contactSearchResultPhysicalPlaceNames(archiveSearchResult),
			SearchMatchTextFieldsInDisplayOrder:    searchMatchTextFields,
		}
		if !archiveSearchResult.Time.IsZero() {
			searchMatchPresentation.MatchingRecordAssociatedTime = &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(archiveSearchResult.Time)},
			}
		}
		trawlerSearchMatches = append(trawlerSearchMatches, &search.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archiveSearchResult.Ref),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(archiveSearchResult.AnchorID),
			SearchMatchPresentation:  searchMatchPresentation,
		})
	}
	moreSearchMatchesExist := len(trawlerSearchMatches) < totalSearchMatches
	return &search.TrawlerSearchResponse{
		TrawlerSearchMatchesInDisplayOrder: trawlerSearchMatches,
		TotalSearchMatches:                 uint64(totalSearchMatches),
		MoreSearchMatchesExist:             moreSearchMatchesExist,
	}, nil
}

func contactSearchMatchTextFields(matches []archive.SearchMatch, identifierValuesNotSuitableAsPersonDisplayNames []string) []*search.SearchMatchTextField {
	searchMatchTextFields := make([]*search.SearchMatchTextField, 0, len(matches))
	seenNormalizedHumanEvidenceText := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		searchMatchText := contactSearchResultMatchText(match)
		searchMatchTextFieldName := ""
		switch match.Field {
		case openrecord.PersonDisplayNameAnchorID:
			if model.PersonDisplayNameIsSuitableForHumanPresentation(searchMatchText, identifierValuesNotSuitableAsPersonDisplayNames) {
				searchMatchTextFieldName = "Name"
			}
		case "sort_name":
			if model.PersonDisplayNameIsSuitableForHumanPresentation(searchMatchText, identifierValuesNotSuitableAsPersonDisplayNames) {
				searchMatchTextFieldName = "Sort name"
			}
		case "annotation":
			searchMatchTextFieldName = "Annotation"
		case "body":
			searchMatchTextFieldName = "Contact note"
		case openrecord.PersonAlternativeDisplayNameAnchorID:
			if model.PersonDisplayNameIsSuitableForHumanPresentation(searchMatchText, identifierValuesNotSuitableAsPersonDisplayNames) {
				searchMatchTextFieldName = "Known as"
			}
		case "tag":
			searchMatchTextFieldName = "Tag"
		case openrecord.PersonEmailAddressAnchorID:
			searchMatchTextFieldName = "Email"
		case openrecord.PersonPhoneNumberAnchorID:
			searchMatchTextFieldName = "Phone"
		case openrecord.PersonAccountIdentifierAnchorID:
			searchMatchTextFieldName = "Account"
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
			[]*search.SearchMatchTextFragment,
			0,
			len(match.Runs),
		)
		for _, run := range match.Runs {
			if run.Text == "" {
				continue
			}
			searchMatchTextFragments = append(searchMatchTextFragments, &search.SearchMatchTextFragment{
				SearchMatchTextFragmentContent:            run.Text,
				SearchMatchTextFragmentMatchesSearchQuery: run.Matched,
			})
		}
		searchMatchTextFields = append(searchMatchTextFields, &search.SearchMatchTextField{
			SearchMatchTextFieldName:               searchMatchTextFieldName,
			SearchMatchTextFragmentsInDisplayOrder: searchMatchTextFragments,
		})
	}
	return searchMatchTextFields
}

func contactSearchResultPeopleRelatedToMatchingRecord(personDisplayName string) []*person.PersonRelatedToArchiveRecord {
	personDisplayName = strings.Join(strings.Fields(personDisplayName), " ")
	if personDisplayName == "" {
		return nil
	}
	return []*person.PersonRelatedToArchiveRecord{{
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
		accountProviderName := match.AccountProviderName
		if accountProviderName == "" && match.Field == "note_source" {
			accountProviderName = contactSearchResultMatchText(match)
		}
		if accountProviderDisplayName := contactSearchResultAccountProviderDisplayName(accountProviderName); accountProviderDisplayName != "" {
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
	if !model.PersonDisplayNameIsSuitableForHumanPresentation(accountProviderName, nil) {
		return ""
	}
	return accountProviderName
}

func contactSearchResultIdentifierValuesNotSuitableAsPersonDisplayNames(matches []archive.SearchMatch) []string {
	identifierValuesNotSuitableAsPersonDisplayNames := make([]string, 0, len(matches))
	for _, match := range matches {
		switch match.Field {
		case openrecord.PersonAccountIdentifierAnchorID, "identifier":
			identifierValuesNotSuitableAsPersonDisplayNames = append(identifierValuesNotSuitableAsPersonDisplayNames, contactSearchResultMatchText(match))
		}
	}
	return identifierValuesNotSuitableAsPersonDisplayNames
}

func contactSearchResultMatchText(match archive.SearchMatch) string {
	var text strings.Builder
	for _, run := range match.Runs {
		text.WriteString(run.Text)
	}
	return text.String()
}

func (a *App) Who(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, personQuery string) (*person.TrawlerPersonMatchResponse, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	personQuery, err = resolvePersonLookupTextFromPossibleGloballyRoutableContactsLink(ctx, req, personQuery)
	if err != nil {
		return nil, err
	}
	var candidates []archive.ResolvedPersonMatchCandidate
	if strings.HasPrefix(personQuery, archive.AppID+":person/") {
		candidates, err = st.ResolveCanonicalPersonRecordReference(ctx, personQuery)
	} else {
		candidates, err = st.ResolvePeople(ctx, personQuery)
	}
	if err != nil {
		return nil, err
	}
	out := make([]*person.TrawlerPersonMatchCandidate, 0, len(candidates))
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
			[]*person.PersonMatchFactsFromTrawler,
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
		personMatchCandidate := &person.TrawlerPersonMatchCandidate{
			PersonDisplayName: personDisplayName,
			AlternativePersonDisplayNames: humanReadableAlternativePersonDisplayNames(
				candidate.PersonDisplayName,
				candidate.AlternativePersonDisplayNames,
				personDisplayName,
				exactPersonFilterIdentifiers,
			),
			PersonNameOrHumanReadableContactValueThatMatchedQuery: candidate.PersonNameOrHumanReadableContactValueThatMatchedQuery,
			PersonMatchFactsFromTrawlers:                          personMatchFactsFromTrawlers,
			PersonMessageCountsFromTrawlerArchives: normalizedPersonMessageCountsFromTrawlerArchives(
				candidate.PersonMessageCountsFromTrawlerArchives,
			),
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
	return &person.TrawlerPersonMatchResponse{PersonMatchCandidates: out}, nil
}

func normalizedPersonMessageCountsFromTrawlerArchives(
	messageCounts []*person.PersonMessageCountFromTrawlerArchive,
) []*person.PersonMessageCountFromTrawlerArchive {
	normalizedMessageCounts := make(
		[]*person.PersonMessageCountFromTrawlerArchive,
		0,
		len(messageCounts),
	)
	for _, messageCount := range messageCounts {
		registeredTrawlerIdentity := registeredTrawlerIdentityForContactsArchiveContributor(
			trawlkit.RegisteredTrawlerIdentityText(messageCount.GetRegisteredTrawler()),
		)
		if registeredTrawlerIdentity == "" ||
			messageCount.GetMessageCountInvolvingPersonInTrawlerArchive() == 0 {
			continue
		}
		normalizedMessageCounts = append(
			normalizedMessageCounts,
			&person.PersonMessageCountFromTrawlerArchive{
				RegisteredTrawler: trawlkit.NewRegisteredTrawlerIdentity(
					registeredTrawlerIdentity,
				),
				MessageCountInvolvingPersonInTrawlerArchive: messageCount.
					GetMessageCountInvolvingPersonInTrawlerArchive(),
			},
		)
	}
	return normalizedMessageCounts
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
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", personSelectionContractError{
			personSelectionError:       output.HumanFacingErrorMessage("No person has that link."),
			personSelectionFailureCode: "not_found",
		}
	}
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

type personSelectionContractError struct {
	personSelectionError       error
	personSelectionFailureCode string
}

func (e personSelectionContractError) Error() string {
	return e.personSelectionError.Error()
}

func (e personSelectionContractError) Unwrap() error {
	return e.personSelectionError
}

func (e personSelectionContractError) ExitCode() int {
	return 1
}

func (e personSelectionContractError) ErrorDescription() output.ErrorDescription {
	return output.ErrorDescription{
		Code:    e.personSelectionFailureCode,
		Message: e.Error(),
	}
}

func personLookupError(err error) error {
	switch {
	case errors.Is(err, archive.ErrPersonNotFound):
		return personSelectionContractError{
			personSelectionError:       err,
			personSelectionFailureCode: "not_found",
		}
	case errors.Is(err, archive.ErrPersonSearchMatchedMoreThanOnePerson):
		return personSelectionContractError{
			personSelectionError:       err,
			personSelectionFailureCode: "ambiguous",
		}
	}
	return err
}
