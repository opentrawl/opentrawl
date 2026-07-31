package render

import (
	"fmt"
	"io"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

func WriteFederatedTrawlerArchiveUpdateOperation(
	writer io.Writer,
	operation *federation.FederatedTrawlerArchiveUpdateOperation,
) error {
	if operation == nil {
		return fmt.Errorf("federated trawler archive update operation is missing")
	}
	rows := make(
		[]archiveUpdateHumanRow,
		0,
		len(operation.GetTrawlerArchiveUpdateResults())+
			len(operation.GetOperationFailures())+
			len(operation.GetTrawlersSkippedFromOperation())+
			len(operation.GetPeopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate()),
	)
	for _, result := range operation.GetTrawlerArchiveUpdateResults() {
		if result == nil {
			continue
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: strings.TrimSpace(result.GetRegisteredTrawlerDisplayName()),
			status:  "ok",
			changes: archiveUpdateChanges(result),
		})
	}
	for _, failure := range operation.GetOperationFailures() {
		if failure == nil {
			continue
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName()),
			status:  "not working",
		})
	}
	for _, skipped := range operation.GetTrawlersSkippedFromOperation() {
		if skipped == nil {
			continue
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: strings.TrimSpace(skipped.GetRegisteredTrawlerDisplayName()),
			status:  "not working",
		})
	}
	for _, failure := range operation.GetPeopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate() {
		if failure == nil {
			continue
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: "People",
			status:  "not updated",
			changes: "from " + strings.TrimSpace(failure.GetSuccessfullyUpdatedTrawlerDisplayName()),
		})
	}
	return writeArchiveUpdateHumanRows(writer, rows)
}

func writeArchiveUpdateHumanRows(writer io.Writer, rows []archiveUpdateHumanRow) error {
	if len(rows) == 0 {
		return nil
	}
	trawlerWidth := 0
	statusWidth := 0
	for _, row := range rows {
		trawlerWidth = max(trawlerWidth, DisplayWidth(row.trawler))
		statusWidth = max(statusWidth, DisplayWidth(row.status))
	}
	for _, row := range rows {
		line := padArchiveUpdateCell(row.trawler, trawlerWidth) + "  " + padArchiveUpdateCell(row.status, statusWidth)
		if row.changes != "" {
			line += "  " + row.changes
		}
		if _, err := fmt.Fprintln(writer, strings.TrimRight(line, " ")); err != nil {
			return err
		}
	}
	return nil
}

func padArchiveUpdateCell(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-DisplayWidth(value)))
}

func archiveUpdateChanges(result *federation.TrawlerArchiveUpdateResult) string {
	report := result.GetTrawlerArchiveUpdateReport()
	if report == nil {
		return ""
	}
	changes := make([]string, 0, 3)
	atLeastOneArchiveRecordChangeCountIsKnown := false
	if report.ArchiveRecordCountAddedByThisUpdate != nil {
		atLeastOneArchiveRecordChangeCountIsKnown = true
		if count := report.GetArchiveRecordCountAddedByThisUpdate(); count > 0 {
			changes = append(changes, FormatInteger(int64(count))+" added")
		}
	}
	if report.ArchiveRecordCountUpdatedByThisUpdate != nil {
		atLeastOneArchiveRecordChangeCountIsKnown = true
		if count := report.GetArchiveRecordCountUpdatedByThisUpdate(); count > 0 {
			changes = append(changes, FormatInteger(int64(count))+" updated")
		}
	}
	if report.ArchiveRecordCountRemovedByThisUpdate != nil {
		atLeastOneArchiveRecordChangeCountIsKnown = true
		if count := report.GetArchiveRecordCountRemovedByThisUpdate(); count > 0 {
			changes = append(changes, FormatInteger(int64(count))+" removed")
		}
	}
	if !atLeastOneArchiveRecordChangeCountIsKnown {
		return ""
	}
	if len(changes) == 0 {
		return "no changes"
	}
	return strings.Join(changes, ", ")
}

type archiveUpdateHumanRow struct {
	trawler string
	status  string
	changes string
}
