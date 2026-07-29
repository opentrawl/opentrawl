package imessage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
)

const (
	defaultOpenWindow = 10
	messageRefPrefix  = archive.MessageRefPrefix
)

var (
	errForeignRef = errors.New("ref is not from imessage")
	errInvalidRef = errors.New("ref is not an imessage message ref")
)

func (c *Crawler) loadOpenMessage(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (archive.MessageContext, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.MessageContext{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenRef(ctx, req, ref)
	if err != nil {
		return archive.MessageContext{}, err
	}
	result, err := st.OpenMessage(ctx, messageID, defaultOpenWindow)
	if errors.Is(err, archive.ErrMessageNotFound) {
		return archive.MessageContext{}, commandErr(1, "not_found", errors.New("No message has that link."))
	}
	if err != nil {
		return archive.MessageContext{}, err
	}
	return result, nil
}

func (c *Crawler) resolveOpenRef(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.Contains(ref, ":") {
		return c.resolveShortRef(ctx, req, ref)
	}
	messageID, err := parseMessageRef(ref)
	if err != nil {
		if errors.Is(err, errForeignRef) {
			return "", commandErr(1, "foreign_ref", errors.New("The link is not for iMessage."))
		}
		return "", commandErr(1, "invalid_ref", errors.New("The iMessage link is not valid."))
	}
	return messageID, nil
}

func (c *Crawler) resolveShortRef(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, alias string) (string, error) {
	if !trawlkit.ValidShortRef(alias) {
		return "", commandErr(1, "invalid_ref", errors.New("The iMessage link is not valid."))
	}
	resolved, err := req.ResolveShortReference(ctx, alias)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr(1, "unknown_short_ref", errors.New("No message has that link."))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", errors.New("More than one message has that link."))
	}
	if err != nil {
		return "", err
	}
	messageID, err := parseMessageRef(resolved[0])
	if err != nil {
		return "", err
	}
	return messageID, nil
}

func parseMessageRef(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, messageRefPrefix) {
		return "", errForeignRef
	}
	messageID := strings.TrimPrefix(ref, messageRefPrefix)
	if messageID == "" || strings.TrimSpace(messageID) != messageID {
		return "", errInvalidRef
	}
	id, err := strconv.ParseInt(messageID, 10, 64)
	if err != nil || id <= 0 {
		return "", errInvalidRef
	}
	return messageID, nil
}
