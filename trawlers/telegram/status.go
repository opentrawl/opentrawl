package telegram

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	statusv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*statusv1.TrawlerStatusResponse, error) {
	status := &statusv1.TrawlerArchiveStatus{}
	response := &statusv1.TrawlerStatusResponse{TrawlerArchiveStatus: status}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}
	archiveStore, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	defer func() { _ = archiveStore.Close() }()
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return response, nil
	}
	if !archiveStatus.HasSuccessfullyCompletedArchiveSync {
		return response, nil
	}
	status.TrawlerArchiveCanAnswerCurrentCommands = true
	status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.ArchiveMessageCountAfterLastSuccessfullyCompletedSync)},
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: uint64(archiveStatus.ArchiveConversationCountAfterLastSuccessfullyCompletedSync)},
	}
	if archiveStatus.ArchiveFolderCountAfterLastSuccessfullyCompletedSync > 0 {
		status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = append(status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync, &statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{ArchiveContentKindName: "folders", ArchiveContentKindDisplayName: "folders", ArchiveContentCount: uint64(archiveStatus.ArchiveFolderCountAfterLastSuccessfullyCompletedSync)})
	}
	if !archiveStatus.LastSuccessfullyCompletedArchiveSyncTime.IsZero() {
		status.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(archiveStatus.LastSuccessfullyCompletedArchiveSyncTime)
	}
	return response, nil
}
