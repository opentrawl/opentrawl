package render

import (
	"strings"

	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
)

const (
	searchResultWhoMinimumWidth   = 12
	searchResultWhereMinimumWidth = 12
	searchResultWhatMinimumWidth  = 12
	// Every column of a row wraps to the same height, so a name is not cut
	// while the matched text beside it still has lines to fill.
	searchResultMaximumWrappedLines = 3
)

// SearchResults are the facts of one search: what was asked, what matched,
// and how many more there are.
type SearchResults struct {
	SearchQueryWords                        string
	PersonDisplayNameTheSearchInvolves      string
	SearchStartDateAsTyped                  string
	SearchEndDateAsTyped                    string
	SearchMatchesInDisplayOrder             []SearchResultPresentationForRootTrawlHumanOutput
	TotalSearchMatchCount                   int
	TrawlCommandArgumentsShowingMoreMatches []string
}

type SearchResultPresentationForRootTrawlHumanOutput struct {
	SearchMatchPresentation   *search.SearchMatchPresentation
	GloballyRoutableTrawlLink *identity.GloballyRoutableTrawlLink
}

// WriteSearchResults writes a heading that restates the search, one row per
// match, and the command that shows more matches.
func (output TrawlCommandOutput) WriteSearchResults(searchResults SearchResults) error {
	writer := output.standardOutput
	if len(searchResults.SearchMatchesInDisplayOrder) == 0 {
		return writeLine(writer, searchResultsEmptySentence(searchResults))
	}
	searchResultRows := make([]searchResultRow, 0, len(searchResults.SearchMatchesInDisplayOrder))
	for _, presentation := range searchResults.SearchMatchesInDisplayOrder {
		searchResultRows = append(searchResultRows, searchResultRowFromPresentation(presentation))
	}
	if err := writeLines(writer, Wrap(searchResultsHeading(searchResults), OutputWidth(writer))); err != nil {
		return err
	}
	if err := writeLine(writer, ""); err != nil {
		return err
	}
	columnSpecifications := searchResultColumnSpecifications(searchResultRows)
	columns := make([]TableColumn, 0, len(columnSpecifications))
	tableRows := make([][]string, 0, len(searchResultRows))
	for _, columnSpecification := range columnSpecifications {
		columns = append(columns, columnSpecification.humanOutputColumn)
	}
	for _, searchResultRow := range searchResultRows {
		tableRows = append(tableRows, searchResultTableRow(searchResultRow, columnSpecifications))
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(
		writer,
		columns,
		tableRows,
		searchResultPrimaryHumanContentColumnIndex(columnSpecifications),
	); err != nil {
		return err
	}
	nextCommands := []NextCommand{nextCommandOpeningRecord(searchResultRows[0].globallyRoutableTrawlLink)}
	if len(searchResults.TrawlCommandArgumentsShowingMoreMatches) > 0 {
		nextCommands = append(nextCommands, NextCommandShowingMoreRows(searchResults.TrawlCommandArgumentsShowingMoreMatches))
	}
	return writeNextCommands(writer, nextCommands)
}

func searchResultsEmptySentence(searchResults SearchResults) string {
	if description := searchDescription(searchResults); description != "" {
		return "Nothing matches " + description + "."
	}
	return "Nothing matches."
}

func searchResultsHeading(searchResults SearchResults) string {
	heading := "Search"
	if description := searchDescription(searchResults); description != "" {
		heading += " " + description
	}
	return heading + ": showing " + FormatInteger(int64(len(searchResults.SearchMatchesInDisplayOrder))) +
		" of " + FormatInteger(int64(searchResults.TotalSearchMatchCount)) + "."
}

// searchDescription restates the query words, person and date bounds.
func searchDescription(searchResults SearchResults) string {
	parts := make([]string, 0, 3)
	if queryWords := strings.TrimSpace(searchResults.SearchQueryWords); queryWords != "" {
		parts = append(parts, quotedForSentence(queryWords))
	}
	if personDisplayName := strings.TrimSpace(searchResults.PersonDisplayNameTheSearchInvolves); personDisplayName != "" {
		parts = append(parts, "involving "+personDisplayName)
	}
	description := strings.Join(parts, " ")
	dateParts := make([]string, 0, 2)
	if startDate := strings.TrimSpace(searchResults.SearchStartDateAsTyped); startDate != "" {
		dateParts = append(dateParts, "on or after "+startDate)
	}
	if endDate := strings.TrimSpace(searchResults.SearchEndDateAsTyped); endDate != "" {
		dateParts = append(dateParts, "on or before "+endDate)
	}
	if len(dateParts) == 0 {
		return description
	}
	if description == "" {
		return strings.Join(dateParts, " and ")
	}
	return description + ", " + strings.Join(dateParts, " and ")
}

type searchResultRow struct {
	when                      string
	globallyRoutableTrawlLink string
	what                      string
	who                       string
	where                     string
	match                     string
}

func searchResultRowFromPresentation(
	presentationForRootTrawlHumanOutput SearchResultPresentationForRootTrawlHumanOutput,
) searchResultRow {
	presentation := presentationForRootTrawlHumanOutput.SearchMatchPresentation
	if presentation == nil {
		return searchResultRow{}
	}
	return searchResultRow{
		when:                      formatArchiveRecordAssociatedTime(presentation.GetMatchingRecordAssociatedTime(), localTimeWithUTCOffsetForListRow),
		globallyRoutableTrawlLink: globallyRoutableTrawlLinkText(presentationForRootTrawlHumanOutput.GloballyRoutableTrawlLink),
		what:                      strings.TrimSpace(presentation.GetMatchingRecordDisplayName()),
		who:                       formatSendersAndRecipients(presentation.GetPeopleRelatedToMatchingRecord()),
		where: searchResultPhysicalPlacesAndDigitalContainers(
			SearchMatchPhysicalPlaceNamesForHumanOutput(presentation),
			containerNamesThatAreNotPeople(
				presentation.GetDigitalContainerNamesNearestToBroadest(),
				presentation.GetPeopleRelatedToMatchingRecord(),
			),
		),
		match: searchResultMatchingText(
			presentation.GetSearchMatchTextFieldsInDisplayOrder(),
			presentation.GetMatchingRecordKindDisplayName(),
		),
	}
}

func SearchMatchPhysicalPlaceNamesForHumanOutput(presentation *search.SearchMatchPresentation) []string {
	if facts := presentation.GetAppleLibraryPhotoFacts(); facts != nil {
		names := appleStoredPlaceHierarchyNamesSmallToLarge(facts.GetAppleStoredPlace())
		if len(names) == 0 {
			return []string{photoPlaceForHumanOutput(facts.GetAppleStoredPlace(), facts.GetCameraLocation())}
		}
		if uncertainty := strings.TrimSpace(photoLocationUncertaintyForHumanOutput(facts.GetCameraLocation())); uncertainty != "" {
			names[0] = uncertainty + " · " + names[0]
		}
		return names
	}
	return nonEmptySearchResultNames(presentation.GetPhysicalPlaceNamesSpecificToBroadest())
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
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		alreadyIncluded := false
		for _, existing := range nonEmptyNames {
			if strings.EqualFold(existing, name) {
				alreadyIncluded = true
				break
			}
		}
		if !alreadyIncluded {
			nonEmptyNames = append(nonEmptyNames, name)
		}
	}
	return nonEmptyNames
}

// searchResultMatchingText writes each matching field's text with its
// matches marked. A field is labelled, as "Title: …" is, unless it is the
// record's own text, such as a message's or a note's.
func searchResultMatchingText(
	matchingTextFields []*search.SearchMatchTextField,
	matchingRecordKindDisplayName string,
) string {
	labelledMatchingTexts := make([]string, 0, len(matchingTextFields))
	for _, matchingTextField := range matchingTextFields {
		if matchingTextField == nil {
			continue
		}
		var displayedText strings.Builder
		for _, fragment := range matchingTextField.GetSearchMatchTextFragmentsInDisplayOrder() {
			if fragment == nil {
				continue
			}
			if fragment.GetSearchMatchTextFragmentMatchesSearchQuery() {
				displayedText.WriteString("‹" + fragment.GetSearchMatchTextFragmentContent() + "›")
				continue
			}
			displayedText.WriteString(fragment.GetSearchMatchTextFragmentContent())
		}
		matchingText := strings.Join(strings.Fields(displayedText.String()), " ")
		if matchingText == "" {
			continue
		}
		fieldName := strings.TrimSpace(matchingTextField.GetSearchMatchTextFieldName())
		if fieldName != "" && !strings.EqualFold(fieldName, strings.TrimSpace(matchingRecordKindDisplayName)) {
			matchingText = fieldName + ": " + matchingText
		}
		labelledMatchingTexts = append(labelledMatchingTexts, matchingText)
	}
	return strings.Join(labelledMatchingTexts, "; ")
}

type searchResultColumnSpecification struct {
	humanOutputColumn       TableColumn
	textFromSearchResultRow func(searchResultRow) string
}

// searchResultColumnSpecifications keeps every column that holds data for
// some row. There is no app column: the link names the app.
func searchResultColumnSpecifications(searchResultRows []searchResultRow) []searchResultColumnSpecification {
	columnSpecifications := []searchResultColumnSpecification{
		{
			humanOutputColumn:       archiveEventTimeColumn(columnHeaderWhen),
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.when },
		},
		{
			humanOutputColumn: TableColumn{
				Header:              columnHeaderMatch,
				Wrap:                true,
				MaximumWrappedLines: searchResultMaximumWrappedLines,
			},
			textFromSearchResultRow: searchResultPrimaryMatchedContent,
		},
		{
			humanOutputColumn: TableColumn{
				Header:              columnHeaderWho,
				MinimumWidth:        searchResultWhoMinimumWidth,
				Wrap:                true,
				MaximumWrappedLines: searchResultMaximumWrappedLines,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.who },
		},
		{
			humanOutputColumn: TableColumn{
				Header:              columnHeaderWhere,
				MinimumWidth:        searchResultWhereMinimumWidth,
				Wrap:                true,
				MaximumWrappedLines: searchResultMaximumWrappedLines,
			},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.where },
		},
		{
			humanOutputColumn: TableColumn{
				Header:              columnHeaderWhat,
				MinimumWidth:        searchResultWhatMinimumWidth,
				Wrap:                true,
				MaximumWrappedLines: searchResultMaximumWrappedLines,
			},
			textFromSearchResultRow: searchResultWhatNotDuplicatedInPrimaryMatchedContent,
		},
		{
			humanOutputColumn:       TableColumn{Header: columnHeaderLink, NeverTruncateCellValues: true},
			textFromSearchResultRow: func(searchResultRow searchResultRow) string { return searchResultRow.globallyRoutableTrawlLink },
		},
	}
	keptColumnSpecifications := make([]searchResultColumnSpecification, 0, len(columnSpecifications))
	for _, columnSpecification := range columnSpecifications {
		if columnSpecification.humanOutputColumn.Header == columnHeaderMatch ||
			searchResultRowsContain(searchResultRows, columnSpecification.textFromSearchResultRow) {
			keptColumnSpecifications = append(keptColumnSpecifications, columnSpecification)
		}
	}
	return keptColumnSpecifications
}

func searchResultPrimaryHumanContentColumnIndex(
	columnSpecifications []searchResultColumnSpecification,
) int {
	for columnIndex, columnSpecification := range columnSpecifications {
		if columnSpecification.humanOutputColumn.Header == columnHeaderMatch {
			return columnIndex
		}
	}
	return -1
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

// searchResultWhatNotDuplicatedInPrimaryMatchedContent drops the record's
// name when the matched text already shows it, as an email subject does.
func searchResultWhatNotDuplicatedInPrimaryMatchedContent(searchResultRow searchResultRow) string {
	what := strings.TrimSpace(searchResultRow.what)
	matchedTextWithoutHighlights := strings.NewReplacer("‹", "", "›", "").Replace(searchResultPrimaryMatchedContent(searchResultRow))
	if what == "" || strings.Contains(strings.ToLower(matchedTextWithoutHighlights), strings.ToLower(what)) {
		return ""
	}
	return what
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
