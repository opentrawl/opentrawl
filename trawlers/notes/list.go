package notes

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	note "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/note"
)

func (c *Crawler) runList(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) > 1 {
		return nil, usageError("notes takes at most one folder name")
	}
	limit, err := ckflags.Limit(c.listLimit, true)
	if err != nil {
		return nil, usageError(err.Error())
	}
	folder := ""
	if len(req.TrawlerCommandPositionalArguments) == 1 {
		folder = strings.TrimSpace(req.TrawlerCommandPositionalArguments[0])
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	if folder != "" {
		if err := checkKnownFolder(ctx, archiveStore, folder); err != nil {
			return nil, err
		}
	}
	folders, err := archiveStore.FolderCounts(ctx, folder)
	if err != nil {
		return nil, err
	}
	totalNoteCount := folderTotal(folders)
	archivedNotes, err := archiveStore.ListNotes(ctx, folder, limit)
	if err != nil {
		return nil, err
	}
	noteRecords := make([]*note.NoteRecord, 0, len(archivedNotes))
	for _, archivedNote := range archivedNotes {
		noteRecords = append(noteRecords, &note.NoteRecord{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archivedNote.Ref),
			NoteDisplayName:          noteListDisplayName(archivedNote.Title),
			NoteFolderDisplayName:    noteFolderDisplayName(archivedNote.Folder),
			NoteModifiedTime:         parsedNotesTimestamp(archivedNote.ModifiedAt),
		})
	}
	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info("list_complete", fmt.Sprintf("returned=%d total=%d folders=%d", len(archivedNotes), totalNoteCount, len(folders)))
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_NoteListResponse{
			NoteListResponse: &note.NoteListResponse{
				NoteRecordsNewestFirst: noteRecords,
				TotalMatchingNoteCount: uint64(max(totalNoteCount, 0)),
				MoreMatchingNotesExist: int64(len(archivedNotes)) < totalNoteCount,
			},
		},
	}, nil
}

func (c *Crawler) runFolders(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 0 {
		return nil, usageError("folders takes no arguments")
	}
	archiveStore, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	folders, err := archiveStore.FolderCounts(ctx, "")
	if err != nil {
		return nil, err
	}
	folderRecords := make([]*note.NoteFolderRecord, 0, len(folders))
	for _, folder := range folders {
		folderRecords = append(folderRecords, &note.NoteFolderRecord{
			NoteFolderDisplayName:      noteFolderDisplayName(folder.Folder),
			NoteCount:                  uint64(max(folder.Notes, 0)),
			MostRecentNoteModifiedTime: parsedNotesTimestamp(folder.LastModified),
		})
	}
	return &command.TrawlerCommandResponse{
		TypedTrawlerCommandResponse: &command.TrawlerCommandResponse_NoteFolderListResponse{
			NoteFolderListResponse: &note.NoteFolderListResponse{
				NoteFolderRecordsInDisplayOrder: folderRecords,
			},
		},
	}, nil
}

func checkKnownFolder(ctx context.Context, archiveStore *archive.Store, folder string) error {
	knownFolders, err := archiveStore.KnownFolders(ctx)
	if err != nil {
		return err
	}
	for _, knownFolder := range knownFolders {
		if knownFolder == folder {
			return nil
		}
	}
	return commandErr("not_found", "No folder has that name.", nil)
}

func noteListDisplayName(title string) string {
	if strings.TrimSpace(title) == "" {
		return "(untitled note)"
	}
	return title
}

func noteFolderDisplayName(folder string) string {
	return strings.TrimSpace(folder)
}

func folderTotal(folders []archive.FolderCount) int64 {
	var total int64
	for _, folder := range folders {
		total += folder.Notes
	}
	return total
}
