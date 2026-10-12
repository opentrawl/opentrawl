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

func (c *Crawler) resolveOpenRef(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*trawlkit.CanonicalArchiveRecordReference, error) {
	if !trawlkit.ValidShortRef(trawlkit.LocalTrawlerShortReferenceText(localShortReference)) {
		return nil, commandErr(1, "unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounEvent))
	}
	matches, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandErr(1, "unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounEvent))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, commandErr(1, "ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounEvent))
	}
	if err != nil {
		return nil, err
	}
	return matches[0], nil
}
