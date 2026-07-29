package render

import (
	"fmt"
	"io"
	"strings"

	conversationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation/v1"
	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
)

const (
	conversationListMaximumWrappedLines                      = 2
	conversationListLinkMinimumWidth                         = 5
	maximumConversationParticipantDisplayNamesInHumanPreview = 3
)

func WriteConversationListResponse(
	writer io.Writer,
	response *conversationv1.ConversationListResponse,
	globallyRoutableTrawlLinksByCanonicalRecordReference map[string]string,
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
			globallyRoutableTrawlLink: strings.TrimSpace(
				globallyRoutableTrawlLinksByCanonicalRecordReference[strings.TrimSpace(
					conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
				)],
			),
		})
	}
	return writeConversations(writer, conversations, false)
}

func WriteFederatedTrawlerConversationListOperation(
	writer io.Writer,
	operation *federationv1.FederatedTrawlerConversationListOperation,
	showRegisteredTrawlerDisplayNameInConversationTable bool,
) error {
	if operation == nil {
		return fmt.Errorf("federated trawler conversation list operation is missing")
	}
	trawlerDisplayNameByCanonicalConversationRecordReference := make(map[string]string)
	for _, trawlerResult := range operation.GetTrawlerConversationListResults() {
		if trawlerResult == nil || trawlerResult.GetConversationListResponse() == nil {
			continue
		}
		trawlerDisplayName := strings.TrimSpace(trawlerResult.GetRegisteredTrawlerDisplayName())
		for _, conversationRecord := range trawlerResult.GetConversationListResponse().GetConversationRecordsNewestFirst() {
			if conversationRecord == nil {
				continue
			}
			canonicalConversationRecordReference := strings.TrimSpace(
				conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
			)
			if canonicalConversationRecordReference != "" {
				trawlerDisplayNameByCanonicalConversationRecordReference[canonicalConversationRecordReference] =
					trawlerDisplayName
			}
		}
	}
	conversations := make([]conversationForHumanOutput, 0, len(operation.GetConversationRecordsNewestFirst()))
	for _, federatedConversationRecord := range operation.GetConversationRecordsNewestFirst() {
		if federatedConversationRecord == nil || federatedConversationRecord.GetConversationRecord() == nil {
			continue
		}
		conversationRecord := federatedConversationRecord.GetConversationRecord()
		canonicalConversationRecordReference := strings.TrimSpace(
			conversationRecord.GetCanonicalConversationRecordReferenceForGloballyRoutableTrawlLinkAssignment(),
		)
		conversations = append(conversations, conversationForHumanOutput{
			record:                    conversationRecord,
			globallyRoutableTrawlLink: strings.TrimSpace(federatedConversationRecord.GetGloballyRoutableTrawlLink()),
			trawlerDisplayName: strings.TrimSpace(
				trawlerDisplayNameByCanonicalConversationRecordReference[canonicalConversationRecordReference],
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
		_, err := fmt.Fprintln(writer, "No conversations.")
		return err
	}
	rows := make([]conversationListRow, 0, len(conversations))
	for _, conversation := range conversations {
		conversationRecord := conversation.record
		if conversationRecord == nil {
			continue
		}
		conversationDisplayName := strings.TrimSpace(conversationRecord.GetConversationDisplayName())
		conversationParticipantDisplayNames := conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
			conversationRecord.GetConversationParticipantIdentitiesObservedByTrawlerArchive(),
		)
		allPeopleDisplayNames := strings.Join(conversationParticipantDisplayNames, ", ")
		numberOfDistinctConversationParticipantRecordsForHumanOutput :=
			resolveNumberOfDistinctConversationParticipantRecordsForHumanOutput(
				conversationParticipantDisplayNames,
				conversationRecord.NumberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
			)
		if strings.EqualFold(conversationDisplayName, strings.TrimSpace(allPeopleDisplayNames)) {
			conversationDisplayName = ""
		}
		if conversationDisplayName == "" && len(conversationParticipantDisplayNames) == 0 {
			conversationDisplayName = "Unknown conversation"
		}
		row := conversationListRow{
			trawler:      strings.TrimSpace(conversation.trawlerDisplayName),
			link:         strings.TrimSpace(conversation.globallyRoutableTrawlLink),
			conversation: conversationDisplayName,
			people: conversationParticipantDisplayNamesAndHiddenCount(
				conversationParticipantDisplayNames,
				len(conversationParticipantDisplayNames),
				numberOfDistinctConversationParticipantRecordsForHumanOutput,
			),
			conversationParticipantDisplayNames:                          conversationParticipantDisplayNames,
			numberOfDistinctConversationParticipantRecordsForHumanOutput: numberOfDistinctConversationParticipantRecordsForHumanOutput,
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
	trawlerColumnIndex := -1
	peopleColumnIndex := -1
	conversationColumnIndex := -1
	unreadColumnIndex := -1
	if showWhen {
		whenColumnIndex = len(columns)
		columns = append(columns, TableColumn{Header: "when", KeepWholeTokensWhenTerminalWidthAllows: true})
	}
	if showTrawler {
		trawlerColumnIndex = len(columns)
		columns = append(columns, TableColumn{Header: "trawler"})
	}
	if showLink {
		columns = append(columns, TableColumn{
			Header: "link", MinimumWidth: conversationListLinkMinimumWidth, NeverTruncateCellValues: true,
		})
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
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		values := make([]string, 0, len(columns))
		if showWhen {
			values = append(values, row.when)
		}
		if showTrawler {
			values = append(values, row.trawler)
		}
		if showLink {
			values = append(values, row.link)
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
		tableRows = append(tableRows, values)
	}
	outputWidth := OutputWidth(writer)
	renderColumns := conversationListRenderColumns(
		columns,
		tableRows,
		outputWidth,
		[]int{whenColumnIndex, trawlerColumnIndex, conversationColumnIndex, unreadColumnIndex},
	)
	if peopleColumnIndex >= 0 {
		for rowIndex := range rows {
			tableRows[rowIndex][peopleColumnIndex] = conversationParticipantDisplayNamesForRenderedPeopleColumn(
				rows[rowIndex],
				renderColumns[peopleColumnIndex],
			)
		}
	}
	if showLink {
		if _, err := fmt.Fprintf(writer, "Messages: %s messages --conversation LINK\n\n", TrawlInvocationDisplay(writer)); err != nil {
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
	record                    *conversationv1.ConversationRecord
	globallyRoutableTrawlLink string
	trawlerDisplayName        string
}

type conversationListRow struct {
	when         string
	trawler      string
	link         string
	conversation string
	people       string
	unread       string

	conversationParticipantDisplayNames                          []string
	numberOfDistinctConversationParticipantRecordsForHumanOutput uint64
}

func conversationParticipantDisplayNamesFromIdentitiesObservedByTrawlerArchive(
	participantIdentities []*conversationv1.ConversationParticipantIdentityObservedByTrawlerArchive,
) []string {
	displayNames := make([]string, 0, len(participantIdentities))
	for _, participantIdentity := range participantIdentities {
		if displayName := strings.TrimSpace(participantIdentity.GetPersonDisplayName()); displayName != "" {
			displayNames = append(displayNames, displayName)
		}
	}
	return displayNames
}

func conversationParticipantDisplayNamesWithUnavailableCount(
	conversationParticipantDisplayNames []string,
	numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive *uint64,
) string {
	return ConversationParticipantDisplayNamesPreviewForHumanOutput(
		conversationParticipantDisplayNames,
		resolveNumberOfDistinctConversationParticipantRecordsForHumanOutput(
			conversationParticipantDisplayNames,
			numberOfDistinctConversationParticipantRecordsObservedByTrawlerArchive,
		),
	)
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
	if numberOfConversationParticipantDisplayNamesInPreview > maximumConversationParticipantDisplayNamesInHumanPreview {
		numberOfConversationParticipantDisplayNamesInPreview = maximumConversationParticipantDisplayNamesInHumanPreview
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
			int64(row.numberOfDistinctConversationParticipantRecordsForHumanOutput),
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
	renderColumns := tableRenderColumns(columns, rows, naturalTableWidth)
	for _, columnIndex := range columnIndexesToHideBeforeNarrowingHumanContextBelowItsMinimumWidth {
		if columnIndex >= 0 && columnIndex < len(renderColumns) {
			renderColumns[columnIndex].HideBeforeTruncatingOtherColumnsBelowMinimumWidth = true
		}
	}
	fitRenderColumns(renderColumns, outputWidth)
	return renderColumns
}

func conversationParticipantDisplayNamesForRenderedPeopleColumn(
	row conversationListRow,
	peopleColumn renderColumn,
) string {
	for numberOfConversationParticipantDisplayNamesToShow :=
		len(row.conversationParticipantDisplayNames); ; numberOfConversationParticipantDisplayNamesToShow-- {
		preview := conversationParticipantDisplayNamesAndHiddenCount(
			row.conversationParticipantDisplayNames,
			numberOfConversationParticipantDisplayNamesToShow,
			row.numberOfDistinctConversationParticipantRecordsForHumanOutput,
		)
		if conversationPeoplePreviewFitsRenderedColumn(preview, peopleColumn) ||
			numberOfConversationParticipantDisplayNamesToShow == 0 {
			return preview
		}
	}
}

func conversationPeoplePreviewFitsRenderedColumn(preview string, peopleColumn renderColumn) bool {
	if strings.TrimSpace(preview) == "" {
		return true
	}
	if peopleColumn.Width <= 0 {
		return false
	}
	preview = HumanCell(peopleColumn.Header, preview)
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
