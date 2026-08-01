package notes

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	notecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (c *Crawler) runVersions(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 1 {
		return nil, usageError("Versions needs a note link.")
	}
	limit, err := ckflags.Limit(c.versionListLimit, true)
	if err != nil {
		return nil, usageError(err.Error())
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	inputReference, err := resolveInputRef(ctx, req, req.TrawlerCommandPositionalArguments[0])
	if err != nil {
		return nil, err
	}
	note, err := archiveStore.ResolveNote(ctx, inputReference)
	if err != nil {
		return nil, noteLookupErrorForTrawlerCommand(err)
	}
	versions, err := archiveStore.Versions(ctx, note.ID)
	if err != nil {
		return nil, err
	}
	if requestedTimeText := strings.TrimSpace(c.versionAtOrBeforeTime); requestedTimeText != "" {
		requestedTime, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(requestedTimeText)
		if err != nil {
			return nil, usageError("Time " + err.Error())
		}
		requestedArchiveTime := notestime.Format(requestedTime)
		for versionIndex, version := range versions {
			if version.SourceModifiedAt != "" && version.SourceModifiedAt <= requestedArchiveTime {
				return recoveredNoteVersionListResponse(
					[]*notecontract.RecoveredNoteVersionRecord{recoveredNoteVersionRecord(version, versionIndex)},
					1,
					false,
					timestamppb.New(requestedTime),
				), nil
			}
		}
		return recoveredNoteVersionListResponse(nil, 0, false, timestamppb.New(requestedTime)), nil
	}
	totalVersionCount := len(versions)
	if totalVersionCount > limit {
		versions = versions[:limit]
	}
	versionRecords := make([]*notecontract.RecoveredNoteVersionRecord, 0, len(versions))
	for versionIndex, version := range versions {
		versionRecords = append(versionRecords, recoveredNoteVersionRecord(version, versionIndex))
	}
	return recoveredNoteVersionListResponse(
		versionRecords,
		uint64(totalVersionCount),
		totalVersionCount > len(versionRecords),
		nil,
	), nil
}

func recoveredNoteVersionRecord(version archive.Version, versionIndex int) *notecontract.RecoveredNoteVersionRecord {
	versionTime := version.SourceModifiedAt
	if strings.TrimSpace(versionTime) == "" {
		versionTime = version.FirstObservedAt
	}
	return &notecontract.RecoveredNoteVersionRecord{
		CanonicalRecordReference:                trawlkit.NewCanonicalArchiveRecordReference(version.Ref),
		RecoveredNoteVersionTime:                parsedNotesTimestamp(versionTime),
		NumberOfMoreRecentRecoveredNoteVersions: uint64(max(versionIndex, 0)),
	}
}

func recoveredNoteVersionListResponse(
	versionRecords []*notecontract.RecoveredNoteVersionRecord,
	totalVersionCount uint64,
	moreVersionsExist bool,
	requestedTime *timestamppb.Timestamp,
) *command.TrawlerCommandResponse {
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_RecoveredNoteVersionListResponse{
			RecoveredNoteVersionListResponse: &notecontract.RecoveredNoteVersionListResponse{
				RecoveredNoteVersionRecordsNewestFirst: versionRecords,
				TotalRecoveredNoteVersionCount:         totalVersionCount,
				MoreRecoveredNoteVersionsExist:         moreVersionsExist,
				RequestedNoteVersionAtOrBeforeTime:     requestedTime,
			},
		},
	}
}
