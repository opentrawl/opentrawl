package photoscrawl

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenAsset(ctx context.Context, req *trawlkit.Request, ref string) (archive.OpenResult, error) {
	anchorID := ""
	if req != nil {
		anchorID = req.RequestedAnchorID
	}
	return c.loadOpenAssetForAnchor(ctx, req, ref, anchorID)
}

func (c *Crawler) loadOpenAssetForAnchor(ctx context.Context, req *trawlkit.Request, ref, anchorID string) (archive.OpenResult, error) {
	resolved, err := c.resolveInputRef(ctx, req, ref)
	if err != nil {
		return archive.OpenResult{}, err
	}
	result, err := archive.OpenWithStoreFocused(ctx, req.Store, resolved, anchorID)
	if err != nil {
		return archive.OpenResult{}, archiveReadCommandError(err)
	}
	return result, nil
}

func archiveReadCommandError(err error) error {
	var incompatible archive.ArchiveIncompatibleError
	if errors.As(err, &incompatible) {
		return commandError{
			Code:    "archive_incompatible",
			Message: "The Photos archive needs to be updated.",
			Remedy:  "run trawl sync photos, then retry",
		}
	}
	return err
}

func (c *Crawler) resolveInputRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") || strings.Contains(ref, "/") {
		return ref, nil
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", commandError{
			Code:    "invalid_ref",
			Message: "ref is not a photos asset ref",
			Remedy:  "use a ref in the form photos:asset/ID or a short ref from search",
		}
	}
	fullRefs, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandError{Code: "ambiguous_short_ref", Message: "short ref matches more than one asset", Remedy: "rerun search or use the full ref"}
	}
	if err != nil {
		return "", err
	}
	if len(fullRefs) != 1 {
		return "", commandError{Code: "unknown_short_ref", Message: "short ref was not found", Remedy: "rerun search or use the full ref"}
	}
	return fullRefs[0], nil
}
