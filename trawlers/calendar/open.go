package calendar

import (
	"context"
	"errors"
	"fmt"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) loadOpenEvent(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalCalendarEventReference *trawlkit.CanonicalArchiveRecordReference,
) (archive.EventDetail, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.EventDetail{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	event, err := st.OpenEvent(ctx, trawlkit.CanonicalArchiveRecordReferenceText(canonicalCalendarEventReference))
	if errors.Is(err, archive.ErrEventNotFound) {
		return archive.EventDetail{}, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounEvent))
	}
	if err != nil {
		return archive.EventDetail{}, err
	}
	return event, nil
}
