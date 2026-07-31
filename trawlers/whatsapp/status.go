package whatsapp

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/whatsapp/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	trawlerArchiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: trawlerArchiveStatus}
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
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.Messages)},
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: uint64(archiveStatus.Chats)},
	}
	if !archiveStatus.LastImportAt.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(archiveStatus.LastImportAt)
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	return response, nil
}
