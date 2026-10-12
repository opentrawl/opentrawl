package render

import (
	"io"
	"strings"

	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
)

const (
	messageListMinimumWhoColumnWidth   = 10
	messageListMinimumWhereColumnWidth = 12
	// Every column of a row wraps to the same height, so a name is not cut
	// while the message beside it still has lines to fill.
	messageListMaximumWrappedLines = 3
	messageListOpenedMessageMarker = "→"
)

// messageListDisplayRow is one message in any list of messages: a
// conversation, a trawler's recent messages, or the context of an opened
// message.
type messageListDisplayRow struct {
	openedMessage             bool
	when                      string
	who                       string
	where                     string
	displayedMessageOrMedia   string
	globallyRoutableTrawlLink string
}

// writeMessageListRows writes messages as when, message, who, where and
// link, the same order as search results.
func writeMessageListRows(writer io.Writer, rows []messageListDisplayRow) error {
	if len(rows) == 0 {
		return nil
	}
	showOpenedMessageMarker := false
	showWhere := false
	showLinks := false
	for _, row := range rows {
		showOpenedMessageMarker = showOpenedMessageMarker || row.openedMessage
		showWhere = showWhere || row.where != ""
		showLinks = showLinks || row.globallyRoutableTrawlLink != ""
	}

	columns := make([]TableColumn, 0, 6)
	if showOpenedMessageMarker {
		columns = append(columns, TableColumn{Header: "", Width: 1, MinimumWidth: 1})
	}
	whenColumnIndex := len(columns)
	columns = append(columns,
		TableColumn{Header: columnHeaderWhen},
		TableColumn{Header: columnHeaderMessage, Wrap: true, MaximumWrappedLines: messageListMaximumWrappedLines},
		TableColumn{
			Header:              columnHeaderWho,
			MinimumWidth:        messageListMinimumWhoColumnWidth,
			Wrap:                true,
			MaximumWrappedLines: messageListMaximumWrappedLines,
		},
	)
	messageColumnIndex := whenColumnIndex + 1
	if showWhere {
		columns = append(columns, TableColumn{
			Header:              columnHeaderWhere,
			MinimumWidth:        messageListMinimumWhereColumnWidth,
			Wrap:                true,
			MaximumWrappedLines: messageListMaximumWrappedLines,
		})
	}
	if showLinks {
		columns = append(columns, TableColumn{Header: columnHeaderLink, NeverTruncateCellValues: true})
	}
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		tableRow := make([]string, 0, len(columns))
		if showOpenedMessageMarker {
			openedMessageMarker := ""
			if row.openedMessage {
				openedMessageMarker = messageListOpenedMessageMarker
			}
			tableRow = append(tableRow, openedMessageMarker)
		}
		tableRow = append(tableRow, row.when, row.displayedMessageOrMedia, row.who)
		if showWhere {
			tableRow = append(tableRow, row.where)
		}
		if showLinks {
			tableRow = append(tableRow, row.globallyRoutableTrawlLink)
		}
		tableRows = append(tableRows, tableRow)
	}
	columns[whenColumnIndex] = archiveEventTimeColumn(columnHeaderWhen)
	renderColumns := tableRenderColumnsWithPrimaryHumanContentColumn(
		columns,
		tableRows,
		OutputWidth(writer),
		messageColumnIndex,
	)
	if !listRowsFitAsTable(renderColumns, OutputWidth(writer)) {
		return writeFieldValueRows(writer, renderColumns, tableRows, OutputWidth(writer))
	}
	if err := writeRenderHeader(writer, renderColumns); err != nil {
		return err
	}
	for rowIndex, row := range rows {
		rowColumns := renderColumns
		if row.openedMessage {
			rowColumns = append([]renderColumn(nil), renderColumns...)
			rowColumns[messageColumnIndex].Clamp = 0
			rowColumns[messageColumnIndex].KeepWebAddressesWhole = true
		}
		if err := writeRenderRow(writer, rowColumns, tableRows[rowIndex]); err != nil {
			return err
		}
	}
	return nil
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
