package gmail

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (archive.OpenResult, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.OpenResult{}, archiveErr(err)
	}
	resolved, err := c.resolveOpenRef(ctx, req, localShortReference)
	if err != nil {
		return archive.OpenResult{}, err
	}
	result, err := st.OpenMessage(ctx, resolved)
	if err != nil {
		return archive.OpenResult{}, commandErr("message_not_found", "message could not be opened", err)
	}
	return result, nil
}

func (c *Crawler) resolveOpenRef(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (string, error) {
	matches, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr("unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage).Error(), err)
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr("ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounMessage).Error(), err)
	}
	if err != nil {
		return "", err
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(matches[0]), nil
}
