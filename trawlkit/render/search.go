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
	searchResultMatchMinimumWidth                      = 24
	searchResultWhoMaximumWidth                        = 32
	searchResultWhereMaximumWidth                      = 24
	searchResultWhatMaximumWidth                       = 32
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
		searchResultRow := searchResultRowFromPresentation(presentation)
		if searchResultRow.globallyRoutableTrawlLink != "" {
			searchResultRow.openRecordCommand = trawlCommandLineForDisplay(
				writer,
				[]string{"open", searchResultRow.globallyRoutableTrawlLink},
			)
		}
		searchResultRows = append(searchResultRows, searchResultRow)
	}
	outputWidth := OutputWidth(writer)
	hideWhatBecauseEveryRowRepeatsOneCommonRecordKind := searchResultRowsRepeatOneCommonRecordKindInWhatField(searchResultRows)
	wideColumnSpecifications := wideSearchResultColumnSpecifications(
		searchResultRows,
		searchResults.SearchWasExplicitlyScopedToOneTrawler,
		hideWhatBecauseEveryRowRepeatsOneCommonRecordKind,
	)
	wideColumns := searchResultRenderColumns(wideColumnSpecifications, searchResultRows, outputWidth)
	if !wideSearchResultColumnsShowAllPopulatedOptionalHumanContext(wideColumns) {
		return writeSearchResultRowsWithAttachedContext(
			writer,
			searchResultRows,
			outputWidth,
			searchResults.SearchWasExplicitlyScopedToOneTrawler,
			hideWhatBecauseEveryRowRepeatsOneCommonRecordKind,
		)
	}
	return writeWideSearchResultRows(
		writer,
		searchResultRows,
		wideColumnSpecifications,
		wideColumns,
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
	globallyRoutableTrawlLink     string
	openRecordCommand             string
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
		globallyRoutableTrawlLink:    globallyRoutableTrawlLinkText(presentationForRootTrawlHumanOutput.GloballyRoutableTrawlLink),
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

type searchResultColumnSpecification struct {
	humanOutputColumn       renderColumn
	textFromSearchResultRow func(searchResultRow) string
}

func writeWideSearchResultRows(
	writer io.Writer,
	searchResultRows []searchResultRow,
	columnSpecifications []searchResultColumnSpecification,
	columns []renderColumn,
) error {
	if err := writeRenderHeader(writer, columns); err != nil {
		return err
	}
	for _, searchResultRow := range searchResultRows {
		if err := writeRenderRow(
			writer,
			columns,
			searchResultTableRow(searchResultRow, columnSpecifications),
		); err != nil {
			return err
		}
	}
	return nil
}

func wideSearchResultColumnsShowAllPopulatedOptionalHumanContext(columns []renderColumn) bool {
	for _, column := range columns {
		switch column.Header {
		case "who", "where", "what":
			if column.HiddenFromRenderedTable {
				return false
			}
		}
	}
	return true
}

func writeSearchResultRowsWithAttachedContext(
	writer io.Writer,
	searchResultRows []searchResultRow,
	outputWidth int,
	hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler bool,
	hideWhatBecauseEveryRowRepeatsOneCommonRecordKind bool,
) error {
	primaryColumnSpecifications := []searchResultColumnSpecification{
		{
			humanOutputColumn: renderColumn{
				Header:                                 "when",
				KeepWholeTokensWhenTerminalWidthAllows: true,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.when },
		},
		{
			humanOutputColumn: renderColumn{
				Header:       "match",
				MinimumWidth: searchResultMatchMinimumWidth,
				Wrap:         true,
				Clamp:        searchResultMaximumWrappedLines,
			},
			textFromSearchResultRow: searchResultPrimaryMatchedContent,
		},
	}
	columns := searchResultRenderColumns(primaryColumnSpecifications, searchResultRows, outputWidth)
	if err := writeRenderHeader(writer, columns); err != nil {
		return err
	}
	attachedContextIndent := strings.Repeat(
		" ",
		columns[0].Width+DisplayWidth(renderTableGap),
	)
	for _, searchResultRow := range searchResultRows {
		if err := writeRenderRow(
			writer,
			columns,
			searchResultTableRow(searchResultRow, primaryColumnSpecifications),
		); err != nil {
			return err
		}
		attachedContext := searchResultAttachedContextInScanOrder(
			searchResultRow,
			hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler,
			hideWhatBecauseEveryRowRepeatsOneCommonRecordKind,
		)
		if err := writeSearchResultAttachedContextAndOpenCommand(
			writer,
			attachedContextIndent,
			attachedContext,
			searchResultRow.openRecordCommand,
			outputWidth,
		); err != nil {
			return err
		}
	}
	return nil
}

func writeSearchResultAttachedContextAndOpenCommand(
	writer io.Writer,
	attachedContextIndent string,
	attachedContext []string,
	openRecordCommand string,
	outputWidth int,
) error {
	attachedContextText := strings.Join(attachedContext, " · ")
	openRecordCommand = strings.TrimSpace(openRecordCommand)
	contextAndOpenCommand := attachedContextText
	if contextAndOpenCommand != "" && openRecordCommand != "" {
		contextAndOpenCommand += " · " + openRecordCommand
	} else if openRecordCommand != "" {
		contextAndOpenCommand = openRecordCommand
	}
	if DisplayWidth(attachedContextIndent+contextAndOpenCommand) <= outputWidth {
		if contextAndOpenCommand == "" {
			return nil
		}
		_, err := fmt.Fprintln(writer, attachedContextIndent+contextAndOpenCommand)
		return err
	}
	if attachedContextText != "" {
		for _, line := range WrapWithIndent(
			attachedContextIndent,
			attachedContextText,
			outputWidth,
			attachedContextIndent,
		) {
			if _, err := fmt.Fprintln(writer, line); err != nil {
				return err
			}
		}
	}
	if openRecordCommand == "" {
		return nil
	}
	for _, line := range shellCommandLines(
		attachedContextIndent,
		attachedContextIndent,
		openRecordCommand,
		outputWidth,
	) {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}

func wideSearchResultColumnSpecifications(
	searchResultRows []searchResultRow,
	hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler bool,
	hideWhatBecauseEveryRowRepeatsOneCommonRecordKind bool,
) []searchResultColumnSpecification {
	availableColumnSpecifications := []searchResultColumnSpecification{
		{
			humanOutputColumn: renderColumn{
				Header:                                 "when",
				KeepWholeTokensWhenTerminalWidthAllows: true,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.when },
		},
		{
			humanOutputColumn: renderColumn{
				Header:       "match",
				MinimumWidth: searchResultMatchMinimumWidth,
				Wrap:         true,
				Clamp:        searchResultMaximumWrappedLines,
			},
			textFromSearchResultRow: searchResultPrimaryMatchedContent,
		},
		{
			humanOutputColumn:       renderColumn{Header: "who", MinimumWidth: searchResultWhoMaximumWidth / searchResultMaximumWrappedLines, Wrap: true, Clamp: searchResultMaximumWrappedLines},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.who },
		},
		{
			humanOutputColumn:       renderColumn{Header: "where", MinimumWidth: searchResultWhereMaximumWidth / searchResultMaximumWrappedLines, Wrap: true, Clamp: searchResultMaximumWrappedLines},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.where },
		},
		{
			humanOutputColumn:       renderColumn{Header: "what", MinimumWidth: searchResultWhatMaximumWidth / searchResultMaximumWrappedLines, Wrap: true, Clamp: searchResultMaximumWrappedLines},
			textFromSearchResultRow: searchResultWhatNotDuplicatedInPrimaryMatchedContent,
		},
		{
			humanOutputColumn: renderColumn{
				Header:                                 "trawler",
				KeepWholeTokensWhenTerminalWidthAllows: true,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.registeredTrawlerDisplayName },
		},
		{
			humanOutputColumn:       renderColumn{Header: "open", NeverTruncateCellValues: true},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.openRecordCommand },
		},
	}

	shownColumnSpecifications := make([]searchResultColumnSpecification, 0, len(availableColumnSpecifications))
	for _, columnSpecification := range availableColumnSpecifications {
		columnHeader := columnSpecification.humanOutputColumn.Header
		if hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler && columnHeader == "trawler" {
			continue
		}
		if hideWhatBecauseEveryRowRepeatsOneCommonRecordKind && columnHeader == "what" {
			continue
		}
		if columnHeader == "open" || searchResultRowsContain(searchResultRows, columnSpecification.textFromSearchResultRow) {
			shownColumnSpecifications = append(shownColumnSpecifications, columnSpecification)
		}
	}
	return shownColumnSpecifications
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
		if metadataMaximumWidth := searchResultMetadataMaximumWidth(column.Header); metadataMaximumWidth > 0 {
			naturalWidth = min(naturalWidth, metadataMaximumWidth)
		}
		column.Width = naturalWidth
		if column.KeepWholeTokensWhenTerminalWidthAllows || column.NeverTruncateCellValues {
			column.MinimumWidth = naturalWidth
		} else {
			column.MinimumWidth = max(column.MinimumWidth, DisplayWidth(column.Header))
		}
		columns = append(columns, column)
	}
	hideOptionalSearchResultColumnsBeforeCrushingMatch(columns, outputWidth)
	fitRenderColumns(columns, outputWidth)
	growSearchResultMatchColumnToUseRemainingOutputWidth(columns, outputWidth)
	return columns
}

func searchResultMetadataMaximumWidth(columnHeader string) int {
	switch columnHeader {
	case "who":
		return searchResultWhoMaximumWidth
	case "where":
		return searchResultWhereMaximumWidth
	case "what":
		return searchResultWhatMaximumWidth
	default:
		return 0
	}
}

func growSearchResultMatchColumnToUseRemainingOutputWidth(columns []renderColumn, outputWidth int) {
	remainingOutputWidth := outputWidth - renderColumnsWidth(columns)
	if remainingOutputWidth <= 0 {
		return
	}
	for columnIndex := range columns {
		if columns[columnIndex].Header == "match" && !columns[columnIndex].HiddenFromRenderedTable {
			columns[columnIndex].Width += remainingOutputWidth
			return
		}
	}
}

func hideOptionalSearchResultColumnsBeforeCrushingMatch(columns []renderColumn, outputWidth int) {
	for _, optionalColumnHeader := range []string{"what", "where", "who"} {
		if renderColumnsMinimumWidth(columns) <= outputWidth {
			return
		}
		for columnIndex := range columns {
			if columns[columnIndex].Header == optionalColumnHeader {
				columns[columnIndex].HiddenFromRenderedTable = true
				break
			}
		}
	}
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

func searchResultPrimaryMatchedContent(searchResultRow searchResultRow) string {
	if matchedContent := strings.TrimSpace(searchResultRow.match); matchedContent != "" {
		return matchedContent
	}
	return strings.TrimSpace(searchResultRow.what)
}

func searchResultWhatNotDuplicatedInPrimaryMatchedContent(searchResultRow searchResultRow) string {
	what := strings.TrimSpace(searchResultRow.what)
	if what == searchResultPrimaryMatchedContent(searchResultRow) {
		return ""
	}
	return what
}

func searchResultAttachedContextInScanOrder(
	searchResultRow searchResultRow,
	hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler bool,
	hideWhatBecauseEveryRowRepeatsOneCommonRecordKind bool,
) []string {
	contextInScanOrder := make([]string, 0, 4)
	if who := strings.TrimSpace(searchResultRow.who); who != "" {
		contextInScanOrder = append(contextInScanOrder, who)
	}
	if where := strings.TrimSpace(searchResultRow.where); where != "" {
		contextInScanOrder = append(contextInScanOrder, where)
	}
	if !hideWhatBecauseEveryRowRepeatsOneCommonRecordKind {
		if what := searchResultWhatNotDuplicatedInPrimaryMatchedContent(searchResultRow); what != "" {
			contextInScanOrder = append(contextInScanOrder, what)
		}
	}
	if !hideTrawlerBecauseSearchWasExplicitlyScopedToOneTrawler {
		if trawler := strings.TrimSpace(searchResultRow.registeredTrawlerDisplayName); trawler != "" {
			contextInScanOrder = append(contextInScanOrder, trawler)
		}
	}
	return contextInScanOrder
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
