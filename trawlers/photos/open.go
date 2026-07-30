package photos

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenAsset(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (archive.OpenResult, error) {
	anchorID := ""
	if req != nil {
		anchorID = req.RequestedPresentationAnchorIdentifier
	}
	return c.loadOpenAssetForAnchor(ctx, req, ref, anchorID)
}

func (c *Crawler) loadOpenAssetForAnchor(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref, anchorID string) (archive.OpenResult, error) {
	resolved, err := c.resolveInputRef(ctx, req, ref)
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

func (c *Crawler) resolveInputRef(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || strings.Contains(ref, "/") {
		return ref, nil
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", commandError{
			Code:    "invalid_ref",
			Message: "ref is not a photos asset ref",
		}
	}
	fullRefs, err := req.ResolveShortReference(ctx, ref)
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
	return fullRefs[0], nil
}
