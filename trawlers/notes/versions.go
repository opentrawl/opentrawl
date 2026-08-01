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
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
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
				return notesListCommandResponse(
					[]string{"When", "Version", "Link"},
					[]*presentation.TrawlerSpecificCommandListPresentationRow{notesVersionListRow(version, versionIndex)},
					1,
					false,
				), nil
			}
		}
		return notesEmptyListCommandResponse("No recovered version existed at " + requestedTimeText + "."), nil
	}
	totalVersionCount := len(versions)
	if totalVersionCount > limit {
		versions = versions[:limit]
	}
	rows := make([]*presentation.TrawlerSpecificCommandListPresentationRow, 0, len(versions))
	for versionIndex, version := range versions {
		rows = append(rows, notesVersionListRow(version, versionIndex))
	}
	return notesListCommandResponse(
		[]string{"When", "Version", "Link"},
		rows,
		uint64(totalVersionCount),
		totalVersionCount > len(rows),
	), nil
}

func notesVersionListRow(version archive.Version, versionIndex int) *presentation.TrawlerSpecificCommandListPresentationRow {
	versionTime := version.SourceModifiedAt
	if strings.TrimSpace(versionTime) == "" {
		versionTime = version.FirstObservedAt
	}
	return notesListRow(
		notesPresentationTimeValue(versionTime),
		notesPresentationTextValue(versionPosition(versionIndex)),
		notesPresentationCanonicalRecordReferenceValue(version.Ref),
	)
}

func versionPosition(index int) string {
	if index == 0 {
		return "latest"
	}
	return fmt.Sprintf("previous %d", index)
}
