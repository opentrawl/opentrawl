package render

import (
	"fmt"
	"io"
	"strings"

	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	identity "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/identity"
)

const (
	conversationListMaximumWrappedLines                             = 2
	conversationListLinkMinimumWidth                                = 5
	conversationListPeopleMaximumWidth                              = 120
	maximumConversationParticipantDisplayNamesInCompactHumanPreview = 4
)

func writeConversationListResponse(
	writer io.Writer,
	response *conversation.ConversationListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) error {
	if response == nil {
		return fmt.Errorf("conversation list response is missing")
	}
	conversations := make([]conversationForHumanOutput, 0, len(response.GetConversationRecordsNewestFirst()))
	for _, conversationRecord := range response.GetConversationRecordsNewestFirst() {
		if conversationRecord == nil {
			continue
		}
		conversations = append(conversations, conversationForHumanOutput{
			record: conversationRecord,
			globallyRoutableTrawlLink: globallyRoutableTrawlLinksByCanonicalRecordReference.
				globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(
					conversationRecord.GetCanonicalRecordReference(),
				),
		})
	}
	return writeConversations(writer, conversations)
}

// WriteConversationList writes conversations newest first, then the command
// that shows more.
func (output TrawlCommandOutput) WriteConversationList(
	operation *federation.FederatedTrawlerConversationListOperation,
	nextCommandShowingMoreConversations []NextCommand,
) error {
	writer := output.standardOutput
	if operation == nil {
		return fmt.Errorf("federated trawler conversation list operation is missing")
	}
	conversations := make([]conversationForHumanOutput, 0, len(operation.GetConversationRecordsNewestFirst()))
	for _, federatedConversationRecord := range operation.GetConversationRecordsNewestFirst() {
		if federatedConversationRecord == nil || federatedConversationRecord.GetConversationRecord() == nil {
			continue
		}
		conversationRecord := federatedConversationRecord.GetConversationRecord()
		conversations = append(conversations, conversationForHumanOutput{
			record:                    conversationRecord,
			globallyRoutableTrawlLink: federatedConversationRecord.GetTrawlLink(),
			personDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter: strings.TrimSpace(
				operation.GetPersonDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter(),
			),
		})
	}
	if operation.GetOutcome() != federation.OperationOutcome_OPERATION_OUTCOME_FAILED {
		if err := writeConversations(writer, conversations); err != nil {
			return err
		}
		var nextCommands []NextCommand
		if len(conversations) > 0 {
			nextCommands = append(nextCommands, nextCommandListingConversationMessages(
				globallyRoutableTrawlLinkText(conversations[0].globallyRoutableTrawlLink),
			))
		}
		if operation.GetMoreConversationRecordsExist() {
			nextCommands = append(nextCommands, nextCommandShowingMoreConversations...)
		}
		if err := writeNextCommands(writer, nextCommands); err != nil {
			return err
		}
	}
	return output.WriteTrawlerOperationFailures(
		operation.GetOperationFailures(),
		operation.GetTrawlersSkippedFromOperation(),
		false,
	)
}

func writeConversations(
	writer io.Writer,
	conversations []conversationForHumanOutput,
) error {
	if len(conversations) == 0 {
		return writeLine(writer, "No conversations match.")
	}
	rows := make([]conversationListRow, 0, len(conversations))
	for _, conversation := range conversations {
		conversationRecord := conversation.record
		if conversationRecord == nil {
			continue
		}
		conversationDisplayName := strings.TrimSpace(conversationRecord.GetConversationDisplayName())
		conversationParticipantDisplayNamesObservedByTrawlerArchive := conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
			conversationRecord.GetConversationParticipantIdentitiesObservedByTrawlerArchive(),
		)
		peopleDisplayNamesForHumanOutput := conversationParticipantDisplayNamesObservedByTrawlerArchive
		if len(peopleDisplayNamesForHumanOutput) == 0 &&
			conversation.personDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter != "" {
			peopleDisplayNamesForHumanOutput = []string{
				conversation.personDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter,
			}
		}
		allPeopleDisplayNames := strings.Join(peopleDisplayNamesForHumanOutput, ", ")
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive :=
			resolveNumberOfDistinctConversationParticipantRecordsForHumanOutput(
				conversationParticipantDisplayNamesObservedByTrawlerArchive,
				conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
			)
		numberOfPeopleForHumanOutput := max(
			numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
			uint64(len(peopleDisplayNamesForHumanOutput)),
		)
		if strings.EqualFold(conversationDisplayName, strings.TrimSpace(allPeopleDisplayNames)) {
			conversationDisplayName = ""
		}
		row := conversationListRow{
			link:         globallyRoutableTrawlLinkText(conversation.globallyRoutableTrawlLink),
			conversation: conversationDisplayName,
			people: conversationParticipantDisplayNamesAndHiddenCount(
				peopleDisplayNamesForHumanOutput,
				len(peopleDisplayNamesForHumanOutput),
				numberOfPeopleForHumanOutput,
			),
			peopleDisplayNamesForHumanOutput: peopleDisplayNamesForHumanOutput,
			numberOfPeopleForHumanOutput:     numberOfPeopleForHumanOutput,
		}
		row.when = formatExactTimeInThisComputerTimeZone(conversationRecord.GetMostRecentConversationActivityTime(), localTimeWithUTCOffsetForListRow)
		if conversationRecord.UnreadMessageCount != nil && conversationRecord.GetUnreadMessageCount() > 0 {
			row.unread = FormatInteger(int64(conversationRecord.GetUnreadMessageCount()))
		}
		rows = append(rows, row)
	}
	showWhen := anyConversationListCell(rows, func(row conversationListRow) string { return row.when })
	showLink := anyConversationListCell(rows, func(row conversationListRow) string { return row.link })
	showConversation := anyConversationListCell(rows, func(row conversationListRow) string { return row.conversation })
	showPeople := anyConversationListCell(rows, func(row conversationListRow) string { return row.people })
	showUnread := anyConversationListCell(rows, func(row conversationListRow) string { return row.unread })
	columns := make([]TableColumn, 0, 5)
	conversationColumnIndex := -1
	peopleColumnIndex := -1
	if showWhen {
		columns = append(columns, archiveEventTimeColumn(columnHeaderWhen))
	}
	if showConversation {
		conversationColumnIndex = len(columns)
		columns = append(columns, TableColumn{
			Header: columnHeaderConversation, Wrap: true, MaximumWrappedLines: conversationListMaximumWrappedLines,
		})
	}
	if showPeople {
		peopleColumnIndex = len(columns)
		columns = append(columns, TableColumn{
			Header:              columnHeaderPeople,
			MinimumWidth:        conversationPeopleColumnMinimumWidth(rows),
			Wrap:                true,
			MaximumWrappedLines: conversationListMaximumWrappedLines,
		})
	}
	if showUnread {
		columns = append(columns, TableColumn{Header: "unread", AlignRight: true})
	}
	if showLink {
		columns = append(columns, TableColumn{
			Header: columnHeaderLink, MinimumWidth: conversationListLinkMinimumWidth, NeverTruncateCellValues: true,
		})
	}
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		values := make([]string, 0, len(columns))
		if showWhen {
			values = append(values, row.when)
		}
		if showConversation {
			values = append(values, row.conversation)
		}
		if showPeople {
			values = append(values, row.people)
		}
		if showUnread {
			values = append(values, row.unread)
		}
		if showLink {
			values = append(values, row.link)
		}
		tableRows = append(tableRows, values)
	}
	// Every row names its people; only group chats also have a name.
	primaryHumanContentColumnIndex := peopleColumnIndex
	if primaryHumanContentColumnIndex < 0 {
		primaryHumanContentColumnIndex = conversationColumnIndex
	}
	renderColumns := tableRenderColumnsWithPrimaryHumanContentColumn(
		columns,
		tableRows,
		OutputWidth(writer),
		primaryHumanContentColumnIndex,
	)
	if !listRowsFitAsTable(renderColumns, OutputWidth(writer)) {
		if peopleColumnIndex >= 0 {
			for rowIndex, row := range rows {
				tableRows[rowIndex][peopleColumnIndex] = ConversationParticipantDisplayNamesPreviewForHumanOutput(
					row.peopleDisplayNamesForHumanOutput,
					row.numberOfPeopleForHumanOutput,
				)
			}
		}
		return writeFieldValueRows(writer, renderColumns, tableRows, OutputWidth(writer))
	}
	balanceConversationIdentityAndPeopleColumns(
		renderColumns,
		columns,
		tableRows,
		conversationColumnIndex,
		peopleColumnIndex,
	)
	if peopleColumnIndex >= 0 {
		for rowIndex := range rows {
			tableRows[rowIndex][peopleColumnIndex] = peopleDisplayNamesForRenderedPeopleColumn(
				rows[rowIndex],
				renderColumns[peopleColumnIndex],
			)
		}
	}
	if err := writeRenderHeader(writer, renderColumns); err != nil {
		return err
	}
	for _, tableRow := range tableRows {
		if err := writeRenderRow(writer, renderColumns, tableRow); err != nil {
			return err
		}
	}
	return nil
}

type conversationForHumanOutput struct {
	record                                                              *conversation.ConversationRecord
	globallyRoutableTrawlLink                                           *identity.GloballyRoutableTrawlLink
	personDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter string
}

type conversationListRow struct {
	when         string
	link         string
	conversation string
	people       string
	unread       string

	peopleDisplayNamesForHumanOutput []string
	numberOfPeopleForHumanOutput     uint64
}

func conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
	participantIdentities []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive,
) []string {
	displayNames := make([]string, 0, len(participantIdentities))
	for _, participantIdentity := range participantIdentities {
		if displayName := strings.TrimSpace(participantIdentity.GetPersonDisplayName()); displayName != "" {
			displayNames = append(displayNames, displayName)
		}
	}
	return displayNames
}

func resolveNumberOfDistinctConversationParticipantRecordsForHumanOutput(
	conversationParticipantDisplayNames []string,
	numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive *uint64,
) uint64 {
	numberOfDistinctConversationParticipantRecordsForHumanOutput := uint64(len(conversationParticipantDisplayNames))
	if numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive != nil &&
		*numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive >
			numberOfDistinctConversationParticipantRecordsForHumanOutput {
		numberOfDistinctConversationParticipantRecordsForHumanOutput =
			*numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive
	}
	return numberOfDistinctConversationParticipantRecordsForHumanOutput
}

func ConversationParticipantDisplayNamesPreviewForHumanOutput(
	conversationParticipantDisplayNames []string,
	numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive uint64,
) string {
	if uint64(len(conversationParticipantDisplayNames)) >
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive {
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive =
			uint64(len(conversationParticipantDisplayNames))
	}
	if len(conversationParticipantDisplayNames) == 0 {
		return ""
	}
	numberOfConversationParticipantDisplayNamesInPreview := len(conversationParticipantDisplayNames)
	if numberOfConversationParticipantDisplayNamesInPreview > maximumConversationParticipantDisplayNamesInCompactHumanPreview {
		numberOfConversationParticipantDisplayNamesInPreview = maximumConversationParticipantDisplayNamesInCompactHumanPreview
	}
	return conversationParticipantDisplayNamesAndHiddenCount(
		conversationParticipantDisplayNames,
		numberOfConversationParticipantDisplayNamesInPreview,
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
	)
}

func conversationParticipantDisplayNamesAndHiddenCount(
	conversationParticipantDisplayNames []string,
	numberOfConversationParticipantDisplayNamesToShow int,
	numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive uint64,
) string {
	if numberOfConversationParticipantDisplayNamesToShow > len(conversationParticipantDisplayNames) {
		numberOfConversationParticipantDisplayNamesToShow = len(conversationParticipantDisplayNames)
	}
	if numberOfConversationParticipantDisplayNamesToShow < 0 {
		numberOfConversationParticipantDisplayNamesToShow = 0
	}
	if uint64(len(conversationParticipantDisplayNames)) >
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive {
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive =
			uint64(len(conversationParticipantDisplayNames))
	}
	displayNamePreviewParts := append(
		[]string(nil),
		conversationParticipantDisplayNames[:numberOfConversationParticipantDisplayNamesToShow]...,
	)
	numberOfConversationParticipantDisplayNamesNotShown := numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive -
		uint64(numberOfConversationParticipantDisplayNamesToShow)
	if numberOfConversationParticipantDisplayNamesToShow == 0 && numberOfConversationParticipantDisplayNamesNotShown > 0 {
		return countOfPeople(numberOfConversationParticipantDisplayNamesNotShown)
	}
	if numberOfConversationParticipantDisplayNamesNotShown > 0 {
		displayNamePreviewParts = append(
			displayNamePreviewParts,
			"+"+FormatInteger(int64(numberOfConversationParticipantDisplayNamesNotShown)),
		)
	}
	return strings.Join(displayNamePreviewParts, ", ")
}

// countOfPeople names a number of people whose names are not known. One
// unnamed person says nothing a reader can use, so it is left blank.
func countOfPeople(numberOfPeople uint64) string {
	if numberOfPeople <= 1 {
		return ""
	}
	return FormatInteger(int64(numberOfPeople)) + " people"
}

// conversationPeopleColumnMinimumWidth is the narrowest people column that
// still shows each conversation's first name and how many others there are,
// in at most two lines, without cutting a word.
func conversationPeopleColumnMinimumWidth(rows []conversationListRow) int {
	minimumWidth := minPlainColumnWidth
	for _, row := range rows {
		firstNameAndHiddenCount := conversationParticipantDisplayNamesAndHiddenCount(
			row.peopleDisplayNamesForHumanOutput,
			1,
			row.numberOfPeopleForHumanOutput,
		)
		rowMinimumWidth := minPlainColumnWidth
		for _, word := range strings.Fields(firstNameAndHiddenCount) {
			rowMinimumWidth = max(rowMinimumWidth, DisplayWidth(word))
		}
		for len(Wrap(firstNameAndHiddenCount, rowMinimumWidth)) > conversationListMaximumWrappedLines {
			rowMinimumWidth++
		}
		minimumWidth = max(minimumWidth, rowMinimumWidth)
	}
	return minimumWidth
}

func peopleDisplayNamesForRenderedPeopleColumn(
	row conversationListRow,
	peopleColumn renderColumn,
) string {
	numberOfPeopleDisplayNamesToShow := len(row.peopleDisplayNamesForHumanOutput)
	for {
		preview := conversationParticipantDisplayNamesAndHiddenCount(
			row.peopleDisplayNamesForHumanOutput,
			numberOfPeopleDisplayNamesToShow,
			row.numberOfPeopleForHumanOutput,
		)
		if conversationPeoplePreviewFitsRenderedColumn(preview, peopleColumn) ||
			numberOfPeopleDisplayNamesToShow == 0 {
			return preview
		}
		numberOfPeopleDisplayNamesToShow--
	}
}

func balanceConversationIdentityAndPeopleColumns(
	renderColumns []renderColumn,
	tableColumns []TableColumn,
	rows [][]string,
	conversationColumnIndex int,
	peopleColumnIndex int,
) {
	if conversationColumnIndex < 0 || peopleColumnIndex < 0 ||
		conversationColumnIndex >= len(renderColumns) || peopleColumnIndex >= len(renderColumns) {
		return
	}
	combinedWidth := renderColumns[conversationColumnIndex].Width + renderColumns[peopleColumnIndex].Width
	minimumConversationWidth := minRenderColumnWidth(renderColumns[conversationColumnIndex])
	minimumPeopleWidth := minRenderColumnWidth(renderColumns[peopleColumnIndex])
	if combinedWidth <= minimumConversationWidth+minimumPeopleWidth {
		return
	}
	naturalConversationWidth := naturalTableColumnWidth(
		strings.ToLower(strings.TrimSpace(tableColumns[conversationColumnIndex].Header)),
		tableColumns[conversationColumnIndex].Wrap,
		rows,
		conversationColumnIndex,
	)
	naturalPeopleWidth := naturalTableColumnWidth(
		strings.ToLower(strings.TrimSpace(tableColumns[peopleColumnIndex].Header)),
		tableColumns[peopleColumnIndex].Wrap,
		rows,
		peopleColumnIndex,
	)
	// The people get two thirds: every row names its people, but only group
	// chats have a conversation name.
	conversationWidth := min(naturalConversationWidth, max(minimumConversationWidth, combinedWidth/3))
	peopleWidth := combinedWidth - conversationWidth
	if peopleWidth > naturalPeopleWidth {
		conversationWidth += peopleWidth - naturalPeopleWidth
		peopleWidth = naturalPeopleWidth
	}
	if peopleWidth < minimumPeopleWidth {
		peopleWidth = minimumPeopleWidth
		conversationWidth = combinedWidth - peopleWidth
	}
	if conversationWidth < minimumConversationWidth {
		conversationWidth = minimumConversationWidth
		peopleWidth = combinedWidth - conversationWidth
	}
	peopleWidth = min(peopleWidth, conversationListPeopleMaximumWidth)
	renderColumns[conversationColumnIndex].Width = conversationWidth
	renderColumns[peopleColumnIndex].Width = peopleWidth
}

// WriteConversationParticipants writes everyone seen in one conversation.
func (output TrawlCommandOutput) WriteConversationParticipants(
	response *conversation.ConversationParticipantListResponse,
) error {
	writer := output.standardOutput
	if response == nil {
		return fmt.Errorf("conversation participant list response is missing")
	}
	participantRows := response.GetConversationParticipantsInAlphabeticalOrder()
	if len(participantRows) == 0 {
		return writeNoConversationParticipantNamesAvailable(writer, response)
	}
	rows := make([][]string, 0, len(participantRows))
	for _, participant := range participantRows {
		if participant == nil || strings.TrimSpace(participant.GetPersonDisplayName()) == "" {
			continue
		}
		rows = append(rows, []string{
			strings.TrimSpace(participant.GetPersonDisplayName()),
			globallyRoutableTrawlLinkText(participant.GetPersonTrawlLinkResolvedAcrossTrawlerArchives()),
		})
	}
	if len(rows) == 0 {
		return writeNoConversationParticipantNamesAvailable(writer, response)
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(
		writer,
		[]TableColumn{
			{Header: columnHeaderPerson, Wrap: true, MaximumWrappedLines: 2},
			{Header: columnHeaderLink, MinimumWidth: conversationListLinkMinimumWidth, NeverTruncateCellValues: true},
		},
		rows,
		0,
	); err != nil {
		return err
	}
	numberOfParticipantNamesUnavailable := int64(
		response.GetNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive(),
	) - int64(len(rows))
	if numberOfParticipantNamesUnavailable <= 0 {
		return nil
	}
	return writeLines(writer, []string{"", "Names unavailable: " + FormatInteger(numberOfParticipantNamesUnavailable) + "."})
}

func writeNoConversationParticipantNamesAvailable(
	writer io.Writer,
	response *conversation.ConversationParticipantListResponse,
) error {
	if err := writeLine(writer, "No participant names are available."); err != nil {
		return err
	}
	numberOfParticipantNamesUnavailable :=
		response.GetNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive()
	if numberOfParticipantNamesUnavailable == 0 {
		return nil
	}
	return writeLine(writer, "Names unavailable: "+FormatInteger(int64(numberOfParticipantNamesUnavailable))+".")
}

func conversationPeoplePreviewFitsRenderedColumn(preview string, peopleColumn renderColumn) bool {
	if strings.TrimSpace(preview) == "" {
		return true
	}
	if peopleColumn.Width <= 0 {
		return false
	}
	preview = strings.TrimSpace(preview)
	if !peopleColumn.Wrap {
		return DisplayWidth(preview) <= peopleColumn.Width
	}
	wrappedLines := Wrap(elideWideTokens(preview, peopleColumn.Width, false), peopleColumn.Width)
	return peopleColumn.Clamp <= 0 || len(wrappedLines) <= peopleColumn.Clamp
}

func anyConversationListCell(rows []conversationListRow, value func(conversationListRow) string) bool {
	for _, row := range rows {
		if strings.TrimSpace(value(row)) != "" {
			return true
		}
	}
	return false
}
