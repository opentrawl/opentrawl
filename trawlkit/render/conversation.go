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

func WriteConversationListResponse(
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
	return writeConversations(writer, conversations, false)
}

type registeredTrawlerDisplayNameForCanonicalConversationRecordReference struct {
	canonicalConversationRecordReference *identity.CanonicalArchiveRecordReference
	registeredTrawlerDisplayName         string
}

func registeredTrawlerDisplayNameForCanonicalConversationRecord(
	registeredTrawlerDisplayNames []registeredTrawlerDisplayNameForCanonicalConversationRecordReference,
	canonicalConversationRecordReference *identity.CanonicalArchiveRecordReference,
) string {
	for _, registeredTrawlerDisplayName := range registeredTrawlerDisplayNames {
		if canonicalArchiveRecordReferencesMatch(
			registeredTrawlerDisplayName.canonicalConversationRecordReference,
			canonicalConversationRecordReference,
		) {
			return registeredTrawlerDisplayName.registeredTrawlerDisplayName
		}
	}
	return ""
}

func WriteFederatedTrawlerConversationListOperation(
	writer io.Writer,
	operation *federation.FederatedTrawlerConversationListOperation,
	showRegisteredTrawlerDisplayNameInConversationTable bool,
) error {
	if operation == nil {
		return fmt.Errorf("federated trawler conversation list operation is missing")
	}
	registeredTrawlerDisplayNamesByCanonicalConversationRecordReference :=
		make([]registeredTrawlerDisplayNameForCanonicalConversationRecordReference, 0)
	for _, trawlerResult := range operation.GetTrawlerConversationListResults() {
		if trawlerResult == nil || trawlerResult.GetConversationListResponse() == nil {
			continue
		}
		trawlerDisplayName := strings.TrimSpace(trawlerResult.GetRegisteredTrawlerDisplayName())
		for _, conversationRecord := range trawlerResult.GetConversationListResponse().GetConversationRecordsNewestFirst() {
			if conversationRecord == nil {
				continue
			}
			canonicalConversationRecordReference := conversationRecord.GetCanonicalRecordReference()
			if canonicalArchiveRecordReferenceText(canonicalConversationRecordReference) != "" {
				registeredTrawlerDisplayNamesByCanonicalConversationRecordReference = append(
					registeredTrawlerDisplayNamesByCanonicalConversationRecordReference,
					registeredTrawlerDisplayNameForCanonicalConversationRecordReference{
						canonicalConversationRecordReference: canonicalConversationRecordReference,
						registeredTrawlerDisplayName:         trawlerDisplayName,
					},
				)
			}
		}
	}
	conversations := make([]conversationForHumanOutput, 0, len(operation.GetConversationRecordsNewestFirst()))
	for _, federatedConversationRecord := range operation.GetConversationRecordsNewestFirst() {
		if federatedConversationRecord == nil || federatedConversationRecord.GetConversationRecord() == nil {
			continue
		}
		conversationRecord := federatedConversationRecord.GetConversationRecord()
		canonicalConversationRecordReference := conversationRecord.GetCanonicalRecordReference()
		conversations = append(conversations, conversationForHumanOutput{
			record:                    conversationRecord,
			globallyRoutableTrawlLink: federatedConversationRecord.GetTrawlLink(),
			personDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter: strings.TrimSpace(
				operation.GetPersonDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter(),
			),
			trawlerDisplayName: strings.TrimSpace(registeredTrawlerDisplayNameForCanonicalConversationRecord(
				registeredTrawlerDisplayNamesByCanonicalConversationRecordReference,
				canonicalConversationRecordReference,
			),
			),
		})
	}
	return writeConversations(writer, conversations, showRegisteredTrawlerDisplayNameInConversationTable)
}

func writeConversations(
	writer io.Writer,
	conversations []conversationForHumanOutput,
	showTrawler bool,
) error {
	if len(conversations) == 0 {
		_, err := fmt.Fprintln(writer, "No conversations match.")
		return err
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
		if conversationDisplayName == "" && len(peopleDisplayNamesForHumanOutput) == 0 {
			conversationDisplayName = "Unknown conversation"
		}
		row := conversationListRow{
			trawler:      strings.TrimSpace(conversation.trawlerDisplayName),
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
		if timestamp := conversationRecord.GetMostRecentConversationActivityTime(); timestamp != nil && timestamp.IsValid() {
			row.when = ShortLocalTime(timestamp.AsTime())
		}
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
	columns := make([]TableColumn, 0, 6)
	whenColumnIndex := -1
	conversationColumnIndex := -1
	trawlerColumnIndex := -1
	peopleColumnIndex := -1
	unreadColumnIndex := -1
	if showWhen {
		whenColumnIndex = len(columns)
		columns = append(columns, TableColumn{Header: "when", KeepWholeTokensWhenTerminalWidthAllows: true})
	}
	if showConversation {
		conversationColumnIndex = len(columns)
		columns = append(columns, TableColumn{
			Header: "conversation", Wrap: true, MaximumWrappedLines: conversationListMaximumWrappedLines,
		})
	}
	if showPeople {
		peopleColumnIndex = len(columns)
		columns = append(columns, TableColumn{
			Header:              "people",
			MinimumWidth:        conversationPeopleColumnMinimumWidth(rows),
			Wrap:                true,
			MaximumWrappedLines: conversationListMaximumWrappedLines,
		})
	}
	if showUnread {
		unreadColumnIndex = len(columns)
		columns = append(columns, TableColumn{Header: "unread", AlignRight: true})
	}
	if showTrawler {
		trawlerColumnIndex = len(columns)
		columns = append(columns, TableColumn{
			Header: "trawler", Wrap: true, MaximumWrappedLines: conversationListMaximumWrappedLines,
		})
	}
	if showLink {
		columns = append(columns, TableColumn{
			Header: "link", MinimumWidth: conversationListLinkMinimumWidth, NeverTruncateCellValues: true,
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
		if showTrawler {
			values = append(values, row.trawler)
		}
		if showLink {
			values = append(values, row.link)
		}
		tableRows = append(tableRows, values)
	}
	outputWidth := OutputWidth(writer)
	primaryHumanContentColumnIndex := conversationColumnIndex
	if primaryHumanContentColumnIndex < 0 {
		primaryHumanContentColumnIndex = peopleColumnIndex
	}
	renderColumns := conversationListRenderColumns(
		columns,
		tableRows,
		outputWidth,
		primaryHumanContentColumnIndex,
		[]int{trawlerColumnIndex, unreadColumnIndex, whenColumnIndex},
	)
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
	if showLink {
		if err := WriteTrawlCommandHint(
			writer,
			"Messages: "+trawlCommandLineForDisplay(writer, []string{"messages", "--conversation", "LINK"}),
		); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(writer); err != nil {
			return err
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
	trawlerDisplayName                                                  string
	personDisplayNameResolvedAcrossTrawlerArchivesForConversationFilter string
}

type conversationListRow struct {
	when         string
	trawler      string
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
	if numberOfConversationParticipantDisplayNamesNotShown :=
		numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive -
			uint64(numberOfConversationParticipantDisplayNamesToShow); numberOfConversationParticipantDisplayNamesNotShown > 0 {
		displayNamePreviewParts = append(
			displayNamePreviewParts,
			"+"+FormatInteger(int64(numberOfConversationParticipantDisplayNamesNotShown)),
		)
	}
	return strings.Join(displayNamePreviewParts, ", ")
}

func conversationPeopleColumnMinimumWidth(rows []conversationListRow) int {
	minimumWidth := minPlainColumnWidth
	for _, row := range rows {
		hiddenPeopleCount := "+" + FormatInteger(
			int64(row.numberOfPeopleForHumanOutput),
		)
		if hiddenPeopleCountWidth := DisplayWidth(hiddenPeopleCount); hiddenPeopleCountWidth > minimumWidth {
			minimumWidth = hiddenPeopleCountWidth
		}
	}
	return minimumWidth
}

func conversationListRenderColumns(
	columns []TableColumn,
	rows [][]string,
	outputWidth int,
	primaryHumanContentColumnIndex int,
	columnIndexesToHideBeforeNarrowingHumanContextBelowItsMinimumWidth []int,
) []renderColumn {
	naturalTableWidth := DisplayWidth(renderTableGap) * max(0, len(columns)-1)
	for columnIndex, column := range columns {
		naturalTableWidth += naturalTableColumnWidth(
			strings.ToLower(strings.TrimSpace(column.Header)),
			column.Wrap,
			rows,
			columnIndex,
		)
	}
	renderColumns := tableRenderColumnsWithPrimaryHumanContentColumn(
		columns,
		rows,
		naturalTableWidth,
		primaryHumanContentColumnIndex,
	)
	for _, columnIndex := range columnIndexesToHideBeforeNarrowingHumanContextBelowItsMinimumWidth {
		if columnIndex >= 0 && columnIndex < len(renderColumns) {
			renderColumns[columnIndex].HideBeforeTruncatingOtherColumnsBelowMinimumWidth = true
		}
	}
	fitRenderColumnsWithPrimaryHumanContentColumn(
		renderColumns,
		outputWidth,
		primaryHumanContentColumnIndex,
	)
	return renderColumns
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
		conversationColumnIndex >= len(renderColumns) || peopleColumnIndex >= len(renderColumns) ||
		renderColumns[conversationColumnIndex].HiddenFromRenderedTable ||
		renderColumns[peopleColumnIndex].HiddenFromRenderedTable {
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
	conversationWidth := min(naturalConversationWidth, max(minimumConversationWidth, combinedWidth/2))
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

func WriteConversationParticipantListResponse(
	writer io.Writer,
	response *conversation.ConversationParticipantListResponse,
) error {
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
			{Header: "person", Wrap: true, MaximumWrappedLines: 2},
			{Header: "link", MinimumWidth: conversationListLinkMinimumWidth, NeverTruncateCellValues: true},
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
	_, err := fmt.Fprintf(
		writer,
		"\nNames unavailable: %s.\n",
		FormatInteger(numberOfParticipantNamesUnavailable),
	)
	return err
}

func writeNoConversationParticipantNamesAvailable(
	writer io.Writer,
	response *conversation.ConversationParticipantListResponse,
) error {
	if _, err := fmt.Fprintln(writer, "No participant names are available."); err != nil {
		return err
	}
	numberOfParticipantNamesUnavailable :=
		response.GetNumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive()
	if numberOfParticipantNamesUnavailable == 0 {
		return nil
	}
	_, err := fmt.Fprintf(
		writer,
		"Names unavailable: %s.\n",
		FormatInteger(int64(numberOfParticipantNamesUnavailable)),
	)
	return err
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
	wrappedLines := Wrap(elideWideTokens(preview, peopleColumn.Width), peopleColumn.Width)
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
