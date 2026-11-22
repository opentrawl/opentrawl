package gmail

import (
	"context"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (archive.OpenResult, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.OpenResult{}, archiveErr(err)
	}
	result, err := st.OpenMessage(ctx, trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReference))
	if err != nil {
		return archive.OpenResult{}, commandErr("message_not_found", "message could not be opened", err)
	}
	return result, nil
}
