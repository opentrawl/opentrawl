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
	archiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference, err :=
		archiveStore.ArchiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference(ctx)
	if err != nil {
		return response, nil
	}
	trawlerArchiveStatus.TrawlerArchiveCanAnswerCurrentCommands =
		archiveCanResolveEveryMessageAndConversationToLocalTrawlerShortReference
	trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedSync{
		{ArchiveContentKindName: "messages", ArchiveContentKindDisplayName: "messages", ArchiveContentCount: uint64(archiveStatus.ArchiveMessageCountAfterLastSuccessfullyCompletedSync)},
		{ArchiveContentKindName: "conversations", ArchiveContentKindDisplayName: "conversations", ArchiveContentCount: uint64(archiveStatus.ArchiveConversationCountAfterLastSuccessfullyCompletedSync)},
	}
	if archiveStatus.ArchiveFolderCountAfterLastSuccessfullyCompletedSync > 0 {
		trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedSync = append(trawlerArchiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedSync, &status.ArchiveContentCountAfterLastSuccessfullyCompletedSync{ArchiveContentKindName: "folders", ArchiveContentKindDisplayName: "folders", ArchiveContentCount: uint64(archiveStatus.ArchiveFolderCountAfterLastSuccessfullyCompletedSync)})
	}
	if !archiveStatus.LastSuccessfullyCompletedArchiveSyncTime.IsZero() {
		trawlerArchiveStatus.LastSuccessfullyCompletedArchiveSyncTime = timestamppb.New(archiveStatus.LastSuccessfullyCompletedArchiveSyncTime)
	}
	return response, nil
}
