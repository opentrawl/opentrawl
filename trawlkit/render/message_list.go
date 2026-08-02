package render

import (
	"io"
	"strings"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

const (
	messageListTimeColumnWidth           = 16
	messageListMinimumSenderColumnWidth  = 10
	messageListMinimumContextColumnWidth = 16
)

func writeMessageListRows(writer io.Writer, rows []messageListDisplayRow) error {
	if len(rows) == 0 {
		return nil
	}
	outputWidth := OutputWidth(writer)
	showSelection := false
	showContext := false
	showLinks := false
	for _, row := range rows {
		showSelection = showSelection || row.selected
		showContext = showContext || messageListCompactContext(row) != ""
		showLinks = showLinks || row.globallyRoutableTrawlLink != ""
	}

	columns := make([]TableColumn, 0, 6)
	if showSelection {
		columns = append(columns, TableColumn{Header: "", Width: 1, MinimumWidth: 1})
	}
	columns = append(columns,
		TableColumn{Header: "time", Width: messageListTimeColumnWidth, MinimumWidth: messageListTimeColumnWidth},
		TableColumn{
			Header:              "from",
			MinimumWidth:        messageListMinimumSenderColumnWidth,
			Wrap:                true,
			MaximumWrappedLines: 3,
		},
		TableColumn{
			Header:              "message",
			Wrap:                true,
			MaximumWrappedLines: 3,
		},
	)
	messageColumnIndex := 2
	if showSelection {
		messageColumnIndex++
	}
	contextColumnIndex := -1
	if showContext {
		contextColumnIndex = len(columns)
		columns = append(columns, TableColumn{
			Header:              "context",
			MinimumWidth:        messageListMinimumContextColumnWidth,
			Wrap:                true,
			MaximumWrappedLines: 2,
		})
	}
	if showLinks {
		columns = append(columns, TableColumn{
			Header:                  "link",
			NeverTruncateCellValues: true,
		})
	}
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		tableRow := make([]string, 0, len(columns))
		if showSelection {
			selectionMarker := ""
			if row.selected {
				selectionMarker = "→"
			}
			tableRow = append(tableRow, selectionMarker)
		}
		tableRow = append(tableRow, row.when, row.senderDisplayContext, row.displayedMessageOrMedia)
		if contextColumnIndex >= 0 {
			tableRow = append(tableRow, messageListCompactContext(row))
		}
		if showLinks {
			tableRow = append(tableRow, row.globallyRoutableTrawlLink)
		}
		tableRows = append(tableRows, tableRow)
	}
	renderColumns := tableRenderColumnsWithPrimaryHumanContentColumn(
		columns,
		tableRows,
		outputWidth,
		messageColumnIndex,
	)

	if err := writeRenderHeader(writer, renderColumns); err != nil {
		return err
	}
	for rowIndex, row := range rows {
		rowColumns := renderColumns
		if row.selected {
			rowColumns = append([]renderColumn(nil), renderColumns...)
			rowColumns[messageColumnIndex].Clamp = 0
		}
		if err := writeRenderRow(writer, rowColumns, tableRows[rowIndex]); err != nil {
			return err
		}
	}
	return nil
}

func messageListCompactContext(row messageListDisplayRow) string {
	contextParts := make([]string, 0, 2)
	if row.recipientDisplayContext != "" &&
		(!strings.EqualFold(row.recipientDisplayContext, "me") || strings.EqualFold(row.senderDisplayContext, "me")) {
		contextParts = append(contextParts, "to "+row.recipientDisplayContext)
	}
	conversationDisplayName := strings.TrimSpace(row.conversationDisplayName)
	if conversationDisplayName != "" &&
		!strings.EqualFold(conversationDisplayName, strings.TrimSpace(row.senderDisplayContext)) &&
		!strings.EqualFold(conversationDisplayName, strings.TrimSpace(row.recipientDisplayContext)) {
		contextParts = append(contextParts, conversationDisplayName)
	}
	return strings.Join(contextParts, " · ")
}

func messageTextAndMediaForHumanOutput(messageText string, media *message.MessageMedia) string {
	messageText = strings.TrimSpace(messageText)
	if media == nil {
		return messageText
	}
	mediaDescription := messageMediaContentKindDisplayName(media.GetMessageMediaContentKind())
	if mediaDescription == "" {
		mediaDescription = "Attachment"
	}
	if mediaTitle := strings.TrimSpace(media.GetMessageMediaTitle()); mediaTitle != "" {
		mediaDescription += ": " + mediaTitle
	}
	if messageText == "" {
		return mediaDescription
	}
	return messageText + " · " + mediaDescription
}
