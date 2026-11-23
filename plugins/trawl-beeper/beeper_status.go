package beeper

import (
	"context"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	status "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Status reports archived message and conversation counts, last successful
// update time, newest archived message time, and whether local queries work.
func (t *BeeperTrawler) Status(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*status.TrawlerStatusResponse, error) {
	archiveStatus := &status.TrawlerArchiveStatus{}
	response := &status.TrawlerStatusResponse{TrawlerArchiveStatus: archiveStatus}
	if req.OpenedTrawlerArchiveStore == nil {
		return response, nil
	}

	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return response, nil
	}
	summary, err := st.Status(ctx)
	if err != nil {
		return response, nil
	}

	archiveStatus.ArchiveContentCountsAfterLastSuccessfullyCompletedUpdate = []*status.ArchiveContentCountAfterLastSuccessfullyCompletedUpdate{
		{
			ArchiveContentKindName:        "messages",
			ArchiveContentKindDisplayName: "messages",
			ArchiveContentCount:           uint64(max(summary.MessageCount, 0)),
		},
		{
			ArchiveContentKindName:        "conversations",
			ArchiveContentKindDisplayName: "conversations",
			ArchiveContentCount:           uint64(max(summary.ChatCount, 0)),
		},
	}
	if !summary.LastCompletedAt.IsZero() {
		archiveStatus.LastSuccessfullyCompletedArchiveUpdateTime = timestamppb.New(summary.LastCompletedAt)
	}
	if !summary.NewestMessageAt.IsZero() {
		archiveStatus.NewestArchivedRecordTime = timestamppb.New(summary.NewestMessageAt)
	}

	// Local read/search commands remain operable whenever the archive has been populated,
	// even if Beeper Desktop is temporarily closed or asleep in macOS App Nap.
	archiveStatus.TrawlerArchiveCanAnswerCurrentCommands = summary.MessageCount > 0 || summary.ChatCount > 0 || !summary.LastCompletedAt.IsZero()
	return response, nil
}

func (t *BeeperTrawler) runNetworks(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	st, err := beeperstore.UseExisting(req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(err)
	}
	summaries, err := st.ListNetworkSummaries(ctx)
	if err != nil {
		return nil, err
	}

	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(summaries))
	for _, item := range summaries {
		newestValue := &presentation.TrawlerSpecificCommandPresentationValue{
			TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: ""},
		}
		if !item.NewestMessageAt.IsZero() {
			newestValue = &presentation.TrawlerSpecificCommandPresentationValue{
				TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_ArchiveRecordAssociatedTimeForDisplay{
					ArchiveRecordAssociatedTimeForDisplay: &presentation.ArchiveRecordAssociatedTimeForDisplay{
						ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{
							ExactTime: timestamppb.New(item.NewestMessageAt),
						},
					},
				},
			}
		}
		rows = append(rows, &presentation.TrawlerSpecificCommandListPresentationRow{
			ColumnValuesInDisplayOrder: []*presentation.TrawlerSpecificCommandPresentationValue{
				{TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: item.Network}},
				{TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{UnsignedCount: uint64(max(item.ConversationCount, 0))}},
				{TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_UnsignedCount{UnsignedCount: uint64(max(item.MessageCount, 0))}},
				newestValue,
				{TypedValue: &presentation.TrawlerSpecificCommandPresentationValue_Text{Text: item.AccountStatus}},
			},
		})
	}

	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_TrawlerSpecificCommandResponse{
			TrawlerSpecificCommandResponse: &command.TrawlerSpecificCommandResponse{
				TrawlerSpecificCommandPresentation: &command.TrawlerSpecificCommandResponse_TrawlerSpecificCommandListPresentation{
					TrawlerSpecificCommandListPresentation: &presentation.TrawlerSpecificCommandListPresentation{
						ColumnDisplayNamesInOrder: []string{
							"network",
							"conversations",
							"messages",
							"newest message",
							"account status",
						},
						RowsInDisplayOrder: rows,
						TotalRowCount: &presentation.TrawlerSpecificCommandListPresentation_ExactTotalRowCount{
							ExactTotalRowCount: uint64(len(rows)),
						},
						ConciseTextShownWhenListIsEmpty: "No Beeper networks have been archived yet.",
					},
				},
			},
		},
	}, nil
}
