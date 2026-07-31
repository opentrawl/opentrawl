package render

import (
	"fmt"
	"io"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

func WriteFederatedTrawlerArchiveSyncOperation(
	writer io.Writer,
	operation *federation.FederatedTrawlerArchiveSyncOperation,
) error {
	if operation == nil {
		return fmt.Errorf("federated trawler archive sync operation is missing")
	}
	rows := make(
		[]archiveSyncHumanRow,
		0,
		len(operation.GetTrawlerArchiveSyncResults())+
			len(operation.GetOperationFailures())+
			len(operation.GetTrawlersSkippedFromOperation())+
			len(operation.GetPeopleArchiveUpdateFailuresAfterTrawlerArchiveSync()),
	)
	for _, result := range operation.GetTrawlerArchiveSyncResults() {
		if result == nil {
			continue
		}
		rows = append(rows, archiveSyncHumanRow{
			trawler: strings.TrimSpace(result.GetRegisteredTrawlerDisplayName()),
			status:  "ok",
			changes: archiveSyncChanges(result),
		})
	}
	for _, failure := range operation.GetOperationFailures() {
		if failure == nil {
			continue
		}
		rows = append(rows, archiveSyncHumanRow{
			trawler: strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName()),
			status:  "not working",
		})
	}
	for _, skipped := range operation.GetTrawlersSkippedFromOperation() {
		if skipped == nil {
			continue
		}
		rows = append(rows, archiveSyncHumanRow{
			trawler: strings.TrimSpace(skipped.GetRegisteredTrawlerDisplayName()),
			status:  "not working",
		})
	}
	for _, failure := range operation.GetPeopleArchiveUpdateFailuresAfterTrawlerArchiveSync() {
		if failure == nil {
			continue
		}
		rows = append(rows, archiveSyncHumanRow{
			trawler: "People",
			status:  "not updated",
			changes: "from " + strings.TrimSpace(failure.GetSuccessfullySyncedTrawlerDisplayName()),
		})
	}
	return writeArchiveSyncHumanRows(writer, rows)
}

func writeArchiveSyncHumanRows(writer io.Writer, rows []archiveSyncHumanRow) error {
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
		line := padArchiveSyncCell(row.trawler, trawlerWidth) + "  " + padArchiveSyncCell(row.status, statusWidth)
		if row.changes != "" {
			line += "  " + row.changes
		}
		if _, err := fmt.Fprintln(writer, strings.TrimRight(line, " ")); err != nil {
			return err
		}
	}
	return nil
}

func padArchiveSyncCell(value string, width int) string {
	return value + strings.Repeat(" ", max(0, width-DisplayWidth(value)))
}

func archiveSyncChanges(result *federation.TrawlerArchiveSyncResult) string {
	report := result.GetTrawlerArchiveSyncReport()
	if report == nil {
		return ""
	}
	changes := make([]string, 0, 3)
	atLeastOneArchiveRecordChangeCountIsKnown := false
	if report.ArchiveRecordCountAddedByThisSync != nil {
		atLeastOneArchiveRecordChangeCountIsKnown = true
		if count := report.GetArchiveRecordCountAddedByThisSync(); count > 0 {
			changes = append(changes, FormatInteger(int64(count))+" added")
		}
	}
	if report.ArchiveRecordCountUpdatedByThisSync != nil {
		atLeastOneArchiveRecordChangeCountIsKnown = true
		if count := report.GetArchiveRecordCountUpdatedByThisSync(); count > 0 {
			changes = append(changes, FormatInteger(int64(count))+" updated")
		}
	}
	if report.ArchiveRecordCountRemovedByThisSync != nil {
		atLeastOneArchiveRecordChangeCountIsKnown = true
		if count := report.GetArchiveRecordCountRemovedByThisSync(); count > 0 {
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

type archiveSyncHumanRow struct {
	trawler string
	status  string
	changes string
}
