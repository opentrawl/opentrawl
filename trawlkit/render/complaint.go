package render

import (
	"strconv"
	"strings"

	complaint "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/complaint"
)

const (
	columnHeaderComplaint = "complaint"
	columnHeaderAbout     = "about"
	columnHeaderFrom      = "from"
)

// WriteTrawlComplaintSaved confirms a complaint and says who reads it.
func (output TrawlCommandOutput) WriteTrawlComplaintSaved() error {
	writer := output.standardOutput
	if err := writeLines(writer, Wrap("Saved. The owner of this computer reads complaints to fix what was hard to use or read.", OutputWidth(writer))); err != nil {
		return err
	}
	return writeNextCommands(writer, []NextCommand{nextCommandWithLabel(nextCommandLabelComplaints, "complaints")})
}

// WriteTrawlComplaintList writes complaints newest first, each whole.
func (output TrawlCommandOutput) WriteTrawlComplaintList(
	response *complaint.TrawlComplaintListResponse,
	maximumReturnedComplaintCount int,
) error {
	writer := output.standardOutput
	complaints := response.GetTrawlComplaintsNewestFirst()
	if len(complaints) == 0 {
		return writeLines(writer, []string{"No complaints yet."})
	}
	showAbout, showFrom := false, false
	for _, savedComplaint := range complaints {
		showAbout = showAbout || strings.TrimSpace(savedComplaint.GetTrawlCommandComplainedAbout()) != ""
		showFrom = showFrom || strings.TrimSpace(savedComplaint.GetComplainingAgentConversationLink()) != ""
	}
	columns := []TableColumn{
		archiveEventTimeColumn(columnHeaderWhen),
		{Header: columnHeaderComplaint, Wrap: true},
	}
	if showAbout {
		columns = append(columns, TableColumn{Header: columnHeaderAbout, Wrap: true, MinimumWidth: 12})
	}
	if showFrom {
		columns = append(columns, TableColumn{Header: columnHeaderFrom, NeverTruncateCellValues: true})
	}
	rows := make([][]string, 0, len(complaints))
	for _, savedComplaint := range complaints {
		row := []string{
			formatExactTimeInThisComputerTimeZone(savedComplaint.GetComplaintTime(), localTimeWithUTCOffsetForListRow),
			strings.TrimSpace(savedComplaint.GetComplaintText()),
		}
		if showAbout {
			row = append(row, strings.TrimSpace(savedComplaint.GetTrawlCommandComplainedAbout()))
		}
		if showFrom {
			row = append(row, strings.TrimSpace(savedComplaint.GetComplainingAgentConversationLink()))
		}
		rows = append(rows, row)
	}
	if err := writeHumanRecordRowsWithPrimaryContentColumn(writer, columns, rows, 1); err != nil {
		return err
	}
	if response.GetTotalTrawlComplaintCount() <= uint64(len(complaints)) {
		return nil
	}
	return writeNextCommands(writer, []NextCommand{NextCommandShowingMoreRows(
		[]string{"complaints", "--limit", strconv.Itoa(maximumReturnedComplaintCount * 2)},
	)})
}
