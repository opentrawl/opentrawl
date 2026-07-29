package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenEvent(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (archive.EventDetail, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.EventDetail{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolved, err := c.resolveOpenRef(ctx, req, ref)
	if err != nil {
		return archive.EventDetail{}, err
	}
	event, err := st.OpenEvent(ctx, resolved)
	if errors.Is(err, archive.ErrEventNotFound) {
		return archive.EventDetail{}, commandErr(1, "not_found", errors.New("No event has that link."))
	}
	if err != nil {
		return archive.EventDetail{}, err
	}
	return event, nil
}

func (c *Crawler) resolveOpenRef(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		if _, ok := archive.UIDFromRef(ref); ok {
			return ref, nil
		}
		return "", commandErr(1, "invalid_ref", errors.New("The Calendar event link is not valid."))
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", commandErr(1, "unknown_short_ref", errors.New("No event has that link."))
	}
	matches, err := req.ResolveShortReference(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr(1, "unknown_short_ref", errors.New("No event has that link."))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", errors.New("More than one event has that link."))
	}
	if err != nil {
		return "", err
	}
	return matches[0], nil
}
