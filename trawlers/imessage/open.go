package imessage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/imessage/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/render"
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
	canonicalRecordReference *trawlkit.CanonicalArchiveRecordReference,
) (archive.MessageContext, error) {
	st, err := archive.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return archive.MessageContext{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	messageID, err := parseMessageRef(trawlkit.CanonicalArchiveRecordReferenceText(canonicalRecordReference))
	if err != nil {
		return archive.MessageContext{}, err
	}
	result, err := st.OpenMessage(ctx, messageID, defaultOpenWindow)
	if errors.Is(err, archive.ErrMessageNotFound) {
		return archive.MessageContext{}, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	if err != nil {
		return archive.MessageContext{}, err
	}
	return result, nil
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
