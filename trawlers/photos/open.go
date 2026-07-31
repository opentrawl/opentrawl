package photos

import (
	"context"
	"errors"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenAsset(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (archive.OpenResult, error) {
	anchorID := ""
	if req != nil {
		anchorID = trawlkit.RecordAnchorIdentifierText(req.RequestedRecordAnchor)
	}
	return c.loadOpenAssetForAnchor(ctx, req, localShortReference, anchorID)
}

func (c *Crawler) loadOpenAssetForAnchor(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
	anchorID string,
) (archive.OpenResult, error) {
	resolved, err := c.resolveInputRef(ctx, req, localShortReference)
	if err != nil {
		return archive.OpenResult{}, err
	}
	result, err := archive.OpenWithStoreFocused(ctx, req.OpenedTrawlerArchiveStore, resolved, anchorID)
	if err != nil {
		return archive.OpenResult{}, archiveReadCommandError(err)
	}
	return result, nil
}

func archiveReadCommandError(err error) error {
	return err
}

func (c *Crawler) resolveInputRef(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (string, error) {
	if !trawlkit.ValidShortRef(trawlkit.LocalTrawlerShortReferenceText(localShortReference)) {
		return "", commandError{
			Code:    "invalid_ref",
			Message: "ref is not a photos asset ref",
		}
	}
	fullRefs, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found"}
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset"}
	}
	if err != nil {
		return "", err
	}
	if len(fullRefs) != 1 {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found"}
	}
	return trawlkit.CanonicalArchiveRecordReferenceText(fullRefs[0]), nil
}
