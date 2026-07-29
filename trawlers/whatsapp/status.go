package whatsapp

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
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
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return response, nil
	}
	status.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*statusv1.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.Messages)},
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: uint64(archiveStatus.Chats)},
	}
	if !archiveStatus.LastImportAt.IsZero() {
		status.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(archiveStatus.LastImportAt)
	}
	status.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}
