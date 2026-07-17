package telecrawl

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlkit"
)

func (c *Crawler) loadOpenMessage(ctx context.Context, req *trawlkit.Request, ref string) (store.MessageWindow, error) {
	r := c.handler(ctx, req)
	st, err := store.UseExisting(ctx, req.Store, req.Paths.Archive)
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
		return store.MessageWindow{}, r.contractError("not_found", "message was not found in this archive", "run trawl telegram search --json again and use one of the returned refs.")
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
			return 0, r.contractError("invalid_ref", "ref is not a telegram message ref", "use a ref returned by trawl telegram search --json, such as telegram:msg/<id>.")
		}
		return sourcePK, nil
	}
	fullRefs, err := r.req.ResolveShortRef(r.ctx, ref)
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "run trawl telegram search and copy the displayed short ref, or use a full ref from trawl telegram search --json.")
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return 0, r.contractError("ambiguous_short_ref", "short ref matches more than one archived message", "run trawl telegram search again and use the longer displayed ref or the full ref from trawl telegram search --json.")
	}
	if err != nil {
		return 0, err
	}
	if len(fullRefs) != 1 {
		return 0, r.contractError("unknown_short_ref", "short ref was not found in this archive", "run trawl telegram search and copy the displayed short ref, or use a full ref from trawl telegram search --json.")
	}
	sourcePK, err := parseMessageRef(fullRefs[0])
	if err != nil {
		return 0, err
	}
	return sourcePK, nil
}

func parseMessageRef(ref string) (int64, error) {
	prefix := store.MessageRefPrefix
	if !strings.HasPrefix(ref, prefix) {
		if !strings.HasPrefix(ref, store.LegacyMessageRefPrefix) {
			return 0, errors.New("invalid message ref")
		}
		prefix = store.LegacyMessageRefPrefix
	}
	rawID := strings.TrimPrefix(ref, prefix)
	if rawID == "" {
		return 0, errors.New("invalid message ref")
	}
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != rawID {
		return 0, errors.New("invalid message ref")
	}
	return id, nil
}
