package telegram

import (
	"context"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
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
		return nil, err
	}
	defer func() { _ = archiveStore.Close() }()
	archiveStatus, err := archiveStore.Status(ctx)
	if err != nil {
		return nil, err
	}
	if !archiveStatus.HasSuccessfullyCompletedArchiveUpdate {
		return response, nil
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands = true
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.ArchiveMessageCountAfterLastSuccessfullyCompletedUpdate)},
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: uint64(archiveStatus.ArchiveConversationCountAfterLastSuccessfullyCompletedUpdate)},
	}
	if archiveStatus.ArchiveFolderCountAfterLastSuccessfullyCompletedUpdate > 0 {
		trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = append(trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate, &status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{ArchiveContentKindName: "folders", ArchiveContentKindDisplayName: "folders", ArchiveContentCount: uint64(archiveStatus.ArchiveFolderCountAfterLastSuccessfullyCompletedUpdate)})
	}
	if !archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime)
	}
	return response, nil
}
