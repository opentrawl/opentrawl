package render

import (
	"fmt"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
)

// WriteStatus writes the status facts for each app: what its archive holds,
// when it was last updated, its newest archived record, and whether it works.
func (output TrawlCommandOutput) WriteStatus(operation *federation.FederatedTrawlerStatusOperation) error {
	writer := output.standardOutput
	if operation == nil {
		return fmt.Errorf("federated trawler status operation is missing")
	}
	rows := make([][]string, 0, len(operation.GetTrawlerStatusResults())+len(operation.GetOperationFailures()))
	seen := make(map[string]struct{})
	for _, result := range operation.GetTrawlerStatusResults() {
		if result == nil || result.GetTrawlerStatusResponse().GetTrawlerArchiveStatus() == nil {
			continue
		}
		archiveStatus := result.GetTrawlerStatusResponse().GetTrawlerArchiveStatus()
		identity := strings.TrimSpace(result.GetRegisteredTrawler().GetRegisteredTrawlerIdentity())
		displayName := strings.TrimSpace(result.GetRegisteredTrawlerDisplayName())
		if displayName == "" {
			displayName = strings.TrimSpace(result.GetRegisteredTrawlerCommandName())
		}
		rows = append(rows, []string{
			displayName,
			archivedContentCounts(archiveStatus.GetArchiveContentCountsAfterLastSuccessfullyCompletedUpdate()),
			statusLastUpdate(archiveStatus),
			statusNewestArchivedRecord(archiveStatus),
			statusWorks(archiveStatus),
		})
		seen[identity] = struct{}{}
	}
	for _, failure := range operation.GetOperationFailures() {
		if failure == nil {
			continue
		}
		identity := strings.TrimSpace(failure.GetFailedTrawler().GetRegisteredTrawlerIdentity())
		if _, exists := seen[identity]; exists {
			continue
		}
		displayName := strings.TrimSpace(failure.GetRegisteredTrawlerDisplayName())
		rows = append(rows, []string{displayName, "", "", "", archiveWorksNo})
	}
	if len(rows) == 0 {
		return writeLine(writer, NoTrawlersAreAvailable().commandFailureSentence)
	}
	if err := writeTable(writer, []TableColumn{
		{Header: columnHeaderApp, NeverTruncateCellValues: true},
		{Header: "archive", Wrap: true},
		archiveEventTimeColumn("last updated"),
		archiveEventTimeColumn("newest record"),
		{Header: "works", MinimumWidth: 6, Wrap: true},
	}, rows, 1); err != nil {
		return err
	}
	for _, result := range operation.GetTrawlerStatusResults() {
		if result.GetTrawlerStatusResponse().GetPhotosPipelineStatus() != nil && result.GetTrawlerStatusResponse().GetTrawlerArchiveStatus().GetTrawlerArchiveCanAnswerCurrentCommands() {
			if err := writeLines(writer, Wrap("Photos searches, lists and opens metadata archived from Apple Photos.", OutputWidth(writer))); err != nil {
				return err
			}
		}
		if err := writePhotosPipelineStatus(writer, result.GetTrawlerStatusResponse().GetPhotosPipelineStatus()); err != nil {
			return err
		}
	}
	return output.WriteTrawlerOperationFailures(
		operation.GetOperationFailures(),
		operation.GetTrawlersSkippedFromOperation(),
		true,
	)
}

const (
	archiveWorksYes       = "yes"
	archiveWorksNo        = "no"
	archiveWorksAppClosed = "app closed"
)

func statusWorks(archiveStatus *status.TrawlerArchiveStatus) string {
	if !archiveStatus.GetTrawlerArchiveCanAnswerCurrentCommands() {
		return archiveWorksNo
	}
	if archiveStatus.GetSourceAppSyncLiveness() == status.SourceAppSyncLiveness_SOURCE_APP_SYNC_LIVENESS_SOURCE_APP_NOT_RUNNING {
		return archiveWorksAppClosed
	}
	return archiveWorksYes
}

func archivedContentCounts(
	counts []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate,
) string {
	values := make([]string, 0, len(counts))
	for _, count := range counts {
		if count == nil {
			continue
		}
		name := strings.TrimSpace(count.GetArchiveContentKindDisplayName())
		if name == "" {
			if count.GetArchiveContentKindName() == "apple_library_photos" {
				name = "photos read from Apple Photos"
			} else {
				name = strings.TrimSpace(count.GetArchiveContentKindName())
			}
		}
		if name != "" {
			values = append(values, FormatInteger(int64(count.GetArchiveContentCount()))+" "+name)
		}
	}
	return strings.Join(values, ", ")
}

func statusLastUpdate(archiveStatus *status.TrawlerArchiveStatus) string {
	return formatExactTimeInThisComputerTimeZone(archiveStatus.GetLastSuccessfullyCompletedArchiveUpdateTime(), localTimeWithUTCOffsetForListRow)
}

func statusNewestArchivedRecord(archiveStatus *status.TrawlerArchiveStatus) string {
	return formatExactTimeInThisComputerTimeZone(archiveStatus.GetNewestArchivedRecordTime(), localTimeWithUTCOffsetForListRow)
}
