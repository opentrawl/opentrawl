package notes

import (
	"context"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	command "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/command"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func (c *Crawler) runVersions(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 1 {
		return nil, usageError("Versions needs a note link.")
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
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(versions))
	for versionIndex, version := range versions {
		versionTime := version.SourceModifiedAt
		if strings.TrimSpace(versionTime) == "" {
			versionTime = version.FirstObservedAt
		}
		rows = append(rows, notesListRow(
			notesPresentationTimeValue(versionTime),
			notesPresentationTextValue(versionPosition(versionIndex)),
			notesPresentationCanonicalRecordReferenceValue(version.Ref),
		))
	}
	return notesListCommandResponse(
		[]string{"When", "Version", "Link"},
		rows,
		uint64(len(rows)),
		false,
	), nil
}

func (c *Crawler) runAtTime(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*command.TrawlerCommandResponse, error) {
	if len(req.TrawlerCommandPositionalArguments) != 2 {
		return nil, usageError("At-time needs a note link and time.")
	}
	requestedTime, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(req.TrawlerCommandPositionalArguments[1])
	if err != nil {
		return nil, usageError("Time " + err.Error())
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
	result, err := archiveStore.AtTime(ctx, note, requestedTime)
	if err != nil {
		return nil, err
	}
	if result.Version == nil {
		return notesDetailCommandResponse("No recovered version found", []*presentation.TrawlerSpecificCommandDetailPresentationField{
			notesDetailTextField("Note", noteLabel(result.Note)),
			notesDetailTimeField("Requested", result.RequestedTime),
		}), nil
	}
	return notesDetailCommandResponse(noteLabel(result.Note), []*presentation.TrawlerSpecificCommandDetailPresentationField{
		notesDetailTimeField("Requested", result.RequestedTime),
		notesDetailTimeField("Version", result.Version.SourceModifiedAt),
		notesDetailCanonicalRecordReferenceField("Link", result.Version.Ref),
	}), nil
}

func versionPosition(index int) string {
	if index == 0 {
		return "latest"
	}
	return fmt.Sprintf("previous %d", index)
}
