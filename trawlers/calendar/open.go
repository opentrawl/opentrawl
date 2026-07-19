package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/calendar/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenEvent(ctx context.Context, req *trawlkit.Request, ref string) (archive.EventDetail, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archive.EventDetail{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	resolved, err := c.resolveOpenRef(ctx, req, ref)
	if err != nil {
		return archive.EventDetail{}, err
	}
	event, err := st.OpenEvent(ctx, resolved)
	if err != nil {
		return archive.EventDetail{}, err
	}
	return event, nil
}

func (c *Crawler) resolveOpenRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return ref, nil
	}
	if !trawlkit.ValidShortRef(ref) {
		return "", commandErr(1, "unknown_short_ref", fmt.Errorf("unknown short ref %q", ref), "rerun search or use the full ref")
	}
	matches, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr(1, "unknown_short_ref", fmt.Errorf("unknown short ref %q", ref), "rerun search or use the full ref")
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", fmt.Errorf("short ref %q matches %d events", ref, len(matches)), "rerun search or use the full ref")
	}
	if err != nil {
		return "", err
	}
	return matches[0], nil
}
