package render

import (
	"fmt"
	"io"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
)

// WriteArchiveUpdate writes one row per app: whether its archive updated and
// what changed.
func (output TrawlCommandOutput) WriteArchiveUpdate(operation *federation.FederatedTrawlerArchiveUpdateOperation) error {
	writer := output.standardOutput
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
			status:  "updated",
			changes: archiveUpdateChanges(result),
		})
	}
	for _, failure := range operation.GetOperationFailures() {
		if failure == nil {
			continue
		}
		reason := ""
		if len(failure.GetPhotosStepsNotBuilt()) > 0 {
			reason = photosStepsNotBuiltSentence(failure.GetPhotosStepsNotBuilt())
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName()),
			status:  "failed",
			changes: reason,
		})
	}
	for _, skipped := range operation.GetTrawlersSkippedFromOperation() {
		if skipped == nil {
			continue
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: strings.TrimSpace(skipped.GetRegisteredTrawlerDisplayName()),
			status:  "not supported",
		})
	}
	for _, failure := range operation.GetPeopleArchiveUpdateFailuresAfterTrawlerArchiveUpdate() {
		if failure == nil {
			continue
		}
		rows = append(rows, archiveUpdateHumanRow{
			trawler: "People",
			status:  "failed",
			changes: "from " + strings.TrimSpace(failure.GetSuccessfullyUpdatedTrawlerDisplayName()),
		})
	}
	return writeArchiveUpdateHumanRows(writer, rows)
}

func writeArchiveUpdateHumanRows(writer io.Writer, rows []archiveUpdateHumanRow) error {
	tableRows := make([][]string, 0, len(rows))
	for _, row := range rows {
		tableRows = append(tableRows, []string{row.trawler, row.status, row.changes})
	}
	return writeTable(writer, []TableColumn{
		{Header: columnHeaderApp},
		{Header: "update"},
		{Header: "changes", Wrap: true},
	}, tableRows, 2)
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
