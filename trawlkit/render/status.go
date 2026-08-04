package render

import (
	"fmt"
	"io"
	"strings"

	federation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/federation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
)

func WriteFederatedTrawlerStatusOperation(
	writer io.Writer,
	operation *federation.FederatedTrawlerStatusOperation,
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
		readiness := "not ready"
		if status.GetTrawlerArchiveCanAnswerCurrentCommands() {
			readiness = "ready"
		}
		rows = append(rows, []string{
			displayName,
			archivedContentCounts(status.GetArchiveContentCountsAfterLastSuccessfullyCompletedUpdate()),
			statusLastUpdate(status),
			readiness,
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
		rows = append(rows, []string{displayName, "", "", "not ready"})
	}
	return WriteTable(writer, []TableColumn{
		{Header: "app"},
		{Header: "archive", Wrap: true, MaximumWrappedLines: 2},
		{Header: "last updated"},
		{Header: "ready"},
	}, rows)
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
			name = strings.TrimSpace(count.GetArchiveContentKindName())
		}
		if name != "" {
			values = append(values, FormatInteger(int64(count.GetArchiveContentCount()))+" "+name)
		}
	}
	return strings.Join(values, ", ")
}

func statusLastUpdate(status *status.TrawlerArchiveStatus) string {
	timestamp := status.GetLastSuccessfullyCompletedArchiveUpdateTime()
	if timestamp == nil || !timestamp.IsValid() {
		return ""
	}
	return ShortLocalTime(timestamp.AsTime())
}
