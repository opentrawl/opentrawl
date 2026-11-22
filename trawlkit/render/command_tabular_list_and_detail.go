package render

import (
	"io"
	"strings"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func writeTrawlerSpecificCommandListPresentation(
	writer io.Writer,
	listPresentation *presentation.TrawlerSpecificCommandListPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	listRowActionsInDisplayOrder []*TrawlCommandAction,
) error {
	if listPresentation == nil {
		return nil
	}
	sections := listPresentation.GetSectionsInDisplayOrder()
	if len(sections) == 0 && len(listPresentation.GetRowsInDisplayOrder()) == 0 {
		if emptyText := strings.TrimSpace(listPresentation.GetConciseTextShownWhenListIsEmpty()); emptyText != "" {
			return writeLine(writer, emptyText)
		}
		if summaryHeading := strings.TrimSpace(listPresentation.GetListSummaryHeading()); summaryHeading != "" {
			return writeLine(writer, summaryHeading)
		}
		return writeLine(writer, "Nothing matches.")
	}
	wroteHeading := false
	if summaryHeading := strings.TrimSpace(listPresentation.GetListSummaryHeading()); summaryHeading != "" {
		if err := writeLines(writer, Wrap(summaryHeading, OutputWidth(writer))); err != nil {
			return err
		}
		wroteHeading = true
	}
	if len(sections) > 0 {
		for sectionIndex, section := range sections {
			if section == nil {
				continue
			}
			if wroteHeading || sectionIndex > 0 {
				if err := writeLine(writer, ""); err != nil {
					return err
				}
			}
			if heading := strings.TrimSpace(section.GetSectionHeading()); heading != "" {
				if err := writeLines(writer, Wrap(heading, OutputWidth(writer))); err != nil {
					return err
				}
			}
			if err := writeTrawlerSpecificTable(
				writer,
				section.GetColumnDisplayNamesInOrder(),
				section.GetRowsInDisplayOrder(),
				globallyRoutableTrawlLinksByCanonicalRecordReference,
				nil,
			); err != nil {
				return err
			}
		}
	} else {
		if wroteHeading {
			if err := writeLine(writer, ""); err != nil {
				return err
			}
		}
		if err := writeTrawlerSpecificTable(
			writer,
			listPresentation.GetColumnDisplayNamesInOrder(),
			listPresentation.GetRowsInDisplayOrder(),
			globallyRoutableTrawlLinksByCanonicalRecordReference,
			listRowActionsInDisplayOrder,
		); err != nil {
			return err
		}
	}
	for _, footnote := range listPresentation.GetFootnotesInDisplayOrder() {
		footnote = strings.TrimSpace(footnote)
		if footnote == "" {
			continue
		}
		if err := writeLine(writer, ""); err != nil {
			return err
		}
		if err := writeLines(writer, Wrap(footnote, OutputWidth(writer))); err != nil {
			return err
		}
	}
	return nil
}

func writeTrawlerSpecificTable(
	writer io.Writer,
	rawColumnDisplayNames []string,
	rawRows []*presentation.TrawlerSpecificCommandListPresentationRow,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	listRowActionsInDisplayOrder []*TrawlCommandAction,
) error {
	columnDisplayNames := append([]string(nil), rawColumnDisplayNames...)
	actionColumnIndex := -1
	for _, action := range listRowActionsInDisplayOrder {
		if action != nil && strings.TrimSpace(action.TrawlCommandActionDisplayName) != "" {
			actionColumnIndex = len(columnDisplayNames)
			columnDisplayNames = append(columnDisplayNames, action.TrawlCommandActionDisplayName)
			break
		}
	}
	displayedValuesByRow := make([][]string, 0, len(rawRows))
	columnHasDisplayedValue := make([]bool, len(columnDisplayNames))
	columnContainsGloballyRoutableTrawlLink := make([]bool, len(columnDisplayNames))
	columnContainsTrawlCommandAction := make([]bool, len(columnDisplayNames))
	columnContainsArchiveRecordAssociatedTime := make([]bool, len(columnDisplayNames))
	columnContainsExactMonetaryAmount := make([]bool, len(columnDisplayNames))
	for rowIndex, row := range rawRows {
		if row == nil {
			continue
		}
		values := make([]string, len(columnDisplayNames))
		for columnIndex, value := range row.GetColumnValuesInDisplayOrder() {
			if columnIndex >= len(rawColumnDisplayNames) {
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
			columnContainsExactMonetaryAmount[columnIndex] =
				columnContainsExactMonetaryAmount[columnIndex] ||
					presentationValueIsExactMonetaryAmount(value)
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
		if columnContainsExactMonetaryAmount[columnIndex] {
			column.AlignRight = true
			column.NeverTruncateCellValues = true
			column.Wrap = false
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

func DeclarativeNextCommandsForHumanOutput(
	commands []*presentation.DeclarativeTrawlCommand,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
) []NextCommand {
	nextCommands := make([]NextCommand, 0, len(commands))
	for _, cmd := range commands {
		if cmd == nil {
			continue
		}
		args := make([]string, 0, len(cmd.GetCommandArgumentsAfterTrawlInvocation()))
		missingLink := false
		for _, arg := range cmd.GetCommandArgumentsAfterTrawlInvocation() {
			switch typedArg := arg.GetArgumentValue().(type) {
			case *presentation.DeclarativeTrawlCommandArgument_TextArgument:
				args = append(args, strings.TrimSpace(typedArg.TextArgument))
			case *presentation.DeclarativeTrawlCommandArgument_CanonicalRecordReferenceArgument:
				link := globallyRoutableTrawlLinkText(
					globallyRoutableTrawlLinksByCanonicalRecordReference.
						globallyRoutableTrawlLinkForCanonicalArchiveRecordReference(typedArg.CanonicalRecordReferenceArgument),
				)
				if link == "" {
					missingLink = true
					break
				}
				args = append(args, link)
			}
		}
		if missingLink || len(args) == 0 {
			continue
		}
		nextCommands = append(nextCommands, nextCommandWithLabel(
			nextCommandLabel(strings.TrimSpace(cmd.GetCommandLabel())),
			args...,
		))
	}
	return nextCommands
}

func writeTrawlerSpecificCommandDetailPresentation(
	writer io.Writer,
	detailPresentation *presentation.TrawlerSpecificCommandDetailPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	detailActionsInDisplayOrder []*TrawlCommandAction,
	openedRecordIsShownByThisDetail bool,
) error {
	if detailPresentation == nil {
		return nil
	}
	fields := make([]cardField, 0, len(detailPresentation.GetFieldsInDisplayOrder()))
	globallyRoutableTrawlLink := ""
	for _, field := range detailPresentation.GetFieldsInDisplayOrder() {
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
			if fieldDisplayName == "" {
				fieldDisplayName = columnHeaderLink
			}
			if globallyRoutableTrawlLink == "" {
				globallyRoutableTrawlLink = value
			}
		}
		fields = append(fields, cardField{label: fieldDisplayName, value: value})
	}
	nextCommands := make([]NextCommand, 0, len(detailActionsInDisplayOrder)+len(detailPresentation.GetNextCommandsInDisplayOrder())+1)
	for _, action := range detailActionsInDisplayOrder {
		nextCommands = append(nextCommands, nextCommandForTrawlCommandAction(
			action,
			globallyRoutableTrawlLinksByCanonicalRecordReference,
		))
	}
	nextCommands = append(nextCommands, DeclarativeNextCommandsForHumanOutput(
		detailPresentation.GetNextCommandsInDisplayOrder(),
		globallyRoutableTrawlLinksByCanonicalRecordReference,
	)...)
	body := strings.TrimSpace(detailPresentation.GetBodyText())
	if body == "" {
		body = strings.TrimSpace(detailPresentation.GetBodyUnavailableExplanation())
	}
	if globallyRoutableTrawlLink != "" && !openedRecordIsShownByThisDetail {
		nextCommands = append(nextCommands, nextCommandWithLabel(nextCommandLabelOpenRecord, "open", globallyRoutableTrawlLink))
	}
	return writeCard(writer, card{
		title:        strings.TrimSpace(detailPresentation.GetDetailDisplayName()),
		subtitle:     strings.TrimSpace(detailPresentation.GetDetailSubtitle()),
		fields:       fields,
		body:         body,
		footer:       strings.TrimSpace(detailPresentation.GetDetailFooterText()),
		nextCommands: nextCommands,
	})
}
