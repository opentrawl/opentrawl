package gogcrawl

import (
	"context"
	"errors"
	"strings"

	"github.com/opentrawl/opentrawl/gogcrawl/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

const maxOpenBodyRunes = 4000

func (c *Crawler) loadOpenMessage(ctx context.Context, req *trawlkit.Request, ref string) (archive.OpenResult, error) {
	st, err := archive.UseExisting(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archive.OpenResult{}, archiveErr(err)
	}
	resolved, err := c.resolveOpenRef(ctx, req, ref)
	if err != nil {
		return archive.OpenResult{}, err
	}
	result, err := st.OpenMessage(ctx, resolved)
	if err != nil {
		return archive.OpenResult{}, commandErr("message_not_found", "message could not be opened", "search again and pass a gmail:msg ref", err)
	}
	return boundOpenResult(result), nil
}

func (c *Crawler) resolveOpenRef(ctx context.Context, req *trawlkit.Request, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		return ref, nil
	}
	matches, err := req.ResolveShortRef(ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr("unknown_short_ref", "short ref is unknown", "use a full gmail:msg ref", err)
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr("ambiguous_short_ref", "short ref is ambiguous", "rerun search or use the full gmail:msg ref", err)
	}
	if err != nil {
		return "", err
	}
	return matches[0], nil
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
