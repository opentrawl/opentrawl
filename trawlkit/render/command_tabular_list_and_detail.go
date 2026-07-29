package render

import (
	"fmt"
	"io"
	"strings"

	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

func WriteTrawlerSpecificCommandListPresentation(
	writer io.Writer,
	presentation *presentationv1.TrawlerSpecificCommandListPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference map[string]string,
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
	columnDisplayNames := presentation.GetColumnDisplayNamesInOrder()
	displayedValuesByRow := make([][]string, 0, len(presentation.GetRowsInDisplayOrder()))
	columnHasDisplayedValue := make([]bool, len(columnDisplayNames))
	columnContainsGloballyRoutableTrawlLink := make([]bool, len(columnDisplayNames))
	for _, row := range presentation.GetRowsInDisplayOrder() {
		if row == nil {
			continue
		}
		values := make([]string, len(columnDisplayNames))
		for columnIndex, value := range row.GetColumnValuesInDisplayOrder() {
			if columnIndex >= len(columnDisplayNames) {
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
	return WriteTable(writer, columns, rows)
}

func WriteTrawlerSpecificCommandDetailPresentation(
	writer io.Writer,
	presentation *presentationv1.TrawlerSpecificCommandDetailPresentation,
	globallyRoutableTrawlLinksByCanonicalRecordReference map[string]string,
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
