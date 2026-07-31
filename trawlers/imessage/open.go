package imessage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

const (
	defaultOpenWindow = 10
	messageRefPrefix  = archive.MessageRefPrefix
)

var (
	errForeignRef = errors.New("ref is not from imessage")
	errInvalidRef = errors.New("ref is not an imessage message ref")
)

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (archive.MessageContext, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.MessageContext{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := c.resolveOpenMessageID(ctx, req, localShortReference)
	if err != nil {
		return archive.MessageContext{}, err
	}
	result, err := st.OpenMessage(ctx, messageID, defaultOpenWindow)
	if errors.Is(err, archive.ErrMessageNotFound) {
		return archive.MessageContext{}, commandErr(1, "not_found", output.HumanFacingErrorMessage("No message has that link."))
	}
	if err != nil {
		return archive.MessageContext{}, err
	}
	return result, nil
}

func (c *Crawler) resolveOpenMessageID(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (string, error) {
	if !trawlkit.ValidShortRef(trawlkit.LocalTrawlerShortReferenceText(localShortReference)) {
		return "", commandErr(1, "invalid_ref", output.HumanFacingErrorMessage("The iMessage link is not valid."))
	}
	resolved, err := req.ResolveShortReference(ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return "", commandErr(1, "unknown_short_ref", output.HumanFacingErrorMessage("No message has that link."))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return "", commandErr(1, "ambiguous_short_ref", output.HumanFacingErrorMessage("More than one message has that link."))
	}
	if err != nil {
		return "", err
	}
	messageID, err := parseMessageRef(trawlkit.CanonicalArchiveRecordReferenceText(resolved[0]))
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
