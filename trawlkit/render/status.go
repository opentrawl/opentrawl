package render

import (
	"fmt"
	"io"
	"strings"

	federationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation/v1"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
)

func WriteFederatedTrawlerStatusOperation(
	writer io.Writer,
	operation *federationv1.FederatedTrawlerStatusOperation,
) error {
	if operation == nil {
		return fmt.Errorf("federated trawler status operation is missing")
	}
	rows := make([][]string, 0, len(operation.GetTrawlerStatusResults())+len(operation.GetOperationFailures()))
	seen := make(map[string]struct{})
	for _, result := range operation.GetTrawlerStatusResults() {
		if result == nil || result.GetTrawlerStatusResponse().GetTrawlerArchiveStatus() == nil {
			continue
		}
		status := result.GetTrawlerStatusResponse().GetTrawlerArchiveStatus()
		identity := strings.TrimSpace(result.GetRegisteredTrawler().GetRegisteredTrawlerIdentity())
		displayName := strings.TrimSpace(result.GetRegisteredTrawlerDisplayName())
		if displayName == "" {
			displayName = strings.TrimSpace(result.GetRegisteredTrawlerCommandName())
		}
		works := "no"
		if status.GetTrawlerArchiveCanAnswerCurrentCommands() {
			works = "yes"
		}
		rows = append(rows, []string{
			displayName,
			archivedContentCounts(status.GetArchiveContentCountsAfterLastSuccessfullyCompletedSync()),
			statusLastSync(status),
			works,
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
		rows = append(rows, []string{displayName, "", "", "no"})
	}
	return WriteTable(writer, []TableColumn{
		{Header: "trawler"},
		{Header: "archived", Wrap: true, MaximumWrappedLines: 2},
		{Header: "last update"},
		{Header: "works"},
	}, rows)
}

func archivedContentCounts(
	counts []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync,
) string {
	values := make([]string, 0, len(counts))
	for _, count := range counts {
		if count == nil {
			continue
		}
		name := strings.TrimSpace(count.GetArchiveContentKindDisplayName())
		if name == "" {
			name = strings.TrimSpace(count.GetArchiveContentKindName())
		}
		if name != "" {
			values = append(values, FormatInteger(int64(count.GetArchiveContentCount()))+" "+name)
		}
	}
	return strings.Join(values, ", ")
}

func statusLastSync(status *statusv1.TrawlerArchiveStatus) string {
	timestamp := status.GetLastSuccessfullyCompletedArchiveSyncTime()
	if timestamp == nil || !timestamp.IsValid() {
		return ""
	}
	return ShortLocalTime(timestamp.AsTime())
}
