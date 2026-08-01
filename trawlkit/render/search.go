package render

import (
	"fmt"
	"io"
	"strings"

	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
)

const (
	searchResultMaximumWrappedLines                    = 2
	searchResultMaximumPeoplePerRoleInHumanOutput      = 2
	searchResultMaximumPeopleWithoutRolesInHumanOutput = 3
)

type SearchResults struct {
	Heading                               string
	Hints                                 []string
	Presentations                         []SearchResultPresentationForRootTrawlHumanOutput
	Empty                                 string
	SearchWasExplicitlyScopedToOneTrawler bool
}

type SearchResultPresentationForRootTrawlHumanOutput struct {
	SearchMatchPresentation   *search.SearchMatchPresentation
	GloballyRoutableTrawlLink *identity.GloballyRoutableTrawlLink
}

func SearchResultsEmptySentence(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "No matching results."
	}
	return fmt.Sprintf("No results match %q.", query)
}

func WriteSearchResults(writer io.Writer, searchResults SearchResults) error {
	if len(searchResults.Presentations) == 0 {
		emptySentence := strings.TrimSpace(searchResults.Empty)
		if emptySentence == "" {
			emptySentence = "No matching results."
		}
		_, err := fmt.Fprintln(writer, emptySentence)
		return err
	}
	if err := writeListIntro(writer, searchResults.Heading, searchResults.Hints); err != nil {
		return err
	}
	searchResultRows := make([]searchResultRow, 0, len(searchResults.Presentations))
	for _, presentation := range searchResults.Presentations {
		searchResultRows = append(searchResultRows, searchResultRowFromPresentation(presentation))
	}
	return writeGroupedSearchResultRows(
		writer,
		searchResultRows,
		readableTableOutputWidth(writer),
		searchResults.SearchWasExplicitlyScopedToOneTrawler,
		searchResultRowsRepeatOneCommonRecordKindInWhatField(searchResultRows),
	)
}

func SearchResultsHeading(query, who string, shown, total int) string {
	query = strings.TrimSpace(query)
	who = strings.TrimSpace(who)
	shownText := FormatInteger(int64(shown))
	totalText := FormatInteger(int64(total))
	switch {
	case query != "" && who != "":
		return fmt.Sprintf("Search %q with %s: showing %s of %s.", query, who, shownText, totalText)
	case query != "":
		return fmt.Sprintf("Search %q: showing %s of %s.", query, shownText, totalText)
	case who != "":
		return fmt.Sprintf("Search with %s: showing %s of %s.", who, shownText, totalText)
	default:
		return fmt.Sprintf("Search filters: showing %s of %s.", shownText, totalText)
	}
}

type searchResultRow struct {
	when                          string
	registeredTrawlerDisplayName  string
	link                          string
	what                          string
	who                           string
	where                         string
	match                         string
	matchingRecordKindDisplayName string
}

func searchResultRowFromPresentation(
	presentationForRootTrawlHumanOutput SearchResultPresentationForRootTrawlHumanOutput,
) searchResultRow {
	presentation := presentationForRootTrawlHumanOutput.SearchMatchPresentation
	if presentation == nil {
		return searchResultRow{}
	}
	matchingRecordNameOrKindDisplayName := strings.TrimSpace(presentation.GetMatchingRecordDisplayName())
	if matchingRecordNameOrKindDisplayName == "" {
		matchingRecordNameOrKindDisplayName = strings.TrimSpace(presentation.GetMatchingRecordKindDisplayName())
	}
	return searchResultRow{
		when:                         searchResultAssociatedTime(presentation),
		registeredTrawlerDisplayName: strings.TrimSpace(presentation.GetRegisteredTrawlerDisplayName()),
		link:                         globallyRoutableTrawlLinkText(presentationForRootTrawlHumanOutput.GloballyRoutableTrawlLink),
		what:                         matchingRecordNameOrKindDisplayName,
		who:                          searchResultPeople(presentation.GetPeopleRelatedToMatchingRecord()),
		where: searchResultPhysicalPlacesAndDigitalContainers(
			presentation.GetPhysicalPlaceNamesSpecificToBroadest(),
			presentation.GetDigitalContainerNamesNearestToBroadest(),
		),
		match:                         searchResultMatchingText(presentation.GetSearchMatchTextFieldsInDisplayOrder()),
		matchingRecordKindDisplayName: strings.TrimSpace(presentation.GetMatchingRecordKindDisplayName()),
	}
}

func searchResultAssociatedTime(searchMatchPresentation *search.SearchMatchPresentation) string {
	switch associatedTime := searchMatchPresentation.GetMatchingRecordAssociatedTime().GetArchiveRecordAssociatedTime().(type) {
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		if associatedTime.ExactTime == nil || !associatedTime.ExactTime.IsValid() {
			return ""
		}
		return associatedTime.ExactTime.AsTime().Local().Format("2006-01-02 15:04")
	case *presentation.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
		calendarDate := associatedTime.CalendarDate
		if calendarDate == nil {
			return ""
		}
		return fmt.Sprintf(
			"%04d-%02d-%02d",
			calendarDate.GetCalendarYear(),
			calendarDate.GetCalendarMonthNumber(),
			calendarDate.GetCalendarDayOfMonth(),
		)
	default:
		return ""
	}
}

func searchResultPeople(people []*person.PersonRelatedToArchiveRecord) string {
	senders := searchResultPeopleWithRole(people, person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER)
	recipients := searchResultPeopleWithRole(people, person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT)
	if len(senders) > 0 && len(recipients) > 0 {
		return searchResultPeoplePreview(senders, searchResultMaximumPeoplePerRoleInHumanOutput) +
			" → " +
			searchResultPeoplePreview(recipients, searchResultMaximumPeoplePerRoleInHumanOutput)
	}
	displayNames := make([]string, 0, len(people))
	seenDisplayNames := make(map[string]struct{}, len(people))
	for _, person := range people {
		if person == nil {
			continue
		}
		displayName := strings.TrimSpace(person.GetPersonDisplayName())
		normalizedDisplayName := strings.ToLower(displayName)
		if displayName == "" {
			continue
		}
		if _, alreadyIncluded := seenDisplayNames[normalizedDisplayName]; alreadyIncluded {
			continue
		}
		seenDisplayNames[normalizedDisplayName] = struct{}{}
		displayNames = append(displayNames, displayName)
	}
	return searchResultPeoplePreview(displayNames, searchResultMaximumPeopleWithoutRolesInHumanOutput)
}

func searchResultPeoplePreview(displayNames []string, maximumDisplayNames int) string {
	if len(displayNames) <= maximumDisplayNames {
		return strings.Join(displayNames, ", ")
	}
	return strings.Join(displayNames[:maximumDisplayNames], ", ") +
		", +" +
		FormatInteger(int64(len(displayNames)-maximumDisplayNames))
}

func searchResultPeopleWithRole(
	people []*person.PersonRelatedToArchiveRecord,
	role person.PersonRoleInArchiveRecord,
) []string {
	displayNames := make([]string, 0, len(people))
	for _, person := range people {
		if person == nil || person.GetPersonRoleInArchiveRecord() != role {
			continue
		}
		if displayName := strings.TrimSpace(person.GetPersonDisplayName()); displayName != "" {
			displayNames = append(displayNames, displayName)
		}
	}
	return displayNames
}

func searchResultPhysicalPlacesAndDigitalContainers(
	physicalPlaceNames []string,
	digitalContainerNames []string,
) string {
	physicalPlaces := strings.Join(nonEmptySearchResultNames(physicalPlaceNames), " › ")
	digitalContainers := strings.Join(nonEmptySearchResultNames(digitalContainerNames), " › ")
	switch {
	case physicalPlaces != "" && digitalContainers != "":
		return physicalPlaces + " · " + digitalContainers
	case physicalPlaces != "":
		return physicalPlaces
	default:
		return digitalContainers
	}
}

func nonEmptySearchResultNames(names []string) []string {
	nonEmptyNames := make([]string, 0, len(names))
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			nonEmptyNames = append(nonEmptyNames, name)
		}
	}
	return nonEmptyNames
}

func searchResultMatchingText(matchingTextValues []*search.SearchMatchTextField) string {
	type displayedMatchingTextField struct {
		fieldName     string
		displayedText string
	}
	displayedMatchingTextFields := make([]displayedMatchingTextField, 0, len(matchingTextValues))
	for _, matchingText := range matchingTextValues {
		if matchingText == nil {
			continue
		}
		var displayedText strings.Builder
		matchingRecordTextFragments := matchingText.GetSearchMatchTextFragmentsInDisplayOrder()
		for _, fragment := range matchingRecordTextFragments {
			if fragment == nil {
				continue
			}
			if fragment.GetSearchMatchTextFragmentMatchesSearchQuery() {
				displayedText.WriteString("‹")
			}
			displayedText.WriteString(fragment.GetSearchMatchTextFragmentContent())
			if fragment.GetSearchMatchTextFragmentMatchesSearchQuery() {
				displayedText.WriteString("›")
			}
		}
		matchingRecordTextFieldName := strings.TrimSpace(matchingText.GetSearchMatchTextFieldName())
		matchingRecordTextFieldContent := strings.TrimSpace(displayedText.String())
		if matchingRecordTextFieldName != "" || matchingRecordTextFieldContent != "" {
			displayedMatchingTextFields = append(displayedMatchingTextFields, displayedMatchingTextField{
				fieldName:     matchingRecordTextFieldName,
				displayedText: matchingRecordTextFieldContent,
			})
		}
	}
	if len(displayedMatchingTextFields) == 1 {
		displayedMatchingTextField := displayedMatchingTextFields[0]
		if displayedMatchingTextField.displayedText == "" {
			return displayedMatchingTextField.fieldName
		}
		if displayedMatchingTextField.fieldName != "" && !strings.EqualFold(displayedMatchingTextField.fieldName, "Message") {
			return displayedMatchingTextField.fieldName + ": " + displayedMatchingTextField.displayedText
		}
		return displayedMatchingTextField.displayedText
	}
	labelledMatchingTextValues := make([]string, 0, len(displayedMatchingTextFields))
	for _, displayedMatchingTextField := range displayedMatchingTextFields {
		displayedText := displayedMatchingTextField.displayedText
		if displayedText == "" {
			displayedText = displayedMatchingTextField.fieldName
		} else if displayedMatchingTextField.fieldName != "" {
			displayedText = displayedMatchingTextField.fieldName + ": " + displayedText
		}
		labelledMatchingTextValues = append(labelledMatchingTextValues, displayedText)
	}
	return strings.Join(labelledMatchingTextValues, " · ")
}

func searchResultRowsRepeatOneCommonRecordKindInWhatField(searchResultRows []searchResultRow) bool {
	commonRecordKindDisplayName := ""
	for _, searchResultRow := range searchResultRows {
		recordKindDisplayName := strings.TrimSpace(searchResultRow.matchingRecordKindDisplayName)
		if recordKindDisplayName == "" ||
			!strings.EqualFold(strings.TrimSpace(searchResultRow.what), recordKindDisplayName) {
			return false
		}
		if commonRecordKindDisplayName == "" {
			commonRecordKindDisplayName = recordKindDisplayName
			continue
		}
		if !strings.EqualFold(recordKindDisplayName, commonRecordKindDisplayName) {
			return false
		}
	}
	return len(searchResultRows) > 0 && commonRecordKindDisplayName != ""
}

func writeGroupedSearchResultRows(
	writer io.Writer,
	searchResultRows []searchResultRow,
	outputWidth int,
	hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler bool,
	hideWhatBecauseEveryRowRepeatsOneCommonRecordKind bool,
) error {
	for searchResultRowIndex, searchResultRow := range searchResultRows {
		if searchResultRowIndex > 0 {
			if _, err := fmt.Fprintln(writer); err != nil {
				return err
			}
		}
		matchedContent := strings.TrimSpace(searchResultRow.match)
		if matchedContent == "" {
			matchedContent = strings.TrimSpace(searchResultRow.what)
		}
		whenPrefix := ""
		if when := strings.TrimSpace(searchResultRow.when); when != "" {
			whenPrefix = when + "  "
		}
		matchedContentLines := WrapWithIndent(
			whenPrefix,
			matchedContent,
			outputWidth,
			strings.Repeat(" ", DisplayWidth(whenPrefix)),
		)
		for _, line := range clampLines(
			matchedContentLines,
			searchResultMaximumWrappedLines,
			outputWidth,
		) {
			if _, err := fmt.Fprintln(writer, line); err != nil {
				return err
			}
		}

		contextInScanOrder := searchResultContextInScanOrder(
			writer,
			searchResultRow,
			hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler,
			hideWhatBecauseEveryRowRepeatsOneCommonRecordKind,
		)
		for _, line := range WrapWithIndent(
			"  ",
			strings.Join(contextInScanOrder, " · "),
			outputWidth,
			"  ",
		) {
			if _, err := fmt.Fprintln(writer, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func searchResultContextInScanOrder(
	writer io.Writer,
	searchResultRow searchResultRow,
	hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler bool,
	hideWhatBecauseEveryRowRepeatsOneCommonRecordKind bool,
) []string {
	contextInScanOrder := make([]string, 0, 5)
	if who := strings.TrimSpace(searchResultRow.who); who != "" {
		contextInScanOrder = append(contextInScanOrder, "who "+who)
	}
	if where := strings.TrimSpace(searchResultRow.where); where != "" {
		contextInScanOrder = append(contextInScanOrder, "where "+where)
	}
	if !hideWhatBecauseEveryRowRepeatsOneCommonRecordKind {
		if what := strings.TrimSpace(searchResultRow.what); what != "" && what != strings.TrimSpace(searchResultRow.match) {
			contextInScanOrder = append(contextInScanOrder, "what "+what)
		}
	}
	if !hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler {
		if trawler := strings.TrimSpace(searchResultRow.registeredTrawlerDisplayName); trawler != "" {
			contextInScanOrder = append(contextInScanOrder, trawler)
		}
	}
	if link := strings.TrimSpace(searchResultRow.link); link != "" {
		contextInScanOrder = append(contextInScanOrder, trawlCommandLineForDisplay(writer, []string{"open", link}))
	}
	return contextInScanOrder
}
