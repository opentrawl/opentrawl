package render

import (
	"fmt"
	"io"
	"strings"
)

func writeMessageListRows(writer io.Writer, rows []messageListDisplayRow) error {
	outputWidth := OutputWidth(writer)
	if outputWidth >= messageListWideOutputMinimumWidth {
		return writeWideMessageListRows(writer, rows, outputWidth)
	}
	return writeNarrowMessageListRows(writer, rows, outputWidth)
}

func writeWideMessageListRows(writer io.Writer, rows []messageListDisplayRow, outputWidth int) error {
	showContext := false
	openLinkWidth := len("link")
	fromWidth := len("from")
	contextWidth := len("to / in")
	for _, row := range rows {
		showContext = showContext || row.recipientDisplayContext != "" || row.conversationDisplayContext != ""
		openLinkWidth = max(openLinkWidth, DisplayWidth(row.globallyRoutableTrawlLink))
		fromWidth = max(fromWidth, min(DisplayWidth(row.senderDisplayContext), messageListMaximumSenderColumnWidth))
		contextWidth = max(contextWidth, min(DisplayWidth(messageListCompactContext(row)), messageListMaximumContextWidth))
	}

	columnCount := 4
	if showContext {
		columnCount++
	}
	textWidth := outputWidth - messageListWhenColumnWidth - fromWidth - openLinkWidth - (columnCount-1)*len(renderTableGap)
	if showContext {
		textWidth -= contextWidth
	}
	if textWidth < messageListMinimumTextColumnWidth {
		return writeNarrowMessageListRows(writer, rows, outputWidth)
	}

	columns := []TableColumn{
		{Header: "when", Width: messageListWhenColumnWidth, MinimumWidth: messageListWhenColumnWidth},
		{Header: "from", Width: fromWidth, MinimumWidth: fromWidth},
		{Header: "text", Width: textWidth, MinimumWidth: textWidth},
	}
	if showContext {
		columns = append(columns, TableColumn{Header: "to / in", Width: contextWidth, MinimumWidth: contextWidth})
	}
	columns = append(columns, TableColumn{
		Header:                  "link",
		Width:                   openLinkWidth,
		MinimumWidth:            openLinkWidth,
		NeverTruncateCellValues: true,
	})

	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		tableRow := []string{row.when, row.senderDisplayContext, row.displayedMessageOrMedia}
		if showContext {
			tableRow = append(tableRow, messageListCompactContext(row))
		}
		tableRows = append(tableRows, append(tableRow, row.globallyRoutableTrawlLink))
	}
	renderColumns := tableRenderColumns(columns, tableRows, outputWidth)
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

func writeNarrowMessageListRows(writer io.Writer, rows []messageListDisplayRow, outputWidth int) error {
	fromWidth := len("from")
	for _, row := range rows {
		fromWidth = max(fromWidth, min(DisplayWidth(row.senderDisplayContext), messageListMaximumSenderColumnWidth))
	}
	textWidth := outputWidth - messageListWhenColumnWidth - fromWidth - 2*len(renderTableGap)
	if textWidth < 1 {
		textWidth = 1
	}
	columns := []renderColumn{
		{Width: messageListWhenColumnWidth},
		{Width: fromWidth},
		{Width: textWidth, Wrap: true, Clamp: 2},
	}
	if err := writeRenderRowWithMode(writer, columns, []string{"when", "from", "text"}, true); err != nil {
		return err
	}
	for _, row := range rows {
		if err := writeRenderRowWithMode(
			writer,
			columns,
			[]string{row.when, row.senderDisplayContext, row.displayedMessageOrMedia},
			false,
		); err != nil {
			return err
		}
		if err := writeMessageListGroupedMetadata(writer, row, outputWidth); err != nil {
			return err
		}
	}
	return nil
}

func messageListCompactContext(row messageListDisplayRow) string {
	contextParts := make([]string, 0, 2)
	if row.recipientDisplayContext != "" {
		contextParts = append(contextParts, "to "+row.recipientDisplayContext)
	}
	if row.conversationDisplayContext != "" {
		contextParts = append(contextParts, "in "+row.conversationDisplayContext)
	}
	return strings.Join(contextParts, " · ")
}

func writeMessageListGroupedMetadata(writer io.Writer, row messageListDisplayRow, outputWidth int) error {
	contextParts := make([]string, 0, 2)
	if row.recipientDisplayContext != "" {
		contextParts = append(contextParts, "To: "+row.recipientDisplayContext)
	}
	if row.conversationDisplayContext != "" {
		contextParts = append(contextParts, "Conversation: "+row.conversationDisplayContext)
	}
	context := strings.Join(contextParts, " · ")
	openCommand := trawlCommandLineForDisplay(writer, []string{"open", row.globallyRoutableTrawlLink})
	groupedMetadata := strings.Trim(strings.Join([]string{context, "Open: " + openCommand}, " · "), " ·")
	if DisplayWidth("  "+groupedMetadata) <= outputWidth {
		_, err := fmt.Fprintln(writer, "  "+groupedMetadata)
		return err
	}
	if context != "" {
		for _, line := range WrapWithIndent("  ", context, outputWidth, "  ") {
			if _, err := fmt.Fprintln(writer, line); err != nil {
				return err
			}
		}
	}
	for _, line := range shellCommandLines("  Open: ", "  ", openCommand, outputWidth) {
		if _, err := fmt.Fprintln(writer, line); err != nil {
			return err
		}
	}
	return nil
}
