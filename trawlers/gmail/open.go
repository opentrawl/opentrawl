package gmail

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/gmail/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

const maxOpenBodyRunes = 4000

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
	return boundOpenResult(result), nil
}

func (c *Crawler) resolveOpenRef(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (string, error) {
	matches, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr("unknown_short_ref", "short ref is unknown", err)
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr("ambiguous_short_ref", "short ref is ambiguous", err)
	}
	if err != nil {
		return "", err
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(matches[0]), nil
}

func boundOpenResult(result archive.OpenResult) archive.OpenResult {
	body, elided := truncateOpenBody(result.Body)
	result.Body = body
	result.BodyTruncated = elided > 0
	result.BodyElidedChars = elided
	return result
}

func truncateOpenBody(body string) (string, int) {
	runes := []rune(body)
	if len(runes) <= maxOpenBodyRunes {
		return body, 0
	}
	return string(runes[:maxOpenBodyRunes]), len(runes) - maxOpenBodyRunes
}
