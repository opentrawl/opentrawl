package render

import (
	"fmt"
	"io"
	"strings"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func WriteTrawlerSpecificCommandListPresentation(
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
			_, err := fmt.Fprintln(writer, emptyText)
			return err
		}
		return nil
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
			)
			values[columnIndex] = displayedValue
			columnHasDisplayedValue[columnIndex] = columnHasDisplayedValue[columnIndex] || displayedValue != ""
			columnContainsGloballyRoutableTrawlLink[columnIndex] =
				columnContainsGloballyRoutableTrawlLink[columnIndex] ||
					presentationValueIsCanonicalRecordReference(value)
		}
		if actionColumnIndex >= 0 && rowIndex < len(listRowActionsInDisplayOrder) {
			action := listRowActionsInDisplayOrder[rowIndex]
			values[actionColumnIndex] = trawlCommandActionLineForDisplay(
				writer,
				action,
				globallyRoutableTrawlLinksByCanonicalRecordReference,
			)
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
			column.Header = "link"
			column.NeverTruncateCellValues = true
		}
		if columnContainsTrawlCommandAction[columnIndex] {
			column.NeverTruncateCellValues = true
			column.CellValueIsTrawlCommandAction = true
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
	return writeTrawlerSpecificCommandListRows(writer, columns, rows)
}

func writeTrawlerSpecificCommandListRows(
	writer io.Writer,
	columns []TableColumn,
	rows [][]string,
) error {
	if len(columns) == 0 || len(rows) == 0 {
		return nil
	}
	outputWidth := readableTableOutputWidth(writer)
	renderColumns := tableRenderColumns(columns, rows, outputWidth)
	actionColumnIndex := trawlCommandActionColumnIndex(columns)
	if actionColumnIndex < 0 || !tableNeedsFieldValueRows(renderColumns, outputWidth) {
		return writeTrawlerSpecificCommandComparableRows(
			writer,
			renderColumns,
			rows,
			nil,
			actionColumnIndex,
		)
	}

	columnsWithoutAction := append([]TableColumn(nil), columns[:actionColumnIndex]...)
	columnsWithoutAction = append(columnsWithoutAction, columns[actionColumnIndex+1:]...)
	rowsWithoutAction := make([][]string, 0, len(rows))
	actionsInRowOrder := make([]string, 0, len(rows))
	for _, row := range rows {
		rowWithoutAction := append([]string(nil), row[:actionColumnIndex]...)
		if actionColumnIndex+1 < len(row) {
			rowWithoutAction = append(rowWithoutAction, row[actionColumnIndex+1:]...)
		}
		rowsWithoutAction = append(rowsWithoutAction, rowWithoutAction)
		actionsInRowOrder = append(actionsInRowOrder, tableRowValue(row, actionColumnIndex))
	}
	return writeTrawlerSpecificCommandComparableRows(
		writer,
		tableRenderColumns(columnsWithoutAction, rowsWithoutAction, outputWidth),
		rowsWithoutAction,
		actionsInRowOrder,
		-1,
	)
}

func trawlCommandActionColumnIndex(columns []TableColumn) int {
	for columnIndex, column := range columns {
		if column.CellValueIsTrawlCommandAction {
			return columnIndex
		}
	}
	return -1
}

func writeTrawlerSpecificCommandComparableRows(
	writer io.Writer,
	renderColumns []renderColumn,
	rows [][]string,
	actionsInRowOrder []string,
	actionColumnIndex int,
) error {
	writer = tableHumanOutputWriter{writer: writer}
	if err := writeRenderHeader(writer, renderColumns); err != nil {
		return err
	}
	for rowIndex, row := range rows {
		if err := writeRenderRow(writer, renderColumns, row); err != nil {
			return err
		}
		if actionColumnIndex >= 0 || rowIndex >= len(actionsInRowOrder) {
			continue
		}
		action := strings.TrimSpace(actionsInRowOrder[rowIndex])
		if action == "" {
			continue
		}
		if err := WriteIndentedTrawlCommand(writer, action); err != nil {
			return err
		}
	}
	return nil
}

func WriteTrawlerSpecificCommandDetailPresentation(
	writer io.Writer,
	presentation *presentation.TrawlerSpecificCommandDetailPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference GloballyRoutableTrawlLinksByCanonicalArchiveRecordReference,
	detailActionsInDisplayOrder []*TrawlCommandAction,
) error {
	if presentation == nil {
		return nil
	}
	fields := make([]CardField, 0, len(presentation.GetFieldsInDisplayOrder()))
	globallyRoutableTrawlLink := ""
	for _, field := range presentation.GetFieldsInDisplayOrder() {
		if field == nil {
			continue
		}
		value := presentationValueFromTrawlerSpecificCommand(
			field.GetFieldValue(),
			globallyRoutableTrawlLinksByCanonicalRecordReference,
		)
		fieldDisplayName := strings.TrimSpace(field.GetFieldDisplayName())
		if presentationValueIsCanonicalRecordReference(field.GetFieldValue()) {
			fieldDisplayName = "link"
			if globallyRoutableTrawlLink == "" {
				globallyRoutableTrawlLink = value
			}
		}
		fields = append(fields, CardField{Label: fieldDisplayName, Value: value})
	}
	for _, action := range detailActionsInDisplayOrder {
		if action == nil {
			continue
		}
		fields = append(fields, CardField{
			Label:                     action.TrawlCommandActionDisplayName,
			Value:                     trawlCommandActionLineForDisplay(writer, action, globallyRoutableTrawlLinksByCanonicalRecordReference),
			ValueIsTrawlCommandAction: true,
		})
	}
	body := strings.TrimSpace(presentation.GetBodyText())
	if body == "" {
		body = strings.TrimSpace(presentation.GetBodyUnavailableExplanation())
	}
	var hints []string
	if globallyRoutableTrawlLink != "" {
		hints = []string{"Open: " + trawlCommandLineForDisplay(writer, []string{"open", globallyRoutableTrawlLink})}
	}
	return WriteCard(writer, Card{
		Title:  strings.TrimSpace(presentation.GetDetailDisplayName()),
		Fields: fields,
		Body:   body,
		Hints:  hints,
	})
}
