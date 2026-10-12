package render

import (
	"io"
	"strings"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func writeTrawlerSpecificCommandListPresentation(
	writer io.Writer,
	presentation *presentation.TrawlerSpecificCommandListPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	listRowActionsInDisplayOrder []*TrawlCommandAction,
) error {
	if presentation == nil {
		return nil
	}
	if len(presentation.GetRowsInDisplayOrder()) == 0 {
		if emptyText := strings.TrimSpace(presentation.GetConciseTextShownWhenListIsEmpty()); emptyText != "" {
			return writeLine(writer, emptyText)
		}
		return writeLine(writer, "Nothing matches.")
	}
	columnDisplayNames := append([]string(nil), presentation.GetColumnDisplayNamesInOrder()...)
	actionColumnIndex := -1
	for _, action := range listRowActionsInDisplayOrder {
		if action != nil && strings.TrimSpace(action.TrawlCommandActionDisplayName) != "" {
			actionColumnIndex = len(columnDisplayNames)
			columnDisplayNames = append(columnDisplayNames, action.TrawlCommandActionDisplayName)
			break
		}
	}
	displayedValuesByRow := make([][]string, 0, len(presentation.GetRowsInDisplayOrder()))
	columnHasDisplayedValue := make([]bool, len(columnDisplayNames))
	columnContainsGloballyRoutableTrawlLink := make([]bool, len(columnDisplayNames))
	columnContainsTrawlCommandAction := make([]bool, len(columnDisplayNames))
	columnContainsArchiveRecordAssociatedTime := make([]bool, len(columnDisplayNames))
	for rowIndex, row := range presentation.GetRowsInDisplayOrder() {
		if row == nil {
			continue
		}
		values := make([]string, len(columnDisplayNames))
		for columnIndex, value := range row.GetColumnValuesInDisplayOrder() {
			if columnIndex >= len(presentation.GetColumnDisplayNamesInOrder()) {
				break
			}
			displayedValue := presentationValueFromTrawlerSpecificCommand(
				value,
				globallyRoutableTrawlLinksByCanonicalRecordReference,
				localTimeWithUTCOffsetForListRow,
			)
			values[columnIndex] = displayedValue
			columnHasDisplayedValue[columnIndex] = columnHasDisplayedValue[columnIndex] || displayedValue != ""
			columnContainsGloballyRoutableTrawlLink[columnIndex] =
				columnContainsGloballyRoutableTrawlLink[columnIndex] ||
					presentationValueIsCanonicalRecordReference(value)
			columnContainsArchiveRecordAssociatedTime[columnIndex] =
				columnContainsArchiveRecordAssociatedTime[columnIndex] ||
					presentationValueIsArchiveRecordAssociatedTime(value)
		}
		if actionColumnIndex >= 0 && rowIndex < len(listRowActionsInDisplayOrder) {
			action := listRowActionsInDisplayOrder[rowIndex]
			if nextCommand := nextCommandForTrawlCommandAction(
				action,
				globallyRoutableTrawlLinksByCanonicalRecordReference,
			); !nextCommand.isEmpty() {
				values[actionColumnIndex] = formatTrawlCommandLine(writer, nextCommand.trawlCommandArgumentsAfterInvocation)
			}
			columnHasDisplayedValue[actionColumnIndex] =
				columnHasDisplayedValue[actionColumnIndex] || values[actionColumnIndex] != ""
			columnContainsTrawlCommandAction[actionColumnIndex] =
				columnContainsTrawlCommandAction[actionColumnIndex] || action != nil
		}
		displayedValuesByRow = append(displayedValuesByRow, values)
	}
	displayedColumnIndexes := make([]int, 0, len(columnDisplayNames))
	columns := make([]TableColumn, 0, len(columnDisplayNames))
	for columnIndex, columnDisplayName := range columnDisplayNames {
		if !columnHasDisplayedValue[columnIndex] {
			continue
		}
		column := TableColumn{
			Header:              columnDisplayName,
			Wrap:                true,
			MaximumWrappedLines: 2,
		}
		if columnContainsGloballyRoutableTrawlLink[columnIndex] {
			column.Header = columnHeaderLink
			column.NeverTruncateCellValues = true
		}
		if columnContainsTrawlCommandAction[columnIndex] {
			column.NeverTruncateCellValues = true
		}
		if columnContainsArchiveRecordAssociatedTime[columnIndex] {
			column = archiveEventTimeColumn(columnDisplayName)
		}
		columns = append(columns, column)
		displayedColumnIndexes = append(displayedColumnIndexes, columnIndex)
	}
	rows := make([][]string, 0, len(displayedValuesByRow))
	for _, values := range displayedValuesByRow {
		row := make([]string, 0, len(displayedColumnIndexes))
		for _, columnIndex := range displayedColumnIndexes {
			row = append(row, values[columnIndex])
		}
		rows = append(rows, row)
	}
	return writeTable(writer, columns, rows, -1)
}

func writeTrawlerSpecificCommandDetailPresentation(
	writer io.Writer,
	presentation *presentation.TrawlerSpecificCommandDetailPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	detailActionsInDisplayOrder []*TrawlCommandAction,
	openedRecordIsShownByThisDetail bool,
) error {
	if presentation == nil {
		return nil
	}
	fields := make([]cardField, 0, len(presentation.GetFieldsInDisplayOrder()))
	globallyRoutableTrawlLink := ""
	for _, field := range presentation.GetFieldsInDisplayOrder() {
		if field == nil {
			continue
		}
		value := presentationValueFromTrawlerSpecificCommand(
			field.GetFieldValue(),
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			localTimeWithUTCOffsetAndUTCForOpenedRecord,
		)
		fieldDisplayName := strings.TrimSpace(field.GetFieldDisplayName())
		if presentationValueIsCanonicalRecordReference(field.GetFieldValue()) {
			fieldDisplayName = columnHeaderLink
			if globallyRoutableTrawlLink == "" {
				globallyRoutableTrawlLink = value
			}
		}
		fields = append(fields, cardField{label: fieldDisplayName, value: value})
	}
	nextCommands := make([]NextCommand, 0, len(detailActionsInDisplayOrder)+1)
	for _, action := range detailActionsInDisplayOrder {
		nextCommands = append(nextCommands, nextCommandForTrawlCommandAction(
			action,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
		))
	}
	body := strings.TrimSpace(presentation.GetBodyText())
	if body == "" {
		body = strings.TrimSpace(presentation.GetBodyUnavailableExplanation())
	}
	if globallyRoutableTrawlLink != "" && !openedRecordIsShownByThisDetail {
		nextCommands = append(nextCommands, nextCommandWithLabel(nextCommandLabelOpenRecord, "open", globallyRoutableTrawlLink))
	}
	return writeCard(writer, card{
		title:        strings.TrimSpace(presentation.GetDetailDisplayName()),
		fields:       fields,
		body:         body,
		nextCommands: nextCommands,
	})
}
