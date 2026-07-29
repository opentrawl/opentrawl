package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenMessage(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, ref string) (store.MessageWindow, error) {
	r := c.handler(ctx, req)
	st, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return store.MessageWindow{}, archiveErr(fmt.Errorf("open archive: %w", err))
	}
	defer func() { _ = st.Close() }()
	sourcePK, err := r.resolveOpenMessageRef(ref)
	if err != nil {
		return store.MessageWindow{}, err
	}
	window, err := st.OpenMessageWindow(ctx, sourcePK, openContextRadius)
	if errors.Is(err, store.ErrMessageNotFound) {
		return store.MessageWindow{}, r.contractError("not_found", "No message has that link.")
	}
	if err != nil {
		return store.MessageWindow{}, err
	}
	return window, nil
}

func (r *runtime) resolveOpenMessageRef(ref string) (int64, error) {
	ref = strings.TrimSpace(ref)
	if strings.Contains(ref, ":") {
		sourcePK, err := parseMessageRef(ref)
		if err != nil {
			return 0, r.contractError("invalid_ref", "The Telegram message link is not valid.")
		}
		return sourcePK, nil
	}
	fullRefs, err := r.req.ResolveShortReference(r.ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return 0, r.contractError("unknown_short_ref", "No message has that link.")
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return 0, r.contractError("ambiguous_short_ref", "More than one message has that link.")
	}
	if err != nil {
		return 0, err
	}
	if len(fullRefs) != 1 {
		return 0, r.contractError("unknown_short_ref", "No message has that link.")
	}
	sourcePK, err := parseMessageRef(fullRefs[0])
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
