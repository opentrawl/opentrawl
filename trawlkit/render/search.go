package render

import (
	"fmt"
	"io"
	"strings"

	identityv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity/v1"
	personv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person/v1"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
	searchv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search/v1"
)

const (
	searchResultMatchMinimumWidth         = 16
	searchResultOtherFlexibleMinimumWidth = 5
	searchResultMaximumWrappedLines       = 2
)

type SearchResults struct {
	Heading                               string
	Hints                                 []string
	Presentations                         []SearchResultPresentationForRootTrawlHumanOutput
	Empty                                 string
	SearchWasExplicitlyScopedToOneTrawler bool
}

type SearchResultPresentationForRootTrawlHumanOutput struct {
	SearchMatchPresentation   *searchv1.SearchMatchPresentation
	GloballyRoutableTrawlLink *identityv1.GloballyRoutableTrawlLink
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
	shownSearchResultColumnSpecifications := searchResultColumnSpecifications(
		searchResultRows,
		searchResults.SearchWasExplicitlyScopedToOneTrawler,
		searchResultRowsRepeatOneCommonRecordKindInWhatColumn(searchResultRows),
	)
	outputWidth := OutputWidth(writer)
	columns := searchResultRenderColumns(shownSearchResultColumnSpecifications, searchResultRows, outputWidth)
	if tableNeedsFieldValueRows(columns, outputWidth) {
		rows := make([][]string, 0, len(searchResultRows))
		for _, searchResultRow := range searchResultRows {
			rows = append(rows, searchResultTableRow(searchResultRow, shownSearchResultColumnSpecifications))
		}
		return writeFieldValueRows(writer, columns, rows)
	}
	if err := writeRenderHeader(writer, columns); err != nil {
		return err
	}
	for _, searchResultRow := range searchResultRows {
		if err := writeRenderRow(writer, columns, searchResultTableRow(searchResultRow, shownSearchResultColumnSpecifications)); err != nil {
			return err
		}
	}
	return nil
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

type searchResultColumnSpecification struct {
	humanOutputColumn          renderColumn
	textFromSearchResultRow    func(searchResultRow) string
	alwaysShownInSearchResults bool
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

func searchResultAssociatedTime(presentation *searchv1.SearchMatchPresentation) string {
	switch associatedTime := presentation.GetMatchingRecordAssociatedTime().GetArchiveRecordAssociatedTime().(type) {
	case *presentationv1.ArchiveRecordAssociatedTimeForDisplay_ExactTime:
		if associatedTime.ExactTime == nil || !associatedTime.ExactTime.IsValid() {
			return ""
		}
		return associatedTime.ExactTime.AsTime().Local().Format("2006-01-02 15:04")
	case *presentationv1.ArchiveRecordAssociatedTimeForDisplay_CalendarDate:
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

func searchResultPeople(people []*personv1.PersonRelatedToArchiveRecord) string {
	senders := searchResultPeopleWithRole(people, personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER)
	recipients := searchResultPeopleWithRole(people, personv1.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_RECIPIENT)
	if len(senders) > 0 && len(recipients) > 0 {
		return strings.Join(senders, ", ") + " → " + strings.Join(recipients, ", ")
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
	return strings.Join(displayNames, ", ")
}

func searchResultPeopleWithRole(
	people []*personv1.PersonRelatedToArchiveRecord,
	role personv1.PersonRoleInArchiveRecord,
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

func searchResultMatchingText(matchingTextValues []*searchv1.SearchMatchTextField) string {
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

func searchResultColumnSpecifications(
	searchResultRows []searchResultRow,
	hideTrawlerColumnBecauseSearchWasExplicitlyScopedToOneTrawler bool,
	hideWhatColumnBecauseEveryRowRepeatsOneCommonRecordKind bool,
) []searchResultColumnSpecification {
	availableColumnSpecifications := []searchResultColumnSpecification{
		{
			humanOutputColumn:       renderColumn{Header: "when", KeepWholeTokensWhenTerminalWidthAllows: true},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.when },
		},
		{
			humanOutputColumn:       renderColumn{Header: "trawler", KeepWholeTokensWhenTerminalWidthAllows: true},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.registeredTrawlerDisplayName },
		},
		{
			humanOutputColumn:          renderColumn{Header: "link", NeverTruncateCellValues: true},
			textFromSearchResultRow:    func(searchResultRow searchResultRow) string { return searchResultRow.link },
			alwaysShownInSearchResults: true,
		},
		{
			humanOutputColumn: renderColumn{
				Header:       "what",
				MinimumWidth: searchResultOtherFlexibleMinimumWidth,
				Wrap:         true,
				Clamp:        searchResultMaximumWrappedLines,
				HideBeforeTruncatingOtherColumnsBelowMinimumWidth: true,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.what },
		},
		{
			humanOutputColumn: renderColumn{
				Header:       "who",
				MinimumWidth: searchResultOtherFlexibleMinimumWidth,
				Wrap:         true,
				Clamp:        searchResultMaximumWrappedLines,
				HideBeforeTruncatingOtherColumnsBelowMinimumWidth: true,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.who },
		},
		{
			humanOutputColumn: renderColumn{
				Header:       "where",
				MinimumWidth: searchResultOtherFlexibleMinimumWidth,
				Wrap:         true,
				Clamp:        searchResultMaximumWrappedLines,
				HideBeforeTruncatingOtherColumnsBelowMinimumWidth: true,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.where },
		},
		{
			humanOutputColumn:       renderColumn{Header: "match", MinimumWidth: searchResultMatchMinimumWidth, Wrap: true, Clamp: searchResultMaximumWrappedLines},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.match },
		},
	}

	shownColumnSpecifications := make([]searchResultColumnSpecification, 0, len(availableColumnSpecifications))
	for _, columnSpecification := range availableColumnSpecifications {
		columnHeader := columnSpecification.humanOutputColumn.Header
		if hideTrawlerColumnBecauseSearchWasExplicitlyScopedToOneTrawler && columnHeader == "trawler" {
			continue
		}
		if hideWhatColumnBecauseEveryRowRepeatsOneCommonRecordKind && columnHeader == "what" {
			continue
		}
		if columnSpecification.alwaysShownInSearchResults ||
			searchResultRowsContain(searchResultRows, columnSpecification.textFromSearchResultRow) {
			shownColumnSpecifications = append(shownColumnSpecifications, columnSpecification)
		}
	}
	return shownColumnSpecifications
}

func searchResultRowsRepeatOneCommonRecordKindInWhatColumn(searchResultRows []searchResultRow) bool {
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

func searchResultRowsContain(
	searchResultRows []searchResultRow,
	textFromSearchResultRow func(searchResultRow) string,
) bool {
	for _, searchResultRow := range searchResultRows {
		if strings.TrimSpace(textFromSearchResultRow(searchResultRow)) != "" {
			return true
		}
	}
	return false
}

func searchResultRenderColumns(
	columnSpecifications []searchResultColumnSpecification,
	searchResultRows []searchResultRow,
	outputWidth int,
) []renderColumn {
	columns := make([]renderColumn, 0, len(columnSpecifications))
	for _, columnSpecification := range columnSpecifications {
		naturalWidth := naturalSearchResultColumnWidth(columnSpecification, searchResultRows)
		column := columnSpecification.humanOutputColumn
		column.Width = naturalWidth
		if column.KeepWholeTokensWhenTerminalWidthAllows || column.NeverTruncateCellValues {
			column.MinimumWidth = naturalWidth
		} else {
			column.MinimumWidth = max(column.MinimumWidth, DisplayWidth(column.Header))
		}
		columns = append(columns, column)
	}
	fitRenderColumns(columns, outputWidth)
	return columns
}

func naturalSearchResultColumnWidth(
	columnSpecification searchResultColumnSpecification,
	searchResultRows []searchResultRow,
) int {
	columnWidth := DisplayWidth(columnSpecification.humanOutputColumn.Header)
	for _, searchResultRow := range searchResultRows {
		cellWidth := DisplayWidth(columnSpecification.textFromSearchResultRow(searchResultRow))
		if cellWidth > columnWidth {
			columnWidth = cellWidth
		}
	}
	return columnWidth
}

func searchResultTableRow(
	searchResultRow searchResultRow,
	columnSpecifications []searchResultColumnSpecification,
) []string {
	tableRow := make([]string, 0, len(columnSpecifications))
	for _, columnSpecification := range columnSpecifications {
		tableRow = append(tableRow, columnSpecification.textFromSearchResultRow(searchResultRow))
	}
	return tableRow
}
