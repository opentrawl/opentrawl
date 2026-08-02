package render

import (
	"io"
	"strings"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

const (
	messageListTimeColumnWidth           = 16
	messageListMinimumSenderColumnWidth  = 8
	messageListMinimumContextColumnWidth = 10
)

func writeMessageListRows(writer io.Writer, rows []messageListDisplayRow) error {
	if len(rows) == 0 {
		return nil
	}
	outputWidth := OutputWidth(writer)
	showSelection := false
	showContext := false
	showLinks := false
	maximumSenderWidth := len("from")
	maximumMessageWidth := len("message")
	maximumContextWidth := len("context")
	maximumLinkWidth := len("link")
	protectedMessageWidth := 28
	if outputWidth >= 99 {
		protectedMessageWidth = 40
	}
	if outputWidth >= 200 {
		protectedMessageWidth = 64
	}
	for _, row := range rows {
		showSelection = showSelection || row.selected
		showContext = showContext || messageListCompactContext(row) != ""
		showLinks = showLinks || row.globallyRoutableTrawlLink != ""
		maximumSenderWidth = max(maximumSenderWidth, DisplayWidth(row.senderDisplayContext))
		maximumMessageWidth = max(maximumMessageWidth, DisplayWidth(row.displayedMessageOrMedia))
		maximumContextWidth = max(maximumContextWidth, DisplayWidth(messageListCompactContext(row)))
		maximumLinkWidth = max(maximumLinkWidth, DisplayWidth(row.globallyRoutableTrawlLink))
	}

	columns := make([]renderColumn, 0, 6)
	if showSelection {
		columns = append(columns, renderColumn{Header: "", Width: 1, MinimumWidth: 1})
	}
	columns = append(columns,
		renderColumn{Header: "time", Width: messageListTimeColumnWidth, MinimumWidth: messageListTimeColumnWidth},
		renderColumn{
			Header:       "from",
			Width:        min(maximumSenderWidth, messageListMinimumSenderColumnWidth),
			MinimumWidth: min(maximumSenderWidth, messageListMinimumSenderColumnWidth),
		},
		renderColumn{
			Header:       "message",
			Width:        min(maximumMessageWidth, protectedMessageWidth),
			MinimumWidth: min(maximumMessageWidth, protectedMessageWidth),
			Wrap:         true,
			Clamp:        2,
		},
	)
	contextColumnIndex := -1
	if showContext {
		contextColumnIndex = len(columns)
		columns = append(columns, renderColumn{
			Header:       "context",
			Width:        min(maximumContextWidth, messageListMinimumContextColumnWidth),
			MinimumWidth: min(maximumContextWidth, messageListMinimumContextColumnWidth),
		})
	}
	if showLinks {
		columns = append(columns, renderColumn{
			Header:                  "link",
			Width:                   maximumLinkWidth,
			MinimumWidth:            maximumLinkWidth,
			NeverTruncateCellValues: true,
		})
	}

	senderColumnIndex := 1
	if showSelection {
		senderColumnIndex++
	}
	useRemainingMessageListWidth(columns, outputWidth, senderColumnIndex, maximumSenderWidth)
	if contextColumnIndex >= 0 {
		useRemainingMessageListWidth(columns, outputWidth, contextColumnIndex, maximumContextWidth)
	}
	messageColumnIndex := 2
	if showSelection {
		messageColumnIndex++
	}
	useRemainingMessageListWidth(columns, outputWidth, messageColumnIndex, maximumMessageWidth)
	fitRenderColumns(columns, outputWidth)

	if err := writeRenderHeader(writer, columns); err != nil {
		return err
	}
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
		if showContext {
			tableRow = append(tableRow, messageListCompactContext(row))
		}
		if showLinks {
			tableRow = append(tableRow, row.globallyRoutableTrawlLink)
		}
		rowColumns := columns
		if row.selected {
			rowColumns = append([]renderColumn(nil), columns...)
			rowColumns[messageColumnIndex].Clamp = 0
		}
		if err := writeRenderRow(writer, rowColumns, tableRow); err != nil {
			return err
		}
	}
	return nil
}

func useRemainingMessageListWidth(columns []renderColumn, outputWidth, columnIndex, maximumWidth int) {
	if columnIndex < 0 || columnIndex >= len(columns) {
		return
	}
	availableWidth := outputWidth - renderColumnsWidth(columns)
	if availableWidth <= 0 {
		return
	}
	columns[columnIndex].Width += min(availableWidth, maximumWidth-columns[columnIndex].Width)
}

func messageListCompactContext(row messageListDisplayRow) string {
	contextParts := make([]string, 0, 2)
	if row.recipientDisplayContext != "" &&
		!(strings.EqualFold(row.recipientDisplayContext, "me") && !strings.EqualFold(row.senderDisplayContext, "me")) {
		contextParts = append(contextParts, "to "+row.recipientDisplayContext)
	}
	conversationDisplayName := strings.TrimSpace(row.conversationDisplayName)
	if conversationDisplayName != "" &&
		!strings.EqualFold(conversationDisplayName, strings.TrimSpace(row.senderDisplayContext)) &&
		!strings.EqualFold(conversationDisplayName, strings.TrimSpace(row.recipientDisplayContext)) {
		contextParts = append(contextParts, "in "+conversationDisplayName)
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
