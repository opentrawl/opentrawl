package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

func (c *Crawler) loadOpenMessage(
	ctx context.Context,
	req *trawlkit.TrawlerCommandExecutionRequest,
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (*message.OpenedMessageRecordWithConversationContext, error) {
	r := c.handler(ctx, req)
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	sourcePK, err := r.resolveOpenMessageReference(localShortReference)
	if err != nil {
		return nil, err
	}
	window, err := st.OpenMessageWindow(ctx, sourcePK, openContextRadius)
	if errors.Is(err, store.ErrMessageNotFound) {
		return nil, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	if err != nil {
		return nil, err
	}
	return projectOpenedMessageRecordWithConversationContext(ctx, st, window)
}

func (r *runtime) resolveOpenMessageReference(
	localShortReference *trawlkit.LocalTrawlerShortReference,
) (int64, error) {
	fullRefs, err := r.req.ResolveShortReference(r.ctx, localShortReference)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return 0, commandErr(1, "unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return 0, commandErr(1, "ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounMessage))
	}
	if err != nil {
		return 0, err
	}
	if len(fullRefs) != 1 {
		return 0, commandErr(1, "unknown_short_ref", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	sourcePK, err := parseMessageRef(trawlkit.CanonicalArchiveRecordReferenceText(fullRefs[0]))
	if err != nil {
		return 0, err
	}
	return sourcePK, nil
}

func parseMessageRef(ref string) (int64, error) {
	if !strings.HasPrefix(ref, store.MessageRefPrefix) {
		return 0, errors.New("invalid message ref")
	}
	rawID := strings.TrimPrefix(ref, store.MessageRefPrefix)
	if rawID == "" {
		return 0, errors.New("invalid message ref")
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rawID {
		return 0, errors.New("invalid message ref")
	}
	return id, nil
}
